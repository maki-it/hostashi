// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

func TestSetWWWRedirect(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com", "shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	// No pair: the error names what to add, nothing is stored or applied.
	applies := ag.applies

	_, err := s.SetWWWRedirect(ctx, 1, "blog", agentapi.WWWRedirectToWWW)
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "add www.blog.example.com or www.shop.example.com as a domain first") {
		t.Fatalf("no pair: %v", err)
	}

	if ag.applies != applies {
		t.Fatal("applied without a pair")
	}

	// Off is always fine.
	if _, err := s.SetWWWRedirect(ctx, 1, "blog", agentapi.WWWRedirectOff); err != nil {
		t.Fatalf("off: %v", err)
	}

	if _, err := s.SetDomains(ctx, 1, "blog", []string{"blog.example.com", "www.blog.example.com", "shop.example.com"}); err != nil {
		t.Fatal(err)
	}

	site, err := s.SetWWWRedirect(ctx, 1, "blog", agentapi.WWWRedirectToNonWWW)
	if err != nil || site.WWWRedirect != agentapi.WWWRedirectToNonWWW {
		t.Fatalf("set: %+v %v", site, err)
	}

	if ws, _ := webOf(ag, "blog"); ws.WWWRedirect != agentapi.WWWRedirectToNonWWW {
		t.Fatalf("web %+v", ws)
	}

	// A failed apply keeps the previous mode.
	ag.webErr = errDown

	if _, err := s.SetWWWRedirect(ctx, 1, "blog", agentapi.WWWRedirectToWWW); !errors.Is(err, errDown) {
		t.Fatalf("failed apply: %v", err)
	}

	ag.webErr = nil

	if site, _ := s.Get(ctx, "blog"); site.WWWRedirect != agentapi.WWWRedirectToNonWWW {
		t.Fatalf("after failure: %q", site.WWWRedirect)
	}

	for name, mode := range map[string]string{"unknown": "both", "off spelled out": "off"} {
		if _, err := s.SetWWWRedirect(ctx, 1, "blog", mode); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, err := s.SetWWWRedirect(ctx, 1, "nope", agentapi.WWWRedirectToWWW); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown site: %v", err)
	}

	// A redirect site refuses it and keeps the stored value without
	// sending it to the agent.
	site, err = s.SetType(ctx, 1, "blog", TypeRequest{Type: store.SiteTypeRedirect, Redirect: &store.SiteRedirect{Target: "https://example.org", Status: 301}})
	if err != nil || site.WWWRedirect != agentapi.WWWRedirectToNonWWW {
		t.Fatalf("to redirect: %+v %v", site, err)
	}

	if ws, _ := webOf(ag, "blog"); ws.WWWRedirect != "" {
		t.Fatalf("redirect site got %q", ws.WWWRedirect)
	}

	if _, err := s.SetWWWRedirect(ctx, 1, "blog", agentapi.WWWRedirectToWWW); !errors.Is(err, ErrSiteType) {
		t.Errorf("redirect site: %v", err)
	}
}
