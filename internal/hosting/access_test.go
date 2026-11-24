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
