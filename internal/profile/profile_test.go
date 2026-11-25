// SPDX-License-Identifier: AGPL-3.0-or-later

package profile

import (
	"reflect"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestBuiltins(t *testing.T) {
	seen := map[string]bool{}

	for _, p := range Builtins() {
		if seen[p.Name] || p.Name == "" || p.Title == "" || !p.Builtin {
			t.Errorf("%+v", p)
		}

		seen[p.Name] = true

		if err := p.Hardening.Validate(p.Type == "php"); err != nil {
			t.Errorf("%s: %v", p.Name, err)
		}

		if err := agentapi.ValidateWebDir(p.WebDir); err != nil {
			t.Errorf("%s: %v", p.Name, err)
		}

		if p.Type != "php" && p.Type != "static" {
			t.Errorf("%s: type %q", p.Name, p.Type)
		}
	}

	for _, n := range []string{Static, GenericPHP, WordPress, WooCommerce, Laravel, Symfony, Shopware, TYPO3, Drupal} {
		if !seen[n] {
			t.Errorf("%s missing", n)
		}
	}

	// Callers cannot change the built-ins through a returned copy.
	p, _ := Builtin(WordPress)
	p.Hardening.DenyFiles[0] = "changed"

	if q, _ := Builtin(WordPress); q.Hardening.DenyFiles[0] != "xmlrpc.php" {
		t.Fatal("built-in changed through a copy")
	}

	if _, ok := Builtin("nope"); ok {
		t.Fatal("unknown profile found")
	}
}

func TestDefault(t *testing.T) {
	for typ, want := range map[string]string{"static": Static, "php": GenericPHP, "node": "", "proxy": ""} {
		if got, ok := Default(typ); got != want || ok != (want != "") {
			t.Errorf("%s: %q %v", typ, got, ok)
		}
	}
}

func TestDiffAndEffective(t *testing.T) {
	wp, _ := Builtin(WordPress)

	if o := Diff(wp.Hardening, wp.Hardening); !o.IsZero() {
		t.Fatalf("no change: %+v", o)
	}

	want := wp.Hardening
	want.ConfigFiles = false
	want.DenyFiles = nil
	want.DenyDirs = []string{"private"}

	o := Diff(wp.Hardening, want)
	if o.ConfigFiles == nil || o.DenyFiles == nil || len(*o.DenyFiles) != 0 || o.DenyDirs == nil || o.NoPHPDirs != nil || o.LimitFiles != nil {
		t.Fatalf("diff: %+v", o)
	}

	if got := Effective(wp, o, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("effective %+v, want %+v", got, want)
	}

	// A later change of the profile reaches the values the site kept.
	wp.Hardening.NoPHPDirs = []string{"wp-content/uploads", "wp-content/cache"}
	if got := Effective(wp, o, true); len(got.NoPHPDirs) != 2 {
		t.Fatalf("profile change lost: %+v", got)
	}

	// Without PHP, the rules that need it are dropped.
	if got := Effective(wp, o, false); got.NoPHPDirs != nil || got.LimitFiles != nil || got.Validate(false) != nil {
		t.Fatalf("without PHP: %+v", got)
	}
}
