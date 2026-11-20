// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
)

// State is what Hostashi knows about PHP versions: the values in
// internal/agentapi/php_versions.go plus the manual legacy list.
type State struct {
	Versions []string
	Legacy   []string
	Security map[string]string
	Active   map[string]string
}

// Upstream is what php.net and sury.org report.
type Upstream struct {
	// Released are the branches php.net lists as supported with a release.
	Released []string
	// Dates are the support dates from php.net per branch.
	Dates map[string]Dates
	// Missing returns the sury.org packages a version lacks on any suite
	// and architecture, as "suite/arch: package".
	Missing func(version string) []string
}

// Report describes a Plan, for the log and the pull request.
type Report struct {
	Added []string
	// Waiting are released versions sury.org does not ship completely yet,
	// with what is missing.
	Waiting map[string][]string
	// Dates are changed support dates, as "8.4 security: old -> new".
	Dates []string
}

// Changed is true when the plan changes any file.
func (r Report) Changed() bool { return len(r.Added) > 0 || len(r.Dates) > 0 }

// Plan computes the next state. A version is added when php.net lists it as
// released with support dates, it is newer than every version Hostashi
// knows, and sury.org ships the full package set for it on every supported
// suite and architecture. Support dates of known versions follow php.net.
// Versions are never removed and the legacy list is never changed: both
// are decisions (ADR-0019, ADR-0036).
func Plan(cur State, up Upstream) (State, Report) {
	next := State{
		Versions: slices.Clone(cur.Versions),
		Legacy:   slices.Clone(cur.Legacy),
		Security: maps.Clone(cur.Security),
		Active:   maps.Clone(cur.Active),
	}
	rep := Report{Waiting: map[string][]string{}}

	released := slices.Clone(up.Released)
	SortVersions(released)

	for _, v := range released {
		d, ok := up.Dates[v]
		if !ok || slices.Contains(next.Versions, v) || CompareVersions(v, next.Versions[len(next.Versions)-1]) < 0 {
			continue
		}

		if missing := up.Missing(v); len(missing) > 0 {
			rep.Waiting[v] = missing

			continue
		}

		next.Versions = append(next.Versions, v)
		next.Security[v] = d.Security
		next.Active[v] = d.Active
		rep.Added = append(rep.Added, v)
	}

	for _, v := range cur.Versions {
		d, ok := up.Dates[v]
		if !ok {
			continue
		}

		if old := next.Security[v]; old != d.Security {
			rep.Dates = append(rep.Dates, fmt.Sprintf("%s security support: %s -> %s", v, orNone(old), d.Security))
			next.Security[v] = d.Security
		}

		if old := next.Active[v]; old != d.Active {
			rep.Dates = append(rep.Dates, fmt.Sprintf("%s active support: %s -> %s", v, orNone(old), d.Active))
			next.Active[v] = d.Active
		}
	}

	return next, rep
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}

	return s
}

// CompareVersions compares two minor versions numerically ("8.10" > "8.9").
func CompareVersions(a, b string) int {
	am, an := split(a)
	bm, bn := split(b)

	if am != bm {
		return am - bm
	}

	return an - bn
}

// SortVersions sorts minor versions numerically.
func SortVersions(vs []string) { slices.SortFunc(vs, CompareVersions) }

func split(v string) (int, int) {
	maj, minor, _ := strings.Cut(v, ".")
	a, _ := strconv.Atoi(maj)
	b, _ := strconv.Atoi(minor)

	return a, b
}

var goTmpl = template.Must(template.New("go").Parse(`// SPDX-License-Identifier: AGPL-3.0-or-later

// This file is rewritten by tools/phpversions, which the weekly "PHP
// versions" workflow runs (ADR-0036). Hand edits are fine as long as the
// layout stays: the tool renders the whole file from these values.

package agentapi

// PHPVersions are the PHP versions Hostashi can run a site on (ADR-0019):
// {{.Standard}} as standard, {{.LegacyText}} as legacy. Whether a version is
// installed is a separate question (PHPVersionsList).
var PHPVersions = []string{ {{- .VersionList -}} }

// PHPSecurityUntil is the last day of security fixes per version, from
// php.net/supported-versions.php and php.net/eol.php (ADR-0036). After it
// a version is end of life.
var PHPSecurityUntil = map[string]string{
{{range .Security}}	"{{.K}}": "{{.V}}",
{{end -}}
}

// PHPActiveUntil is the last day of active support (bug fixes) per
// version, same sources. Between it and PHPSecurityUntil a version gets
// security fixes only. Versions that are end of life have no entry.
var PHPActiveUntil = map[string]string{
{{range .Active}}	"{{.K}}": "{{.V}}",
{{end -}}
}
`))

type kv struct{ K, V string }

func sortedKV(m map[string]string) []kv {
	keys := slices.Collect(maps.Keys(m))
	SortVersions(keys)

	out := make([]kv, 0, len(keys))
	for _, k := range keys {
		out = append(out, kv{k, m[k]})
	}

	return out
}

// RenderGo renders internal/agentapi/php_versions.go.
func RenderGo(s State) ([]byte, error) {
	var standard []string

	for _, v := range s.Versions {
		if !slices.Contains(s.Legacy, v) {
			standard = append(standard, v)
		}
	}

	if len(standard) == 0 {
		return nil, errors.New("no standard version")
	}

	std := standard[0]
	if len(standard) > 1 {
		std += " to " + standard[len(standard)-1]
	}

	legacy := slices.Clone(s.Legacy)
	SortVersions(legacy)
	slices.Reverse(legacy)

	quoted := make([]string, len(s.Versions))
	for i, v := range s.Versions {
		quoted[i] = strconv.Quote(v)
	}

	var buf bytes.Buffer

	err := goTmpl.Execute(&buf, map[string]any{
		"Standard":    std,
		"LegacyText":  joinAnd(legacy),
		"VersionList": strings.Join(quoted, ", "),
		"Security":    sortedKV(s.Security),
		"Active":      sortedKV(s.Active),
	})
	if err != nil {
		return nil, fmt.Errorf("render: %w", err)
	}

	out, err := format.Source(buf.Bytes())
	if err != nil {
		return nil, fmt.Errorf("gofmt: %w", err)
	}

	return out, nil
}

// joinAnd writes "8.2, 8.1 and 7.4".
func joinAnd(vs []string) string {
	if len(vs) < 2 {
		return strings.Join(vs, "")
	}

	return strings.Join(vs[:len(vs)-1], ", ") + " and " + vs[len(vs)-1]
}

var (
	shellListRe = regexp.MustCompile(`(?m)^PHP_VERSIONS=\([^)]*\)$`)
	helpRe      = regexp.MustCompile(`(?m)^(#\s+from )[0-9][0-9., ]*[0-9]( \()`)
	featuresRe  = regexp.MustCompile(`(several versions in parallel, )[0-9.]+–[0-9.]+`)
	pkgsRe      = regexp.MustCompile(`(?m)^php_pkgs\(\) \{ local e; for e in ([a-z0-9 ]+); do`)
)

// replaceOnce replaces the single match of re, or fails: a file whose
// layout changed must not be rewritten blindly.
func replaceOnce(name string, b []byte, re *regexp.Regexp, repl string) ([]byte, error) {
	if n := len(re.FindAllIndex(b, -1)); n != 1 {
		return nil, fmt.Errorf("%s: %d matches for %s, want 1", name, n, re)
	}

	return re.ReplaceAll(b, []byte(repl)), nil
}

// RewriteInstaller updates packaging/install.sh: the version list and the
// usage text of --php-versions.
func RewriteInstaller(b []byte, versions []string) ([]byte, error) {
	b, err := replaceOnce("install.sh", b, shellListRe, "PHP_VERSIONS=("+strings.Join(versions, " ")+")")
	if err != nil {
		return nil, err
	}

	return replaceOnce("install.sh", b, helpRe, "${1}"+strings.Join(versions, ", ")+"${2}")
}

// RewritePHPInstall updates the version list of packaging/php/php-install.
func RewritePHPInstall(b []byte, versions []string) ([]byte, error) {
	return replaceOnce("php-install", b, shellListRe, "PHP_VERSIONS=("+strings.Join(versions, " ")+")")
}

// RewriteFeatures updates the version range in docs/03-features.md.
func RewriteFeatures(b []byte, versions []string) ([]byte, error) {
	return replaceOnce("03-features.md", b, featuresRe, "${1}"+versions[0]+"–"+versions[len(versions)-1])
}

// PackageSuffixes reads the fixed package set (fpm, cli, ...) from
// php-install, so the check uses exactly what an install needs.
func PackageSuffixes(phpInstall []byte) ([]string, error) {
	m := pkgsRe.FindAllSubmatch(phpInstall, -1)
	if len(m) != 1 {
		return nil, errors.New("php-install: php_pkgs not found")
	}

	return strings.Fields(string(m[0][1])), nil
}
