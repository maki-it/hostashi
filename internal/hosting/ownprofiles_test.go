// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/profile"
	"github.com/maki-it/hostashi/internal/store"
)

func agencyProfile() profile.Profile {
	return profile.Profile{
		Name: "agency", Title: "Agency PHP", Type: "php", MinPHP: "8.2", WebDir: "public",
		Hardening: agentapi.WebHardening{ConfigFiles: true, DenyFiles: []string{"install.php"}, NoPHPDirs: []string{"uploads"}},
		PageCache: agentapi.PageCachePHP, Checklist: []string{"Deploy into htdocs."},
	}
}

func TestOwnProfiles(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	p, err := s.CreateProfile(ctx, 1, agencyProfile())
	if err != nil || p.Builtin || p.Name != "agency" {
		t.Fatalf("create: %+v %v", p, err)
	}

	if _, err := s.CreateProfile(ctx, 1, agencyProfile()); !errors.Is(err, ErrProfileExists) {
		t.Fatalf("twice: %v", err)
	}

	bad := agencyProfile()
	bad.Name = profile.WordPress

	if _, err := s.CreateProfile(ctx, 1, bad); !errors.Is(err, ErrInvalid) || strings.Contains(err.Error(), "profile: invalid") {
		t.Fatalf("built-in name: %v", err)
	}

	// The list has the built-in profiles first, then the own ones.
	all, err := s.ListProfiles(ctx)
	if err != nil || len(all) != len(profile.Builtins())+1 || all[0].Name != profile.Static || all[len(all)-1].Name != "agency" || all[len(all)-1].Builtin {
		t.Fatalf("list: %+v %v", all, err)
	}

	// A new site with the own profile gets its folder and its rules.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "old", PHP: "8.1", Domains: []string{"old.example.com"}, Profile: "agency"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("PHP too old: %v", err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "docs", Domains: []string{"docs.example.com"}, Profile: "agency"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("static site with a PHP profile: %v", err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", PHP: "8.4", Domains: []string{"shop.example.com"}, Profile: "agency"}); err != nil {
		t.Fatal(err)
	}

	if site, err := s.Get(ctx, "shop"); err != nil || site.WebDir != "public" {
		t.Fatalf("web dir: %+v %v", site, err)
	}

	if h := ag.hardening("shop"); !reflect.DeepEqual(h, &agentapi.WebHardening{ConfigFiles: true, NoPHPDirs: []string{"uploads"}, DenyFiles: []string{"install.php"}}) {
		t.Fatalf("hardening: %+v", h)
	}

	// The site keeps a change of its own; the Hardening tab offers the
	// own profile.
	want := agentapi.WebHardening{ConfigFiles: true, NoPHPDirs: []string{"uploads"}}

	state, err := s.SetSiteProfile(ctx, 1, "shop", "agency", &want)
	if err != nil || !state.Changed || state.Choices[len(state.Choices)-1].Name != "agency" {
		t.Fatalf("set: %+v %v", state, err)
	}

	// An edit reaches the site at once, with its own change kept.
	edit := agencyProfile()
	edit.Hardening.LimitFiles = []string{"login.php"}

	if _, err := s.UpdateProfile(ctx, 1, "agency", edit); err != nil {
		t.Fatal(err)
	}

	if h := ag.hardening("shop"); !reflect.DeepEqual(h, &agentapi.WebHardening{ConfigFiles: true, NoPHPDirs: []string{"uploads"}, LimitFiles: []string{"login.php"}}) {
		t.Fatalf("after edit: %+v", h)
	}

	if d := lastDiff(t, st, "profile.update"); d["hardening"].New == nil || !strings.Contains(*d["hardening"].New, "login.php") {
		t.Fatalf("audit: %+v", d)
	}

	// A failed web state puts the profile back.
	failed := edit
	failed.Title = "Broken"
	ag.webErr = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "nginx rejected the configuration"}

	if _, err := s.UpdateProfile(ctx, 1, "agency", failed); err == nil {
		t.Fatal("failed apply accepted")
	}

	ag.webErr = nil

	if p, err := s.Profile(ctx, "agency"); err != nil || p.Title != "Agency PHP" {
		t.Fatalf("restored: %+v %v", p, err)
	}

	// Renames, type changes in use and built-in profiles are refused.
	renamed := edit
	renamed.Name = "other"

	if _, err := s.UpdateProfile(ctx, 1, "agency", renamed); !errors.Is(err, ErrInvalid) {
		t.Fatalf("rename: %v", err)
	}

	static := profile.Profile{Name: "agency", Title: "Static", Type: "static"}
	if _, err := s.UpdateProfile(ctx, 1, "agency", static); !errors.Is(err, ErrProfileInUse) || !strings.Contains(err.Error(), "shop") {
		t.Fatalf("type change in use: %v", err)
	}

	if _, err := s.UpdateProfile(ctx, 1, profile.WordPress, edit); !errors.Is(err, ErrProfileBuiltin) {
		t.Fatalf("built-in: %v", err)
	}

	if _, err := s.UpdateProfile(ctx, 1, "nope", profile.Profile{Name: "nope", Title: "Nope", Type: "php"}); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("missing: %v", err)
	}

	// Delete waits until no site uses the profile.
	if err := s.DeleteProfile(ctx, 1, "agency"); !errors.Is(err, ErrProfileInUse) || !strings.Contains(err.Error(), "shop") {
		t.Fatalf("delete in use: %v", err)
	}

	if err := s.DeleteProfile(ctx, 1, profile.WordPress); !errors.Is(err, ErrProfileBuiltin) {
		t.Fatalf("delete built-in: %v", err)
	}

	if _, err := s.SetSiteProfile(ctx, 1, "shop", profile.GenericPHP, nil); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteProfile(ctx, 1, "agency"); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteProfile(ctx, 1, "agency"); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("delete twice: %v", err)
	}

	for _, action := range []string{"profile.create", "profile.delete"} {
		if es, _, err := st.AuditEntries(ctx, store.AuditFilter{Action: action}, 0, 5); err != nil || len(es) != 1 || es[0].Target != "agency" {
			t.Fatalf("%s: %+v %v", action, es, err)
		}
	}
}

func TestImportProfile(t *testing.T) {
	s, _, st := newService(t)
	ctx := t.Context()

	b, _ := profile.Builtin(profile.Drupal)

	out, err := profile.ExportYAML(b)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.ImportProfile(ctx, 1, out); !errors.Is(err, ErrInvalid) {
		t.Fatalf("built-in name: %v", err)
	}

	p, err := s.ImportProfile(ctx, 1, []byte(strings.Replace(string(out), "name: drupal", "name: my-drupal", 1)))
	if err != nil || p.WebDir != "web" || p.Name != "my-drupal" {
		t.Fatalf("import: %+v %v", p, err)
	}

	if _, err := s.ImportProfile(ctx, 1, []byte("version: 1\nname: x\ntitle: X\ntype: php\nhardening: {}\nnginx: deny all;\n")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown field: %v", err)
	}

	if es, _, err := st.AuditEntries(ctx, store.AuditFilter{Action: "profile.import"}, 0, 5); err != nil || len(es) != 1 || es[0].Target != "my-drupal" {
		t.Fatalf("audit: %+v %v", es, err)
	}
}
