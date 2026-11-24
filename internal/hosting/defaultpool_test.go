// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"testing"
)

// TestCreateOnTheDefaultPool: a site that names no pool lands on the pool
// the agent calls the default (ADR-0016 addendum 2); a named pool wins.
func TestCreateOnTheDefaultPool(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()

	def, err := "vol1", error(nil)
	s.ResolveDefaultPool(func(context.Context) (string, error) { return def, err })

	site, cerr := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}})
	if cerr != nil || site.Pool != "vol1" {
		t.Fatalf("site %+v, %v", site, cerr)
	}

	site, cerr = s.Create(ctx, 1, CreateRequest{Name: "shop", Pool: "default", Domains: []string{"shop.example.com"}})
	if cerr != nil || site.Pool != "default" {
		t.Fatalf("named pool %+v, %v", site, cerr)
	}

	// An agent that marks no default: the built-in name.
	def = ""

	site, cerr = s.Create(ctx, 1, CreateRequest{Name: "wiki", Domains: []string{"wiki.example.com"}})
	if cerr != nil || site.Pool != "default" {
		t.Fatalf("no default %+v, %v", site, cerr)
	}

	// The agent does not answer: nothing is created.
	err = errDown

	if _, cerr = s.Create(ctx, 1, CreateRequest{Name: "news", Domains: []string{"news.example.com"}}); !errors.Is(cerr, errDown) {
		t.Fatalf("agent down: %v", cerr)
	}

	if _, gerr := s.Get(ctx, "news"); !errors.Is(gerr, ErrNotFound) {
		t.Errorf("site news exists: %v", gerr)
	}
}
