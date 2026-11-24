// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

func TestCheckAccess(t *testing.T) {
	s := &Service{localAddrs: func() ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.0.0.3")}, nil
	}}
	users := []store.AuthUser{{Name: "kim", Hash: "$2a$05$7tXviV9DiqwPre8Yge0sUu000CCwS9nkgKr.DLJbkteSq.2Ar/Hv2"}}

	if err := s.checkAccess(store.AccessSettings{Password: true, Bypass: []string{"10.0.1.0/24"}}, users, store.SiteSnippets{}); err != nil {
		t.Fatal(err)
	}

	for name, c := range map[string]struct {
		a   store.AccessSettings
		sn  store.SiteSnippets
		err error
	}{
		"own address":      {store.AccessSettings{Password: true, Bypass: []string{"10.0.0.0/24"}}, store.SiteSnippets{}, agentapi.ErrAccessRejected},
		"own address off":  {store.AccessSettings{Bypass: []string{"10.0.0.3"}}, store.SiteSnippets{}, agentapi.ErrAccessRejected},
		"bad path off":     {store.AccessSettings{Paths: []string{"x"}}, store.SiteSnippets{}, agentapi.ErrAccessRejected},
		"snippet conflict": {store.AccessSettings{IPMode: agentapi.AccessIPDeny, IPs: []string{"203.0.113.7"}}, store.SiteSnippets{Server: "allow 10.0.0.1;"}, agentapi.ErrAccessConflict},
	} {
		err := s.checkAccess(c.a, users, c.sn)
		if !errors.Is(err, ErrInvalid) || !errors.Is(err, c.err) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if err := s.checkAccess(store.AccessSettings{Password: true}, nil, store.SiteSnippets{}); !errors.Is(err, agentapi.ErrAccessRejected) {
		t.Errorf("no users: %v", err)
	}

	if w := webAccessOf(store.AccessSettings{Paths: []string{"/a"}}, users); !w.IsZero() {
		t.Errorf("password off sends rules: %+v", w)
	}
}

func TestAccessOnRedirectSite(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()

	redirect := &store.SiteRedirect{Target: "https://example.com", Status: 302}
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "old", Domains: []string{"old.example.com"}, Redirect: redirect}); err != nil {
		t.Fatal(err)
	}

	deny := store.AccessSettings{IPMode: agentapi.AccessIPDeny, IPs: []string{"192.0.2.1"}}
	if _, err := s.SetAccess(ctx, 1, "old", deny); !errors.Is(err, ErrSiteType) {
		t.Fatalf("IP rule on a redirect: %v", err)
	}

	if _, err := s.SetAccess(ctx, 1, "old", store.AccessSettings{Password: true}); !errors.Is(err, ErrSiteType) {
		t.Fatalf("password on a redirect: %v", err)
	}

	// Switching everything off stays possible.
	if _, err := s.SetAccess(ctx, 1, "old", store.AccessSettings{}); err != nil {
		t.Fatalf("off on a redirect: %v", err)
	}

	// A setting kept from before a type change is not sent for a redirect.
	all := map[int64]store.SiteAccess{7: {SiteID: 7, Settings: deny}}
	if w := webAccess(all, nil, store.Site{ID: 7, Type: store.SiteTypeRedirect}); w != nil {
		t.Fatalf("redirect: %+v", w)
	}

	if w := webAccess(all, nil, store.Site{ID: 7, Type: store.SiteTypeStatic}); w == nil || w.IPMode != agentapi.AccessIPDeny {
		t.Fatalf("static: %+v", w)
	}
}
