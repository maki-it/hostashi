// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestVerifySums(t *testing.T) {
	signed, err := os.ReadFile("testdata/SHASUMS256-v24.22.3.txt.asc")
	if err != nil {
		t.Fatal(err)
	}

	got, err := VerifySums(signed, releaseKeys, "v24.22.3")
	if err != nil {
		t.Fatal(err)
	}

	pinned, _ := agentapi.NodeReleaseOf("24")
	if pinned.Version == "24.22.3" && (got["x64"] != pinned.SHA256["x64"] || got["arm64"] != pinned.SHA256["arm64"]) {
		t.Errorf("sums %v, pinned %v", got, pinned.SHA256)
	}

	// A changed sum breaks the signature.
	bad := bytes.Replace(signed, []byte(got["x64"]), []byte(strings.Repeat("0", 64)), 1)
	if _, err := VerifySums(bad, releaseKeys, "v24.22.3"); err == nil {
		t.Error("tampered sums accepted")
	}

	// Without the signer's key nothing is accepted.
	if _, err := VerifySums(signed, []byte(""), "v24.22.3"); err == nil {
		t.Error("accepted without keys")
	}

	// Another version's files are not in it.
	if _, err := VerifySums(signed, releaseKeys, "v24.22.4"); err == nil {
		t.Error("sums of another version")
	}
}

func entry(v string, lts string, sec bool) IndexEntry {
	raw := []byte("false")
	if lts != "" {
		raw = []byte(`"` + lts + `"`)
	}

	return IndexEntry{Version: v, LTS: raw, Security: sec}
}

var schedule = map[string]Line{
	"v20": {End: "2026-04-30", Codename: "Iron"},
	"v22": {End: "2027-04-30", Codename: "Jod"},
	"v24": {End: "2028-04-30", Codename: "Krypton"},
	"v26": {End: "2029-04-30", Codename: "Lithium"},
	"v28": {End: "2031-04-30", Codename: "M"},
}

func TestCandidates(t *testing.T) {
	idx := []IndexEntry{
		entry("v27.0.0", "", false),
		entry("v26.13.2", "Lithium", true),
		entry("v26.13.1", "Lithium", false),
		entry("v26.12.0", "", false),
		entry("v24.22.3", "Krypton", false),
		entry("v24.9.10", "Krypton", false),
		entry("v20.20.0", "Iron", true),
		entry("v25.0.0-rc.1", "", false),
	}

	got := Candidates(idx, schedule, "2026-11-22")
	if len(got) != 2 || got["26"].Version != "26.13.2" || !got["26"].Security || got["24"].Version != "24.22.3" || got["26"].End != "2029-04-30" {
		t.Fatalf("candidates %+v", got)
	}
}

func cur() []agentapi.NodeRelease {
	return []agentapi.NodeRelease{
		{Major: "22", Version: "22.23.5", Codename: "Jod", End: "2027-04-30", SHA256: map[string]string{"x64": "a", "arm64": "b"}},
		{Major: "24", Version: "24.22.3", Codename: "Krypton", End: "2028-04-30", SHA256: map[string]string{"x64": "c", "arm64": "d"}},
	}
}

func TestPlan(t *testing.T) {
	var fetched []string

	sums := func(v string) (map[string]string, error) {
		fetched = append(fetched, v)

		return map[string]string{"x64": "x" + v, "arm64": "a" + v}, nil
	}

	cand := map[string]Candidate{
		"22": {Major: "22", Version: "22.23.5", Codename: "Jod", End: "2027-06-30"},
		"24": {Major: "24", Version: "24.22.4", Codename: "Krypton", End: "2028-04-30", Security: true},
		"28": {Major: "28", Version: "28.10.0", Codename: "M", End: "2031-04-30"},
	}

	out, rep, err := Plan(cur(), cand, sums)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(fetched, []string{"24.22.4", "28.10.0"}) {
		t.Errorf("fetched %v", fetched)
	}

	if len(out) != 3 || out[1].Version != "24.22.4" || out[1].SHA256["x64"] != "x24.22.4" || out[0].End != "2027-06-30" || out[2].Major != "28" {
		t.Fatalf("plan %+v", out)
	}

	if !rep.Security() || len(rep.Changes) != 3 {
		t.Errorf("report %+v", rep)
	}

	md := Markdown(rep)
	for _, want := range []string{"Node.js 24: 24.22.3 → 24.22.4 **(security release)**", "New LTS line: Node.js 28", "Node.js 22: end of life now 2027-06-30"} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}

	// Nothing new: no change, no download.
	fetched = nil

	_, rep, err = Plan(cur(), map[string]Candidate{"24": {Major: "24", Version: "24.22.3", End: "2028-04-30"}}, sums)
	if err != nil || rep.Changed() || len(fetched) != 0 {
		t.Errorf("unchanged: %+v %v %v", rep, err, fetched)
	}

	// A failed check stops the plan.
	if _, _, err := Plan(cur(), cand, func(string) (map[string]string, error) { return nil, errors.New("bad signature") }); err == nil {
		t.Error("plan without verified sums")
	}
}

func TestRewriteMatchesTheRepository(t *testing.T) {
	src, err := os.ReadFile("../../" + goFile)
	if err != nil {
		t.Fatal(err)
	}

	date := regexp.MustCompile(`// on (\d{4}-\d{2}-\d{2}) against`).FindSubmatch(src)
	if date == nil {
		t.Fatal("no date in the list's comment")
	}

	out, err := Rewrite(src, agentapi.NodeReleases, string(date[1]))
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(out, src) {
		t.Errorf("rewriting the list changes the file:\n%s", out)
	}

	// The new list goes in place of the old one.
	next := append(slices.Clone(agentapi.NodeReleases), agentapi.NodeRelease{Major: "28", Version: "28.1.0", Codename: "M", End: "2031-04-30", SHA256: map[string]string{"x64": "e", "arm64": "f"}})

	out, err = Rewrite(src, next, "2027-10-30")
	if err != nil || !bytes.Contains(out, []byte(`Major: "28", Version: "28.1.0"`)) || !bytes.Contains(out, []byte("// on 2027-10-30 against")) {
		t.Errorf("rewrite: %v\n%s", err, out)
	}

	if _, err := Rewrite([]byte("package agentapi\n"), next, "2027-10-30"); err == nil {
		t.Error("rewrote a file without the list")
	}
}
