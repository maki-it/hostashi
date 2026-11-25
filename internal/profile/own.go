// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"go.yaml.in/yaml/v4"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cron"
)

// Limits of an own profile (ADR-0069).
const (
	MaxNameLen       = 40
	MaxTitleLen      = 80
	MaxChecklist     = 20
	MaxChecklistText = 300
	MaxCron          = 10
	MaxCronCommand   = 500
	MaxCronNote      = 200
	// MaxYAMLBytes bounds an imported profile.
	MaxYAMLBytes = 64 << 10
	// YAMLVersion is the version of the YAML form that Export writes and
	// Import reads.
	YAMLVersion = 1
)

// ErrInvalid wraps every refusal of an own profile.
var ErrInvalid = errors.New("profile: invalid")

// nameRe is the name of an own profile: lower-case letters, digits and
// dashes, starting with a letter.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// minPHPRe is a PHP version as the panel names them, such as "8.2".
var minPHPRe = regexp.MustCompile(`^[0-9]{1,2}\.[0-9]{1,2}$`)

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// text checks free text that the panel only shows: one line of valid
// UTF-8 without control characters, not empty, at most max bytes.
func text(what, s string, maxLen int) error {
	switch {
	case strings.TrimSpace(s) == "":
		return invalid("%s is empty", what)
	case len(s) > maxLen:
		return invalid("%s is longer than %d bytes", what, maxLen)
	case !utf8.ValidString(s) || strings.IndexFunc(s, unicode.IsControl) >= 0:
		return invalid("%s has control characters or line breaks", what)
	}

	return nil
}

// CheckName checks the name of an own profile: its form, and that no
// built-in profile has it.
func CheckName(name string) error {
	if len(name) > MaxNameLen || !nameRe.MatchString(name) {
		return invalid("name %q: use up to %d lower-case letters, digits and dashes, starting with a letter", name, MaxNameLen)
	}

	if _, ok := Builtin(name); ok {
		return invalid("%q is the name of a built-in profile; choose another one", name)
	}

	return nil
}

// Check validates an own profile. It has the typed values of a built-in
// profile and nothing else: no nginx text reaches the agent through it.
func Check(p Profile) error {
	if err := CheckName(p.Name); err != nil {
		return err
	}

	if err := text("title", p.Title, MaxTitleLen); err != nil {
		return err
	}

	php := p.Type == "php"
	if !php && p.Type != "static" {
		return invalid("type %q: use php or static", p.Type)
	}

	if p.MinPHP != "" && (!php || !minPHPRe.MatchString(p.MinPHP)) {
		return invalid("lowest PHP version %q: use a version such as 8.2, on a PHP profile only", p.MinPHP)
	}

	if err := agentapi.ValidateWebDir(p.WebDir); err != nil {
		return invalid("%s", err.Error())
	}

	if err := p.Hardening.Validate(php); err != nil {
		return invalid("%s", err.Error())
	}

	if p.PageCache != "" && (!php || !slices.Contains(agentapi.PageCachePresets, p.PageCache)) {
		return invalid("page cache preset %q: use one of %s, on a PHP profile only", p.PageCache, strings.Join(agentapi.PageCachePresets, ", "))
	}

	if len(p.Cron) > MaxCron {
		return invalid("more than %d suggested cron jobs", MaxCron)
	}

	for _, c := range p.Cron {
		if _, err := cron.OnCalendar(c.Schedule); err != nil {
			return invalid("cron schedule %q: %s", c.Schedule, err.Error())
		}

		if err := text("cron command", c.Command, MaxCronCommand); err != nil {
			return err
		}

		if c.Note != "" {
			if err := text("cron note", c.Note, MaxCronNote); err != nil {
				return err
			}
		}
	}

	if len(p.Checklist) > MaxChecklist {
		return invalid("more than %d checklist entries", MaxChecklist)
	}

	for _, c := range p.Checklist {
		if err := text("checklist entry", c, MaxChecklistText); err != nil {
			return err
		}
	}

	return nil
}

// Normalize returns an own profile in the form it is stored and compared
// in: not built-in, empty lists as nil.
func Normalize(p Profile) Profile {
	p = p.clone()
	p.Builtin = false

	for _, l := range []*[]string{&p.Hardening.NoPHPDirs, &p.Hardening.DenyDirs, &p.Hardening.DenyFiles, &p.Hardening.LimitFiles, &p.Checklist} {
		if len(*l) == 0 {
			*l = nil
		}
	}

	if len(p.Cron) == 0 {
		p.Cron = nil
	}

	return p
}

// yamlDoc is the YAML form of a profile. It has its own field names, so
// the form stays the same when the Go type changes.
type yamlDoc struct {
	Version   int           `yaml:"version"`
	Name      string        `yaml:"name"`
	Title     string        `yaml:"title"`
	Type      string        `yaml:"type"`
	MinPHP    string        `yaml:"minPhp,omitempty"`
	WebDir    string        `yaml:"webDir,omitempty"`
	Hardening yamlHardening `yaml:"hardening"`
	PageCache string        `yaml:"pageCache,omitempty"`
	Cron      []yamlCron    `yaml:"cron,omitempty"`
	Checklist []string      `yaml:"checklist,omitempty"`
}

type yamlHardening struct {
	ConfigFiles bool     `yaml:"configFiles"`
	NoPHPDirs   []string `yaml:"noPhpDirs,omitempty"`
	DenyDirs    []string `yaml:"denyDirs,omitempty"`
	DenyFiles   []string `yaml:"denyFiles,omitempty"`
	LimitFiles  []string `yaml:"limitFiles,omitempty"`
}

type yamlCron struct {
	Schedule string `yaml:"schedule"`
	Command  string `yaml:"command"`
	Note     string `yaml:"note,omitempty"`
}

// ExportYAML writes a profile, built-in or own, in the YAML form with
// the current version.
func ExportYAML(p Profile) ([]byte, error) {
	d := yamlDoc{
		Version: YAMLVersion, Name: p.Name, Title: p.Title, Type: p.Type, MinPHP: p.MinPHP, WebDir: p.WebDir,
		Hardening: yamlHardening{
			ConfigFiles: p.Hardening.ConfigFiles, NoPHPDirs: p.Hardening.NoPHPDirs, DenyDirs: p.Hardening.DenyDirs,
			DenyFiles: p.Hardening.DenyFiles, LimitFiles: p.Hardening.LimitFiles,
		},
		PageCache: p.PageCache, Checklist: p.Checklist,
	}

	for _, c := range p.Cron {
		d.Cron = append(d.Cron, yamlCron(c))
	}

	var buf bytes.Buffer

	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	if err := enc.Encode(d); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}

	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}

	return buf.Bytes(), nil
}

// ImportYAML reads one profile in the YAML form and checks it like any own
// profile. It refuses another version, unknown fields, more than one
// document and anything Check refuses.
func ImportYAML(in []byte) (Profile, error) {
	if len(in) > MaxYAMLBytes {
		return Profile{}, invalid("the YAML is larger than %d bytes", MaxYAMLBytes)
	}

	// The version first, so a file of a newer version gets that answer
	// and not one about its new fields.
	var head struct {
		Version any `yaml:"version"`
	}

	if err := yaml.Unmarshal(in, &head); err != nil {
		return Profile{}, invalid("the YAML cannot be read: %s", yamlMsg(err))
	}

	if v, ok := head.Version.(int); !ok || v != YAMLVersion {
		return Profile{}, invalid("unknown version %v: this panel reads profiles of version %d", head.Version, YAMLVersion)
	}

	dec := yaml.NewDecoder(bytes.NewReader(in))
	dec.KnownFields(true)

	var d yamlDoc
	if err := dec.Decode(&d); err != nil {
		return Profile{}, invalid("the YAML cannot be read: %s", yamlMsg(err))
	}

	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return Profile{}, invalid("the YAML holds more than one document")
	}

	p := Profile{
		Name: d.Name, Title: d.Title, Type: d.Type, MinPHP: d.MinPHP, WebDir: d.WebDir, PageCache: d.PageCache, Checklist: d.Checklist,
		Hardening: agentapi.WebHardening{
			ConfigFiles: d.Hardening.ConfigFiles, NoPHPDirs: d.Hardening.NoPHPDirs, DenyDirs: d.Hardening.DenyDirs,
			DenyFiles: d.Hardening.DenyFiles, LimitFiles: d.Hardening.LimitFiles,
		},
	}

	for _, c := range d.Cron {
		p.Cron = append(p.Cron, Cron(c))
	}

	p = Normalize(p)
	if err := Check(p); err != nil {
		return Profile{}, err
	}

	return p, nil
}

// yamlMsg shortens a decoder error to its first line, without the
// library's prefix.
func yamlMsg(err error) string {
	msg, _, _ := strings.Cut(err.Error(), "\n")

	return strings.TrimPrefix(strings.TrimPrefix(msg, "yaml: "), "unmarshal errors: ")
}
