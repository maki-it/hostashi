// SPDX-License-Identifier: AGPL-3.0-or-later

package plan

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
)

// PHPImport is what the import makes of the old pool's PHP settings
// (ADR-0049): the allowlisted ones become the site's PHP settings, values
// out of bounds are clamped, and every other key is named.
type PHPImport struct {
	// Settings holds the values that differ from the Hostashi defaults.
	Settings agentapi.PHPSettings
	// Changed names each value that was clamped, mapped or dropped, as
	// "key: old value -> new value".
	Changed []string
	// NotTaken are the pool's other keys, as "php_admin_value[key]".
	// Keys the Hostashi pool sets itself are left out.
	NotTaken []string
}

// hostashiPoolKeys are set by the Hostashi pool itself (internal/php), so
// the old values do not matter.
var hostashiPoolKeys = map[string]bool{
	"open_basedir": true, "upload_tmp_dir": true, "sys_temp_dir": true, "session.save_path": true,
	"disable_functions": true, "allow_url_include": true,
}

var (
	phpSizeRe   = regexp.MustCompile(`^(-?[0-9]{1,13})([KkMmGg]?)$`)
	phpIntRe    = regexp.MustCompile(`^-?[0-9]{1,10}$`)
	nginxSizeRe = regexp.MustCompile(`^([0-9]{1,13})([KkMmGg]?)$`)
)

func unitShift(u string) uint {
	switch strings.ToLower(u) {
	case "k":
		return 10
	case "m":
		return 20
	case "g":
		return 30
	}

	return 0
}

// PHPSettingsOf maps a site's pool settings. php_admin_value wins over
// php_value, as in FPM.
func PHPSettingsOf(s scan.Site) PHPImport {
	var out PHPImport
	if s.Pool == nil {
		return out
	}

	vals := map[string]string{}

	keys := make([]string, 0, len(s.Pool.Settings))
	for k := range s.Pool.Settings {
		keys = append(keys, k)
	}

	slices.Sort(keys)

	for _, k := range keys {
		kind, name, ok := strings.Cut(strings.TrimSuffix(k, "]"), "[")
		if !ok {
			continue
		}

		v := strings.TrimSpace(s.Pool.Settings[k])

		switch {
		case hostashiPoolKeys[name]:
		case !allowlisted[name]:
			out.NotTaken = append(out.NotTaken, k)
		case strings.HasPrefix(kind, "php_admin_"):
			vals[name] = v
		default:
			if _, set := vals[name]; !set {
				vals[name] = v
			}
		}
	}

	m := mapper{d: agentapi.DefaultPHPSettings(), out: &out}

	for _, name := range allowlistOrder {
		if v, ok := vals[name]; ok {
			m.apply(name, v)
		}
	}

	// post_max_size below upload_max_filesize does not pass Validate, and
	// a default post_max_size follows a larger upload size.
	post := out.Settings.PostMaxSize
	if post == 0 {
		post = m.d.PostMaxSize
	}

	if up := out.Settings.Effective().UploadMaxFilesize; post < up {
		if _, given := vals["post_max_size"]; given || out.Settings.PostMaxSize != 0 {
			m.change("post_max_size", megs(post), megs(up)+" (the upload size)")
		}

		if out.Settings.PostMaxSize != 0 {
			out.Settings.PostMaxSize = up
		}
	}

	return out
}

// allowlistOrder are the ini keys of agentapi.PHPSettings.
var allowlistOrder = []string{
	"memory_limit", "upload_max_filesize", "post_max_size", "max_execution_time", "max_input_time",
	"max_input_vars", "max_file_uploads", "date.timezone", "display_errors", "error_reporting",
	"session.gc_maxlifetime", "short_open_tag",
}

var allowlisted = func() map[string]bool {
	m := map[string]bool{}
	for _, k := range allowlistOrder {
		m[k] = true
	}

	return m
}()

type mapper struct {
	d   agentapi.PHPSettings
	out *PHPImport
}

func (m mapper) change(key, from, to string) {
	m.out.Changed = append(m.out.Changed, key+": "+from+" -> "+to)
}

func megs(b uint64) string { return strconv.FormatUint(b/(1<<20), 10) + "M" }

// size reads a PHP size and clamps it to whole MiB within lo and hi; -1
// (no limit) becomes hi.
func (m mapper) size(key, v string, lo, hi uint64) (uint64, bool) {
	g := phpSizeRe.FindStringSubmatch(v)
	if g == nil {
		m.change(key, v, "default (not a size)")

		return 0, false
	}

	n, _ := strconv.ParseInt(g[1], 10, 64)
	if n < 0 {
		m.change(key, v, megs(hi))

		return hi, true
	}

	b := uint64(n) << unitShift(g[2])
	r := (b + (1 << 20) - 1) / (1 << 20) * (1 << 20)
	r = min(max(r, lo), hi)

	if r != b {
		m.change(key, v, megs(r))
	}

	return r, true
}

// integer reads a whole number and clamps it to lo and hi; 0 or below
// (no limit) becomes hi when unlimited is true.
func (m mapper) integer(key, v string, lo, hi int, unlimited bool) (int, bool) {
	if !phpIntRe.MatchString(v) {
		m.change(key, v, "default (not a number)")

		return 0, false
	}

	n, _ := strconv.Atoi(v)
	if n <= 0 {
		if !unlimited {
			m.change(key, v, "default")

			return 0, false
		}

		m.change(key, v, strconv.Itoa(hi))

		return hi, true
	}

	r := min(max(n, lo), hi)
	if r != n {
		m.change(key, v, strconv.Itoa(r))
	}

	return r, true
}

func (m mapper) flag(key, v string) (bool, bool) {
	switch strings.ToLower(v) {
	case "1", "on", "yes", "true":
		return true, true
	case "0", "off", "no", "false", "":
		return false, true
	}

	m.change(key, v, "default")

	return false, false
}

// errorReporting maps an expression onto the nearest preset.
func (m mapper) errorReporting(v string) string {
	x := strings.Join(strings.Fields(v), " ")
	switch {
	case x == "E_ALL" || x == "-1" || x == "32767":
		return agentapi.ErrorReportingDevelopment
	case x == "E_ALL & ~E_DEPRECATED":
		return agentapi.ErrorReportingProduction
	case strings.HasPrefix(x, "E_ALL & ~"):
		m.change("error_reporting", v, "production")

		return agentapi.ErrorReportingProduction
	case x == "E_ERROR | E_PARSE | E_CORE_ERROR | E_COMPILE_ERROR":
		return agentapi.ErrorReportingErrors
	}

	m.change("error_reporting", v, "default")

	return ""
}

func (m mapper) apply(name, v string) {
	s, d := &m.out.Settings, m.d

	setSize := func(dst *uint64, def, lo, hi uint64) {
		if n, ok := m.size(name, v, lo, hi); ok && n != def {
			*dst = n
		}
	}
	setInt := func(dst *int, def, lo, hi int, unlimited bool) {
		if n, ok := m.integer(name, v, lo, hi, unlimited); ok && n != def {
			*dst = n
		}
	}
	setFlag := func(dst **bool, def bool) {
		if b, ok := m.flag(name, v); ok && b != def {
			*dst = &b
		}
	}

	switch name {
	case "memory_limit":
		setSize(&s.MemoryLimit, d.MemoryLimit, agentapi.MinPHPMemoryLimit, agentapi.MaxPHPMemoryLimit)
	case "upload_max_filesize":
		setSize(&s.UploadMaxFilesize, d.UploadMaxFilesize, agentapi.MinPHPUploadSize, agentapi.MaxPHPUploadSize)
	case "post_max_size":
		// 0 means no limit in PHP.
		if v == "0" {
			v = "-1"
		}

		setSize(&s.PostMaxSize, d.PostMaxSize, agentapi.MinPHPUploadSize, agentapi.MaxPHPUploadSize)
	case "max_execution_time":
		setInt(&s.MaxExecutionTime, d.MaxExecutionTime, agentapi.MinPHPTime, agentapi.MaxPHPTime, true)
	case "max_input_time":
		setInt(&s.MaxInputTime, d.MaxInputTime, agentapi.MinPHPTime, agentapi.MaxPHPTime, false)
	case "max_input_vars":
		setInt(&s.MaxInputVars, d.MaxInputVars, agentapi.MinPHPInputVars, agentapi.MaxPHPInputVars, false)
	case "max_file_uploads":
		setInt(&s.MaxFileUploads, d.MaxFileUploads, agentapi.MinPHPFileUploads, agentapi.MaxPHPFileUploads, false)
	case "session.gc_maxlifetime":
		setInt(&s.SessionGCMaxLifetime, d.SessionGCMaxLifetime, agentapi.MinPHPSessionLifetime, agentapi.MaxPHPSessionLifetime, false)
	case "date.timezone":
		switch {
		case v == "" || v == d.Timezone:
		case agentapi.ValidTimezone(v):
			s.Timezone = v
		default:
			m.change(name, v, "default (unknown time zone)")
		}
	case "display_errors":
		setFlag(&s.DisplayErrors, *d.DisplayErrors)
	case "short_open_tag":
		setFlag(&s.ShortOpenTag, *d.ShortOpenTag)
	case "error_reporting":
		if p := m.errorReporting(v); p != "" && p != d.ErrorReporting {
			s.ErrorReporting = p
		}
	}
}

// bodyDiffers compares the old vhost's client_max_body_size with the
// request size the new site allows. It returns "" when they match or the
// old vhost set none; nginx's "0" (no limit) always differs.
func bodyDiffers(old string, ps agentapi.PHPSettings) string {
	if old == "" {
		return ""
	}

	now := ps.ClientMaxBodySize()

	g := nginxSizeRe.FindStringSubmatch(old)
	if g != nil {
		n, _ := strconv.ParseUint(g[1], 10, 64)
		if n<<unitShift(g[2]) == now {
			return ""
		}
	}

	return fmt.Sprintf("the old vhost took requests up to %s (client_max_body_size); here nginx takes up to post_max_size, %s. Change it on the site's PHP tab if needed", old, megs(now))
}

// countSet counts the settings that differ from the defaults.
func countSet(s agentapi.PHPSettings) int {
	b, err := json.Marshal(s)
	if err != nil {
		return 0
	}

	var m map[string]json.RawMessage
	if json.Unmarshal(b, &m) != nil {
		return 0
	}

	return len(m)
}
