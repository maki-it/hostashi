// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/store"
)

func TestNormalizeTitle(t *testing.T) {
	for _, c := range []struct {
		in, want string
		ok       bool
	}{
		{"", "", true},
		{"  Shop of Müller & Söhne  ", "Shop of Müller & Söhne", true},
		{"Kundenportal (alt) – 2024", "Kundenportal (alt) – 2024", true},
		{"日本語のサイト", "日本語のサイト", true},
		{strings.Repeat("ä", 64), strings.Repeat("ä", 64), true},
		{" " + strings.Repeat("a", 64) + " ", strings.Repeat("a", 64), true},
		{strings.Repeat("ä", 65), "", false},
		{"two\nlines", "", false},
		{"tab\there", "", false},
		{"bell\x07", "", false},
		{"del\x7f", "", false},
		{"rtl‮evil", "", false},   // right-to-left override
		{"zero​width", "", false}, // zero width space (Cf)
		{"line sep", "", false},
		{"bad\xffutf8", "", false},
	} {
		got, err := NormalizeTitle(c.in)
		if c.ok != (err == nil) || got != c.want {
			t.Errorf("%q: got %q, %v", c.in, got, err)
		}

		if err != nil && !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v is not ErrInvalid", c.in, err)
		}
	}
}

func TestSetTitle(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	site, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Title: "  Shop Müller ", Domains: []string{"shop.example.com"}})
	if err != nil || site.Title != "Shop Müller" {
		t.Fatalf("create: %+v %v", site, err)
	}

	// The title is panel data only: nothing goes to the agent.
	applies := ag.applies

	site, err = s.SetTitle(ctx, 1, "shop", "Müller GmbH – Shop")
	if err != nil || site.Title != "Müller GmbH – Shop" || site.Name != "shop" {
		t.Fatalf("set: %+v %v", site, err)
	}

	if ag.applies != applies {
		t.Fatal("a title change applied the web state")
	}

	if d := lastDiff(t, st, "site.title"); !changed(d["title"], "Shop Müller", "Müller GmbH – Shop") {
		t.Fatalf("audit diff %+v", d)
	}

	// Invalid input changes nothing.
	if _, err := s.SetTitle(ctx, 1, "shop", "a\nb"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("line break: %v", err)
	}

	if got, _ := st.SiteByName(ctx, "shop"); got.Title != "Müller GmbH – Shop" {
		t.Fatalf("after refused change: %q", got.Title)
	}

	// Empty clears it.
	if site, err = s.SetTitle(ctx, 1, "shop", " "); err != nil || site.Title != "" {
		t.Fatalf("clear: %+v %v", site, err)
	}

	if _, err := s.SetTitle(ctx, 1, "gone", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing site: %v", err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Title: "bad‮", Domains: []string{"blog.example.com"}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("create with a bad title: %v", err)
	}

	if _, err := st.SiteByName(ctx, "blog"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("site stored despite a bad title: %v", err)
	}
}
