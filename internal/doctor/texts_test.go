// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"slices"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Every text has both languages with the same placeholders, so a check
// renders in German whenever it renders in English.
func TestCatalogComplete(t *testing.T) {
	names := func(s string) []string {
		var out []string
		for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
			out = append(out, m[1])
		}

		slices.Sort(out)

		return slices.Compact(out)
	}

	for key, tx := range texts {
		if tx.en == "" || tx.de == "" {
			t.Errorf("%s: a language is empty", key)

			continue
		}

		if !slices.Equal(names(tx.en), names(tx.de)) {
			t.Errorf("%s: placeholders differ: %v and %v", key, names(tx.en), names(tx.de))
		}

		if tx.en == tx.de && !strings.Contains(tx.en, "{{") {
			t.Errorf("%s: the German text is the English one", key)
		}
	}
}

func TestGerman(t *testing.T) {
	c := warnT("system.disk", "disk.free", "disk.warn.hint", P{"path": "/srv", "free": "9.2 GiB", "total": "40.0 GiB", "percent": "23"})

	if c.Summary != "/srv: 9.2 GiB free of 40.0 GiB (23 %)" || c.Code != "disk.free" || c.HintCode != "disk.warn.hint" {
		t.Fatalf("English check = %+v", c)
	}

	sum, hint := German(c)
	if sum != "/srv: 9,2 GiB frei von 40,0 GiB (23 %)" || !strings.HasPrefix(hint, "Die Platte füllt sich.") {
		t.Errorf("German = %q, %q", sum, hint)
	}
}

func TestGermanPhraseParam(t *testing.T) {
	c := failT("system.fstab", "fstab.missing", "fstab.wait.hint", P{
		"path": "/etc/fstab", "devices": "LABEL=web for /home (line 3)", "devices@de": "LABEL=web für /home (Zeile 3)",
	})

	if sum, _ := German(c); !strings.Contains(sum, "LABEL=web für /home (Zeile 3)") {
		t.Errorf("German summary = %q", sum)
	}

	if strings.Contains(c.Summary, "Zeile") {
		t.Errorf("English summary = %q", c.Summary)
	}
}

// A check without a code, or with one this version does not know, as
// from an older agent, stays English.
func TestGermanFallback(t *testing.T) {
	for _, c := range []Check{
		{ID: "x", Status: agentapi.DoctorWarn, Summary: "plain", Hint: "hint"},
		{ID: "x", Status: agentapi.DoctorWarn, Summary: "plain", Hint: "hint", Code: "no.such", HintCode: "no.such"},
		{ID: "x", Status: agentapi.DoctorWarn, Summary: "plain", Hint: "hint", Code: "disk.free"}, // params missing
	} {
		if sum, hint := German(c); sum != "plain" || hint != "hint" {
			t.Errorf("German(%+v) = %q, %q", c, sum, hint)
		}
	}
}

func TestTextPanicsOnUnknownKey(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("no panic")
		}
	}()

	Text("no.such", nil)
}

func TestGermanTitle(t *testing.T) {
	if GermanTitle("system.disk") != "Freier Speicher" || GermanTitle("new.check") != "new.check" {
		t.Error("GermanTitle")
	}
}
