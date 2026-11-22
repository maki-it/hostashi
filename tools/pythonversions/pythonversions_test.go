// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func testdata(t *testing.T, name string) []byte {
	t.Helper()

	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestParseSums(t *testing.T) {
	got, err := ParseSums(testdata(t, "SHA256SUMS-20261110"), "20261110")
	if err != nil {
		t.Fatal(err)
	}

	// The pinned list was taken from this file.
	for _, r := range agentapi.PythonReleases {
		if r.Tag != "20261110" {
			continue
		}

		b, ok := got[r.Line]
		if !ok || b.Version != r.Version || b.SHA256["x86_64"] != r.SHA256["x86_64"] || b.SHA256["aarch64"] != r.SHA256["aarch64"] {
			t.Errorf("%s: %+v, pinned %+v", r.Line, b, r)
		}
	}

	// musl, x86_64_v3, freethreaded, macOS and install_only without
	// stripping are not taken.
	for _, b := range got {
		if len(b.SHA256) != 2 {
			t.Errorf("%+v", b)
		}
	}

	if _, err := ParseSums(testdata(t, "SHA256SUMS-20261110"), "20261111"); err == nil {
		t.Error("sums of another tag accepted")
	}

	if _, err := ParseSums([]byte("zz  cpython-3.13.1+20261110-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz\n"), "20261110"); err == nil {
		t.Error("bad sum accepted")
	}

	// One architecture only: the line is left out.
	one := strings.Repeat("a", 64) + "  cpython-3.13.1+20261110-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz\n"
	if _, err := ParseSums([]byte(one), "20261110"); err == nil {
		t.Error("a line with one architecture accepted")
	}
}

func TestParseLatest(t *testing.T) {
	tag, err := ParseLatest([]byte(`{"version":1,"tag":"20261110","release_url":"x"}`))
	if err != nil || tag != "20261110" {
		t.Fatalf("%q %v", tag, err)
	}

	for _, bad := range []string{`{"version":2,"tag":"20261110"}`, `{"version":1,"tag":"latest"}`, `{"version":1,"tag":"../x"}`, `[]`} {
		if _, err := ParseLatest([]byte(bad)); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestEndDate(t *testing.T) {
	for in, want := range map[string]string{"2027-10": "2027-10-31", "2028-02": "2028-02-29", "2026-10-01": "2026-10-01"} {
		if got, err := EndDate(in); err != nil || got != want {
			t.Errorf("%s: %s %v", in, got, err)
		}
	}

	if _, err := EndDate("soon"); err == nil {
		t.Error("soon accepted")
	}
}

func TestCandidates(t *testing.T) {
	cycle, err := ParseCycle(testdata(t, "release-cycle.json"))
	if err != nil {
		t.Fatal(err)
	}

	builds, err := ParseSums(testdata(t, "SHA256SUMS-20261110"), "20261110")
	if err != nil {
		t.Fatal(err)
	}

	cand, err := Candidates(cycle, builds, "20261110", "2026-11-22")
	if err != nil {
		t.Fatal(err)
	}

	var lines []string
	for l := range cand {
		lines = append(lines, l)
	}

	slices.SortFunc(lines, CompareVersions)

	// 3.10 has ended, 3.16 is in feature status.
	if want := []string{"3.11", "3.12", "3.13", "3.14", "3.15"}; !slices.Equal(lines, want) {
		t.Fatalf("lines %v", lines)
	}

	// While the pins come from this release, the testdata changes nothing.
	fromTestdata := !slices.ContainsFunc(agentapi.PythonReleases, func(r agentapi.PythonRelease) bool { return r.Tag != "20261110" })
	if got, rep := Plan(agentapi.PythonReleases, cand); fromTestdata && (rep.Changed() || !slices.EqualFunc(got, agentapi.PythonReleases, sameRelease)) {
		t.Fatalf("changes against the testdata: %+v", rep)
	}

	// After 2027-10-31, 3.11 is no candidate; the pinned line stays.
	cand, _ = Candidates(cycle, builds, "20261110", "2027-11-01")
	if _, ok := cand["3.11"]; ok {
		t.Error("3.11 after its end")
	}

	if got, _ := Plan(agentapi.PythonReleases, cand); got[0].Line != "3.11" {
		t.Error("an ended line was removed")
	}
}

func sameRelease(a, b agentapi.PythonRelease) bool {
	return a.Line == b.Line && a.Version == b.Version && a.Tag == b.Tag && a.End == b.End &&
		a.SHA256["x86_64"] == b.SHA256["x86_64"] && a.SHA256["aarch64"] == b.SHA256["aarch64"]
}

func sums(c string) map[string]string {
	return map[string]string{"x86_64": strings.Repeat(c, 64), "aarch64": strings.Repeat(c, 64)}
}

func TestPlan(t *testing.T) {
	cur := []agentapi.PythonRelease{
		{Line: "3.12", Version: "3.12.16", Tag: "20261110", End: "2028-10-31", SHA256: sums("1")},
		{Line: "3.13", Version: "3.13.18", Tag: "20261110", End: "2029-10-31", SHA256: sums("2")},
		{Line: "3.14", Version: "3.14.10", Tag: "20261110", End: "2030-10-31", SHA256: sums("3")},
	}
	cand := map[string]Candidate{
		// A new patch version.
		"3.12": {Line: "3.12", Version: "3.12.17", Tag: "20261201", End: "2028-10-31", SHA256: sums("4")},
		// The same version, rebuilt.
		"3.13": {Line: "3.13", Version: "3.13.18", Tag: "20261201", End: "2029-10-31", SHA256: sums("5")},
		// Only the end date moved.
		"3.14": {Line: "3.14", Version: "3.14.10", Tag: "20261110", End: "2030-11-30", SHA256: sums("3")},
		// A new line.
		"3.16": {Line: "3.16", Version: "3.16.0", Tag: "20261201", End: "2032-10-31", SHA256: sums("6")},
	}

	got, rep := Plan(cur, cand)

	if len(got) != 4 || got[0].Version != "3.12.17" || got[1].Tag != "20261201" || got[1].SHA256["x86_64"] != strings.Repeat("5", 64) ||
		got[2].End != "2030-11-30" || got[3].Line != "3.16" {
		t.Fatalf("plan: %+v", got)
	}

	if len(rep.Changes) != 4 || rep.Changes[0].To != "3.12.17+20261201" || rep.Changes[1].From != "3.13.18+20261110" || rep.Changes[3].From != "" {
		t.Errorf("report: %+v", rep.Changes)
	}

	// Two archives each for 3.12, 3.13 and 3.16, none for the end date.
	if len(rep.Verify) != 6 || !strings.HasSuffix(rep.Verify[0].URL, "/20261201/cpython-3.12.17%2B20261201-x86_64-unknown-linux-gnu-install_only_stripped.tar.gz") ||
		rep.Verify[1].SHA256 != strings.Repeat("4", 64) {
		t.Errorf("verify: %+v", rep.Verify)
	}

	md := Markdown(rep)
	for _, want := range []string{"Python 3.12: 3.12.16+20261110 → 3.12.17+20261201", "Python 3.13: 3.13.18+20261110 → 3.13.18+20261201", "end of life now 2030-11-30", "New line: Python 3.16"} {
		if !strings.Contains(md, want) {
			t.Errorf("summary misses %q:\n%s", want, md)
		}
	}

	if VerifyList(rep) == "" || Markdown(Report{}) != "Nothing new.\n" {
		t.Error("empty lists")
	}

	// An older build never replaces a newer pin.
	if _, rep := Plan(cur, map[string]Candidate{"3.13": {Line: "3.13", Version: "3.13.17", Tag: "20261201", End: "2029-10-31", SHA256: sums("7")}}); rep.Changed() {
		t.Errorf("downgrade: %+v", rep)
	}
}

func TestRewrite(t *testing.T) {
	src, err := os.ReadFile("../../" + goFile)
	if err != nil {
		t.Fatal(err)
	}

	// The template writes the list as it is.
	m := regexp.MustCompile(`Checked on ([0-9-]{10}) against[\s\S]*?release ([0-9]{8});`).FindSubmatch(src)
	if m == nil {
		t.Fatal("no date and tag in the list's comment")
	}

	out, err := Rewrite(src, agentapi.PythonReleases, string(m[1]), string(m[2]))
	if err != nil {
		t.Fatal(err)
	}

	if string(out) != string(src) {
		t.Fatalf("rewrite changed the file:\n%s", out)
	}

	more := append(slices.Clone(agentapi.PythonReleases), agentapi.PythonRelease{Line: "3.16", Version: "3.16.0", Tag: "20271010", End: "2032-10-31", SHA256: sums("a")})

	out, err = Rewrite(src, more, "2027-10-10", "20271010")
	if err != nil || !strings.Contains(string(out), `Line: "3.16", Version: "3.16.0", Tag: "20271010"`) || !strings.Contains(string(out), "release 20271010;") {
		t.Fatalf("rewrite: %v\n%s", err, out)
	}

	if _, err := Rewrite([]byte("package agentapi\n"), more, "2027-10-10", "20271010"); err == nil {
		t.Error("rewrite without the list")
	}
}
