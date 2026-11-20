// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

const repo = "../.."

// A shortened copy of php.net/supported-versions.php, same markup.
const supportedHTML = `<table class="standard">
	<tbody>
		<tr class="security">
			<td>
				<a href="/downloads.php?version=8.3">8.3</a>
			</td>
			<td>23 Nov 2023</td>
			<td class="collapse-phone"><em>2 years, 11 months ago</em></td>
			<td>31 Dec 2025</td>
			<td class="collapse-phone"><em>10 months ago</em></td>
			<td>31 Dec 2027</td>
			<td class="collapse-phone"><em>in 1 year, 1 month</em></td>
			<td>
				<a href="https://www.php.net/manual/migration83.php">Migration guide for PHP 8.3</a>
			</td>
		</tr>
		<tr class="stable">
			<td>
				<a href="/downloads.php?version=8.7">8.7</a>
			</td>
			<td>18 Nov 2027</td>
			<td class="collapse-phone"><em>in 1 year</em></td>
			<td>31 Dec 2029</td>
			<td class="collapse-phone"><em>in 3 years</em></td>
			<td>31 Dec 2031</td>
			<td class="collapse-phone"><em>in 5 years</em></td>
			<td></td>
		</tr>
	</tbody>
</table>`

func TestParseSupported(t *testing.T) {
	got, err := ParseSupported([]byte(supportedHTML))
	if err != nil {
		t.Fatal(err)
	}

	want := map[string]Dates{
		"8.3": {Active: "2025-12-31", Security: "2027-12-31"},
		"8.7": {Active: "2029-12-31", Security: "2031-12-31"},
	}
	if !maps.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	for name, page := range map[string]string{
		"no table":       "<html>moved</html>",
		"bad date":       strings.Replace(supportedHTML, "31 Dec 2027", "Dec 2027", 1),
		"security first": strings.Replace(supportedHTML, "31 Dec 2027", "31 Dec 2024", 1),
		"bad branch":     strings.Replace(supportedHTML, `version=8.3">`, `version=8.3.1">`, 1),
	} {
		if _, err := ParseSupported([]byte(page)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseActive(t *testing.T) {
	got, err := ParseActive([]byte(`{"8":{"8.10":{"version":"8.10.0"},"8.2":{"version":"8.2.36"},"8.6":{}},"9":{"9.0":{}}}`))
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"8.2", "8.6", "8.10", "9.0"}; !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	for _, doc := range []string{``, `[]`, `{}`, `{"8":{}}`, `{"8":{"8.6.0":{}}}`, `{"8":{"../8":{}}}`} {
		if _, err := ParseActive([]byte(doc)); err == nil {
			t.Errorf("%q accepted", doc)
		}
	}
}

func TestParsePackages(t *testing.T) {
	got, err := ParsePackages(strings.NewReader("Package: php8.7-fpm\nVersion: 8.7.0-1\n\nPackage: php8.7-cli\nDepends: libc6\n"))
	if err != nil {
		t.Fatal(err)
	}

	if !got["php8.7-fpm"] || !got["php8.7-cli"] || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func current() State {
	return State{
		Versions: agentapi.PHPVersions,
		Legacy:   agentapi.LegacyPHPVersions,
		Security: agentapi.PHPSecurityUntil,
		Active:   agentapi.PHPActiveUntil,
	}
}

func base() State {
	return State{
		Versions: []string{"8.1", "8.2", "8.6"},
		Legacy:   []string{"8.1"},
		Security: map[string]string{"8.1": "2025-12-31", "8.2": "2026-12-31", "8.6": "2030-12-31"},
		Active:   map[string]string{"8.2": "2024-12-31", "8.6": "2028-12-31"},
	}
}

func complete(string) []string { return nil }

func TestPlanAddsAReleasedVersion(t *testing.T) {
	cur := base()
	up := Upstream{
		// 8.5 is older than the newest known version: not added, a gap is a decision.
		Released: []string{"8.7", "8.2", "8.5", "8.6"},
		Dates: map[string]Dates{
			"8.2": {Active: "2024-12-31", Security: "2026-12-31"},
			"8.5": {Active: "2027-12-31", Security: "2029-12-31"},
			"8.6": {Active: "2028-12-31", Security: "2030-12-31"},
			"8.7": {Active: "2029-12-31", Security: "2031-12-31"},
		},
		Missing: complete,
	}

	next, rep := Plan(cur, up)
	if !slices.Equal(next.Versions, []string{"8.1", "8.2", "8.6", "8.7"}) || !slices.Equal(rep.Added, []string{"8.7"}) {
		t.Fatalf("versions %v, added %v", next.Versions, rep.Added)
	}

	if next.Security["8.7"] != "2031-12-31" || next.Active["8.7"] != "2029-12-31" || len(rep.Dates) != 0 {
		t.Fatalf("dates %v %v %v", next.Security, next.Active, rep.Dates)
	}

	if !slices.Equal(next.Legacy, []string{"8.1"}) || !rep.Changed() {
		t.Fatalf("legacy %v, changed %v", next.Legacy, rep.Changed())
	}

	// The current state is not modified.
	if len(cur.Versions) != 3 || cur.Security["8.7"] != "" {
		t.Fatalf("input changed: %v", cur)
	}
}

func TestPlanWaitsForSury(t *testing.T) {
	up := Upstream{
		Released: []string{"8.7"},
		Dates:    map[string]Dates{"8.7": {Active: "2029-12-31", Security: "2031-12-31"}},
		Missing: func(v string) []string {
			return []string{"resolute/arm64: php" + v + "-intl"}
		},
	}

	next, rep := Plan(base(), up)
	if rep.Changed() || len(next.Versions) != 3 || len(rep.Waiting["8.7"]) != 1 {
		t.Fatalf("next %v, report %+v", next.Versions, rep)
	}

	if md := Markdown(rep); !strings.Contains(md, "PHP 8.7 is released on php.net but not complete on sury.org yet") ||
		!strings.Contains(md, "resolute/arm64: php8.7-intl") {
		t.Fatalf("markdown: %s", md)
	}
}

func TestPlanNeedsDates(t *testing.T) {
	// Listed as released, but not in the table yet: wait.
	next, rep := Plan(base(), Upstream{Released: []string{"8.7"}, Dates: map[string]Dates{}, Missing: complete})
	if rep.Changed() || len(next.Versions) != 3 {
		t.Fatalf("next %v, report %+v", next.Versions, rep)
	}

	if md := Markdown(rep); !strings.HasPrefix(md, "No new PHP version") {
		t.Fatalf("markdown: %s", md)
	}
}

func TestPlanFollowsChangedDates(t *testing.T) {
	up := Upstream{
		Released: []string{"8.2", "8.6"},
		Dates: map[string]Dates{
			"8.2": {Active: "2024-12-31", Security: "2027-06-30"},
			"8.6": {Active: "2028-12-31", Security: "2030-12-31"},
		},
		Missing: complete,
	}

	next, rep := Plan(base(), up)
	if next.Security["8.2"] != "2027-06-30" || !slices.Equal(rep.Dates, []string{"8.2 security support: 2026-12-31 -> 2027-06-30"}) {
		t.Fatalf("security %v, dates %v", next.Security, rep.Dates)
	}

	// 8.1 is not on php.net any more: its date stays.
	if next.Security["8.1"] != "2025-12-31" || len(next.Versions) != 3 {
		t.Fatalf("next %+v", next)
	}
}

// The file in the repository is exactly what the tool renders, so a run
// without upstream changes leaves it as it is.
func TestRenderGoMatchesTheRepository(t *testing.T) {
	want, err := os.ReadFile(filepath.Join(repo, goFile))
	if err != nil {
		t.Fatal(err)
	}

	got, err := RenderGo(current())
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("rendered file differs from %s:\n%s", goFile, got)
	}
}

func TestRenderGoComment(t *testing.T) {
	s := base()
	s.Versions = append(s.Versions, "8.7")

	got, err := RenderGo(s)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"// 8.2 to 8.7 as standard, 8.1 as legacy.",
		`var PHPVersions = []string{"8.1", "8.2", "8.6", "8.7"}`,
	} {
		if !bytes.Contains(got, []byte(want)) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}

	if _, err := RenderGo(State{Versions: []string{"8.1"}, Legacy: []string{"8.1"}}); err == nil {
		t.Error("only legacy versions accepted")
	}
}

func TestRewritesAreStable(t *testing.T) {
	for name, rewrite := range map[string]func([]byte, []string) ([]byte, error){
		installerFile:  RewriteInstaller,
		phpInstallFile: RewritePHPInstall,
		featuresFile:   RewriteFeatures,
	} {
		b, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			t.Fatal(err)
		}

		same, err := rewrite(b, agentapi.PHPVersions)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if !bytes.Equal(same, b) {
			t.Errorf("%s: rewriting with the current versions changes the file", name)
		}

		more := append(slices.Clone(agentapi.PHPVersions), "9.9")

		out, err := rewrite(b, more)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if !bytes.Contains(out, []byte("9.9")) || bytes.Equal(out, b) {
			t.Errorf("%s: 9.9 not written", name)
		}

		if _, err := rewrite([]byte("nothing to see\n"), more); err == nil {
			t.Errorf("%s: a file without the list accepted", name)
		}
	}
}

func TestRewriteInstaller(t *testing.T) {
	in := "#   --php-versions LIST  more\n#                        from 8.1, 8.2 (8.1 is legacy). Kept\nPHP_VERSIONS=(8.1 8.2)\n"

	out, err := RewriteInstaller([]byte(in), []string{"8.1", "8.2", "8.10"})
	if err != nil {
		t.Fatal(err)
	}

	want := "#   --php-versions LIST  more\n#                        from 8.1, 8.2, 8.10 (8.1 is legacy). Kept\nPHP_VERSIONS=(8.1 8.2 8.10)\n"
	if string(out) != want {
		t.Fatalf("got\n%s", out)
	}

	if _, err := RewriteInstaller([]byte(in+"PHP_VERSIONS=(8.1)\n"), []string{"8.1"}); err == nil {
		t.Fatal("two lists accepted")
	}
}

func TestPackageSuffixes(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(repo, phpInstallFile))
	if err != nil {
		t.Fatal(err)
	}

	got, err := PackageSuffixes(b)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Contains(got, "fpm") || !slices.Contains(got, "intl") || len(got) < 5 {
		t.Fatalf("got %v", got)
	}

	if _, err := PackageSuffixes([]byte("php_pkgs() { echo; }")); err == nil {
		t.Fatal("missing list accepted")
	}
}

func TestApply(t *testing.T) {
	root := t.TempDir()

	for _, name := range []string{goFile, installerFile, phpInstallFile, featuresFile} {
		b, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			t.Fatal(err)
		}

		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(root, name), b, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	s := current()
	s.Versions = append(slices.Clone(s.Versions), "9.9")
	s.Security = maps.Clone(s.Security)
	s.Security["9.9"] = "2040-12-31"

	if err := Apply(root, s); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{goFile, installerFile, phpInstallFile, featuresFile} {
		b, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}

		if !bytes.Contains(b, []byte("9.9")) {
			t.Errorf("%s: 9.9 missing", name)
		}
	}

	if info, err := os.Stat(filepath.Join(root, installerFile)); err != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("installer mode: %v %v", info, err)
	}
}
