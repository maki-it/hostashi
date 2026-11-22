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

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Candidate is the newest LTS release of a line that nodejs.org offers.
type Candidate struct {
	Major    string
	Version  string // without "v"
	Codename string
	Security bool // a security release
	End      string
}

// Candidates picks, per major, the newest LTS release from the index and
// its end of life from the schedule. Lines whose end has passed on today
// are left out.
func Candidates(index []IndexEntry, schedule map[string]Line, today string) map[string]Candidate {
	out := map[string]Candidate{}

	for _, e := range index {
		code := e.Codename()
		v := strings.TrimPrefix(e.Version, "v")

		if code == "" || !releaseRe.MatchString(v) {
			continue
		}

		major, _, _ := strings.Cut(v, ".")
		line, ok := schedule["v"+major]

		if !ok || line.End == "" || line.End < today {
			continue
		}

		if cur, ok := out[major]; ok && CompareVersions(cur.Version, v) >= 0 {
			continue
		}

		out[major] = Candidate{Major: major, Version: v, Codename: code, Security: e.Security, End: line.End}
	}

	return out
}

var releaseRe = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// CompareVersions compares two releases like "24.22.3".
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
	Major    string
	From     string // "" for a new line
	To       string
	Security bool
	End      string // the new end of life when it changed, else ""
}

// Report says what the plan changes.
type Report struct {
	Changes []Change
}

// Changed reports whether anything changes.
func (r Report) Changed() bool { return len(r.Changes) > 0 }

// Security reports whether a change is a security release.
func (r Report) Security() bool {
	return slices.ContainsFunc(r.Changes, func(c Change) bool { return c.Security })
}

// Plan updates the pinned lines to the newest LTS releases, adds new LTS
// lines and follows changed end dates. Lines that nodejs.org no longer
// supports stay: removing one is a decision by hand. sums fetches the
// verified checksums of a release.
func Plan(cur []agentapi.NodeRelease, cand map[string]Candidate, sums func(version string) (map[string]string, error)) ([]agentapi.NodeRelease, Report, error) {
	out := slices.Clone(cur)

	var rep Report

	for i, r := range out {
		c, ok := cand[r.Major]
		if !ok {
			continue
		}

		ch := Change{Major: r.Major, From: r.Version, To: r.Version}

		if CompareVersions(c.Version, r.Version) > 0 {
			s, err := sums(c.Version)
			if err != nil {
				return nil, Report{}, err
			}

			out[i].Version, out[i].Codename, out[i].SHA256 = c.Version, c.Codename, s
			ch.To, ch.Security = c.Version, c.Security
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

	for major := range cand {
		if !slices.ContainsFunc(out, func(r agentapi.NodeRelease) bool { return r.Major == major }) {
			added = append(added, major)
		}
	}

	slices.SortFunc(added, CompareVersions)

	for _, major := range added {
		c := cand[major]

		s, err := sums(c.Version)
		if err != nil {
			return nil, Report{}, err
		}

		out = append(out, agentapi.NodeRelease{Major: major, Version: c.Version, Codename: c.Codename, End: c.End, SHA256: s})
		rep.Changes = append(rep.Changes, Change{Major: major, To: c.Version, Security: c.Security, End: c.End})
	}

	slices.SortFunc(out, func(a, b agentapi.NodeRelease) int { return CompareVersions(a.Major, b.Major) })

	return out, rep, nil
}

var listTmpl = template.Must(template.New("list").Parse(`// NodeReleases are the LTS lines Hostashi installs, oldest first. Checked
// on {{.Date}} against https://nodejs.org/dist/index.json and the signed
// SHASUMS256.txt of each release.
var NodeReleases = []NodeRelease{
{{- range .Releases}}
	{
		Major: "{{.Major}}", Version: "{{.Version}}", Codename: "{{.Codename}}", End: "{{.End}}",
		SHA256: map[string]string{
			"x64":   "{{index .SHA256 "x64"}}",
			"arm64": "{{index .SHA256 "arm64"}}",
		},
	},
{{- end}}
}
`))

// listRe matches the list in node_versions.go, from its comment to the
// closing brace.
var listRe = regexp.MustCompile(`(?s)// NodeReleases are the LTS lines.*?\nvar NodeReleases = \[\]NodeRelease\{\n.*?\n\}\n`)

// Rewrite replaces the list in node_versions.go.
func Rewrite(src []byte, releases []agentapi.NodeRelease, date string) ([]byte, error) {
	var b bytes.Buffer
	if err := listTmpl.Execute(&b, map[string]any{"Date": date, "Releases": releases}); err != nil {
		return nil, err
	}

	if n := len(listRe.FindAllIndex(src, -1)); n != 1 {
		return nil, fmt.Errorf("node_versions.go: found the list %d times", n)
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
			fmt.Fprintf(&b, "- New LTS line: Node.js %s (%s), end of life %s\n", c.Major, c.To, c.End)
		case c.From != c.To:
			sec := ""
			if c.Security {
				sec = " **(security release)**"
			}

			fmt.Fprintf(&b, "- Node.js %s: %s → %s%s\n", c.Major, c.From, c.To, sec)
		}

		if c.From != "" && c.End != "" {
			fmt.Fprintf(&b, "- Node.js %s: end of life now %s\n", c.Major, c.End)
		}
	}

	return b.String()
}
