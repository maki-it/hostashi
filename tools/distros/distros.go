// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Release is one distribution release.
type Release struct {
	ID       string // "debian" or "ubuntu", as in os-release
	Version  string // "13", "26.04"
	Codename string // "trixie", "resolute"
	Name     string // "Debian 13", "Ubuntu 26.04 LTS"
}

// The supported releases are the case pattern in install.sh's preflight
// (ADR-0020), e.g. "debian-13 | ubuntu-26.04) ;;   # ADR-0020".
var supportedRe = regexp.MustCompile(`(?m)^\s*([a-z]+-[0-9.]+(?:\s*\|\s*[a-z]+-[0-9.]+)*)\)\s*;;\s*# ADR-0020`)

// Supported reads the supported releases (id -> versions) from install.sh.
func Supported(installer []byte) (map[string][]string, error) {
	m := supportedRe.FindAllSubmatch(installer, -1)
	if len(m) != 1 {
		return nil, fmt.Errorf("install.sh: %d supported-OS patterns, want 1", len(m))
	}

	out := map[string][]string{}

	for _, alt := range strings.Split(string(m[0][1]), "|") {
		id, ver, _ := strings.Cut(strings.TrimSpace(alt), "-")
		out[id] = append(out[id], ver)
	}

	return out, nil
}

// fields parses a Debian control-style stanza list (Release files,
// meta-release): one map per paragraph.
func fields(b []byte) []map[string]string {
	var (
		out []map[string]string
		cur map[string]string
	)

	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			cur = nil

			continue
		}

		k, v, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, " ") {
			continue
		}

		if cur == nil {
			cur = map[string]string{}
			out = append(out, cur)
		}

		cur[k] = strings.TrimSpace(v)
	}

	return out
}

// ParseDebianStable reads the release from the Release file of Debian's
// "stable" suite.
func ParseDebianStable(b []byte) (Release, error) {
	f := fields(b)
	if len(f) == 0 || f[0]["Suite"] != "stable" || f[0]["Codename"] == "" {
		return Release{}, errors.New("debian: not the Release file of the stable suite")
	}

	major, _, _ := strings.Cut(f[0]["Version"], ".")
	if _, err := strconv.Atoi(major); err != nil {
		return Release{}, fmt.Errorf("debian: version %q", f[0]["Version"])
	}

	return Release{ID: "debian", Version: major, Codename: f[0]["Codename"], Name: "Debian " + major}, nil
}

var ltsRe = regexp.MustCompile(`^([0-9]{2}\.[0-9]{2})(?:\.[0-9]+)? LTS$`)

// ParseUbuntuLTS returns the newest released, supported LTS from
// changelogs.ubuntu.com/meta-release-lts.
func ParseUbuntuLTS(b []byte, now time.Time) (Release, error) {
	var best Release

	for _, f := range fields(b) {
		m := ltsRe.FindStringSubmatch(f["Version"])
		if m == nil || f["Supported"] != "1" || f["Dist"] == "" {
			continue
		}

		if d, err := time.Parse("Mon, 2 January 2006 15:04:05 MST", f["Date"]); err == nil && d.After(now) {
			continue
		}

		if best.Version == "" || compare(m[1], best.Version) > 0 {
			best = Release{ID: "ubuntu", Version: m[1], Codename: f["Dist"], Name: "Ubuntu " + m[1] + " LTS"}
		}
	}

	if best.Version == "" {
		return Release{}, errors.New("ubuntu: no supported LTS release in meta-release-lts")
	}

	return best, nil
}

// compare compares dotted numeric versions ("26.04" < "28.04", "9" < "13").
func compare(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(as), len(bs)) {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}

		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}

		if x != y {
			return x - y
		}
	}

	return 0
}

// IsNew is true when r is newer than every supported version of its
// distribution.
func IsNew(r Release, supported map[string][]string) bool {
	vs := supported[r.ID]
	if len(vs) == 0 {
		return false
	}

	return compare(r.Version, slices.MaxFunc(vs, compare)) > 0
}

// Readiness is what the issue reports about a new release.
type Readiness struct {
	Sury     bool
	Packages map[string]bool // nginx packages Hostashi installs -> present
}

// Title is the issue title; the workflow uses it to find an earlier issue.
func Title(r Release) string {
	return fmt.Sprintf("Support %s (%s)", r.Name, r.Codename)
}

func check(ok bool) string {
	if ok {
		return "yes"
	}

	return "not yet"
}

// IssueBody describes the new release and what is ready for it.
func IssueBody(r Release, rd Readiness, supported map[string][]string) string {
	var b strings.Builder

	cur := slices.Clone(supported[r.ID])
	slices.SortFunc(cur, compare)

	distro, _, _ := strings.Cut(r.Name, " ")
	fmt.Fprintf(&b, "%s (%s) is released. Hostashi supports %s %s today (ADR-0020).\n\n", r.Name, r.Codename, distro, strings.Join(cur, ", "))
	b.WriteString("Found by the monthly Distributions workflow. Adding a release is a decision (ADR-0020), so nothing was changed.\n\n")
	b.WriteString("## Upstream today\n\n")
	fmt.Fprintf(&b, "- sury.org PHP repository for `%s`: %s\n", r.Codename, check(rd.Sury))

	pkgs := make([]string, 0, len(rd.Packages))
	for p := range rd.Packages {
		pkgs = append(pkgs, p)
	}

	slices.Sort(pkgs)

	for _, p := range pkgs {
		fmt.Fprintf(&b, "- `%s` in the distribution (amd64): %s\n", p, check(rd.Packages[p]))
	}

	b.WriteString("\n## To do before supporting it\n\n")
	b.WriteString("- [ ] ADR-0020 updated (which releases, what happens to the oldest one)\n")
	b.WriteString("- [ ] `install.sh` preflight, PHP default version and FPM units for the new release\n")
	b.WriteString("- [ ] sury.org suite in `tools/phpversions` (`SuryDists`) and the PHP pin (spike S10)\n")
	b.WriteString("- [ ] Installer test in CI (`go.yml` matrix) green on the new image\n")
	b.WriteString("- [ ] Quota, dracut/initramfs and systemd exposure checked\n")

	return b.String()
}
