// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Candidate is the newest build of a line that python-build-standalone
// offers, for a line that is still supported.
type Candidate struct {
	Line    string
	Version string
	Tag     string
	End     string // YYYY-MM-DD
	SHA256  map[string]string
}

// EndDate turns the release cycle's end of life into a day: a planned
// "YYYY-MM" becomes the month's last day.
func EndDate(s string) (string, error) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t.Format(time.DateOnly), nil
	}

	t, err := time.Parse("2006-01", s)
	if err != nil {
		return "", fmt.Errorf("end of life %q", s)
	}

	return t.AddDate(0, 1, -1).Format(time.DateOnly), nil
}

// Candidates picks the lines in bugfix or security status whose end has
// not passed on today and that have a build in the release.
func Candidates(cycle map[string]Cycle, builds map[string]Build, tag, today string) (map[string]Candidate, error) {
	out := map[string]Candidate{}

	for line, c := range cycle {
		if c.Status != "bugfix" && c.Status != "security" {
			continue
		}

		end, err := EndDate(c.EndOfLife)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", line, err)
		}

		if end < today {
			continue
		}

		b, ok := builds[line]
		if !ok {
			continue
		}

		out[line] = Candidate{Line: line, Version: b.Version, Tag: tag, End: end, SHA256: b.SHA256}
	}

	return out, nil
}

// CompareVersions compares two versions like "3.13.18" or lines like
// "3.13".
func CompareVersions(a, b string) int {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")

	for i := range max(len(pa), len(pb)) {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}

		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}

		if x != y {
			return x - y
		}
	}

	return 0
}

// Change is one line the plan updates or adds.
type Change struct {
	Line string
	From string // "" for a new line; "3.13.18+20261110"
	To   string
	End  string // the new end of life when it changed, else ""
}

// Report says what the plan changes.
type Report struct {
	Changes []Change
	// Verify are the archives whose build provenance the workflow checks
	// before the pull request is opened.
	Verify []Archive
}

// Archive is one download of a changed build.
type Archive struct {
	URL    string
	SHA256 string
}

// Changed reports whether anything changes.
func (r Report) Changed() bool { return len(r.Changes) > 0 }

// PatchOnly reports whether there are changes and every one is a newer
// release or build of a line that is already pinned, with the same end of
// life. Only such a pull request merges by itself once all checks are
// green (ADR-0077); new lines and changed end dates wait for a maintainer.
func (r Report) PatchOnly() bool {
	return r.Changed() && !slices.ContainsFunc(r.Changes, func(c Change) bool {
		return c.From == "" || c.End != "" || c.To == c.From
	})
}

func build(version, tag string) string { return version + "+" + tag }

// archives lists the downloads of a release, x86_64 first.
func archives(r agentapi.PythonRelease) []Archive {
	var out []Archive

	for _, arch := range []string{"x86_64", "aarch64"} {
		out = append(out, Archive{
			URL:    DistURL + "/" + r.Tag + "/cpython-" + r.Version + "%2B" + r.Tag + "-" + arch + "-unknown-linux-gnu-install_only_stripped.tar.gz",
			SHA256: r.SHA256[arch],
		})
	}

	return out
}

// Plan moves the pinned lines to the newest build: a newer CPython
// version, or a newer build of the same version (python-build-standalone
// rebuilds for new OpenSSL and SQLite releases). It adds new lines and
// follows changed end dates. Lines that are no longer supported stay:
// removing one is a decision by hand.
func Plan(cur []agentapi.PythonRelease, cand map[string]Candidate) ([]agentapi.PythonRelease, Report) {
	out := slices.Clone(cur)

	var rep Report

	for i, r := range out {
		c, ok := cand[r.Line]
		if !ok {
			continue
		}

		ch := Change{Line: r.Line, From: build(r.Version, r.Tag), To: build(r.Version, r.Tag)}

		if v := CompareVersions(c.Version, r.Version); v > 0 || (v == 0 && c.Tag > r.Tag) {
			out[i].Version, out[i].Tag, out[i].SHA256 = c.Version, c.Tag, c.SHA256
			ch.To = build(c.Version, c.Tag)

			rep.Verify = append(rep.Verify, archives(out[i])...)
		}

		if c.End != r.End {
			out[i].End = c.End
			ch.End = c.End
		}

		if ch.To != ch.From || ch.End != "" {
			rep.Changes = append(rep.Changes, ch)
		}
	}

	var added []string

	for line := range cand {
		if !slices.ContainsFunc(out, func(r agentapi.PythonRelease) bool { return r.Line == line }) {
			added = append(added, line)
		}
	}

	slices.SortFunc(added, CompareVersions)

	for _, line := range added {
		c := cand[line]
		r := agentapi.PythonRelease{Line: line, Version: c.Version, Tag: c.Tag, End: c.End, SHA256: c.SHA256}
		out = append(out, r)
		rep.Changes = append(rep.Changes, Change{Line: line, To: build(c.Version, c.Tag), End: c.End})
		rep.Verify = append(rep.Verify, archives(r)...)
	}

	slices.SortFunc(out, func(a, b agentapi.PythonRelease) int { return CompareVersions(a.Line, b.Line) })

	return out, rep
}

var listTmpl = template.Must(template.New("list").Parse(`// PythonReleases are the lines Hostashi installs, oldest first: every line
// that has not ended. Checked on {{.Date}} against
// https://peps.python.org/api/release-cycle.json and the SHA256SUMS of
// python-build-standalone release {{.Tag}}; the build provenance of each
// new archive was verified with Sigstore.
var PythonReleases = []PythonRelease{
{{- range .Releases}}
	{
		Line: "{{.Line}}", Version: "{{.Version}}", Tag: "{{.Tag}}", End: "{{.End}}",
		SHA256: map[string]string{
			"x86_64":  "{{index .SHA256 "x86_64"}}",
			"aarch64": "{{index .SHA256 "aarch64"}}",
		},
	},
{{- end}}
}
`))

// listRe matches the list in python_versions.go, from its comment to the
// closing brace.
var listRe = regexp.MustCompile(`(?s)// PythonReleases are the lines.*?\nvar PythonReleases = \[\]PythonRelease\{\n.*?\n\}\n`)

// Rewrite replaces the list in python_versions.go.
func Rewrite(src []byte, releases []agentapi.PythonRelease, date, tag string) ([]byte, error) {
	var b bytes.Buffer
	if err := listTmpl.Execute(&b, map[string]any{"Date": date, "Tag": tag, "Releases": releases}); err != nil {
		return nil, err
	}

	if n := len(listRe.FindAllIndex(src, -1)); n != 1 {
		return nil, fmt.Errorf("python_versions.go: found the list %d times", n)
	}

	return listRe.ReplaceAllLiteral(src, b.Bytes()), nil
}

// Markdown describes the report for the pull request.
func Markdown(r Report) string {
	if !r.Changed() {
		return "Nothing new.\n"
	}

	var b strings.Builder

	for _, c := range r.Changes {
		switch {
		case c.From == "":
			fmt.Fprintf(&b, "- New line: Python %s (%s), end of life %s\n", c.Line, c.To, c.End)
		case c.From != c.To:
			fmt.Fprintf(&b, "- Python %s: %s → %s\n", c.Line, c.From, c.To)
		}

		if c.From != "" && c.End != "" {
			fmt.Fprintf(&b, "- Python %s: end of life now %s\n", c.Line, c.End)
		}
	}

	return b.String()
}

// VerifyList is the file the workflow reads: one "SHA256 URL" per archive.
func VerifyList(r Report) string {
	var b strings.Builder

	for _, a := range r.Verify {
		fmt.Fprintf(&b, "%s %s\n", a.SHA256, a.URL)
	}

	return b.String()
}
