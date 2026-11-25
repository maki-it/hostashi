// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"reflect"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/profile"
	"github.com/maki-it/hostashi/internal/store"
)

func (f *fakeAgent) hardening(user string) *agentapi.WebHardening {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, s := range f.web.Sites {
		if s.User == user {
			return s.Hardening
		}
	}

	return nil
}

func TestWebHardeningFollowsProfile(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	site, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "docs", Domains: []string{"docs.example.com"}}); err != nil {
		t.Fatal(err)
	}

	// Without a stored profile, PHP and static sites get the generic rules.
	generic := &agentapi.WebHardening{ConfigFiles: true}
	for _, name := range []string{"blog", "docs"} {
		if h := ag.hardening(name); !reflect.DeepEqual(h, generic) {
			t.Fatalf("%s: %+v", name, h)
		}
	}

	off := false
	allowXMLRPC := []string{}

	if _, err := s.st.SetSiteProfile(ctx, store.SiteProfile{
		SiteID: site.ID, Profile: profile.WordPress, Overrides: profile.Overrides{ConfigFiles: &off, DenyFiles: &allowXMLRPC},
	}, s.now()); err != nil {
		t.Fatal(err)
	}

	if err := s.applyWeb(ctx); err != nil {
		t.Fatal(err)
	}

	want := &agentapi.WebHardening{NoPHPDirs: []string{"wp-content/uploads"}, LimitFiles: []string{"wp-login.php"}}
	if h := ag.hardening("blog"); !reflect.DeepEqual(h, want) {
		t.Fatalf("wordpress with overrides: %+v", h)
	}

	// A disabled site runs no PHP: the rules that need PHP are dropped,
	// so the state stays valid.
	if _, err := s.SetDisabled(ctx, 1, "blog", true, ""); err != nil {
		t.Fatal(err)
	}

	if h := ag.hardening("blog"); h != nil {
		t.Fatalf("disabled: %+v", h)
	}

	// An unknown stored profile falls back to the default of the type.
	if _, err := s.st.SetSiteProfile(ctx, store.SiteProfile{SiteID: site.ID, Profile: "gone"}, s.now()); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetDisabled(ctx, 1, "blog", false, ""); err != nil {
		t.Fatal(err)
	}

	if h := ag.hardening("blog"); !reflect.DeepEqual(h, generic) {
		t.Fatalf("unknown profile: %+v", h)
	}
}
