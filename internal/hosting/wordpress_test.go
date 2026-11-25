// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/profile"
)

type fakeWP struct {
	reqs []agentapi.WPInstallRequest
	err  error
}

func (f *fakeWP) WPInstallStart(_ context.Context, req agentapi.WPInstallRequest) (agentapi.WPInstallStatus, error) {
	if err := req.Validate(); err != nil {
		return agentapi.WPInstallStatus{}, &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: err.Error()}
	}

	if f.err != nil {
		return agentapi.WPInstallStatus{}, f.err
	}

	f.reqs = append(f.reqs, req)

	return agentapi.WPInstallStatus{Site: req.Site, State: "running"}, nil
}

func (f *fakeWP) WPInstallStatus(_ context.Context, req agentapi.WPStatusRequest) (agentapi.WPInstallStatus, error) {
	st := agentapi.WPInstallStatus{Site: req.Site, State: agentapi.WPNone}
	if len(f.reqs) > 0 {
		st.State = "succeeded"
	}

	return st, nil
}

func wpInput() WordPressInput {
	return WordPressInput{Locale: "de_DE", Title: "Kims Blog", AdminUser: "kim", AdminEmail: "kim@example.com"}
}

func TestInstallWordPress(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if _, err := s.InstallWordPress(ctx, 1, "my-blog", wpInput()); !errors.Is(err, ErrNoWordPress) {
		t.Fatalf("without agent: %v", err)
	}

	f := &fakeWP{}
	s.EnableWordPress(f)

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "my-blog", PHP: "8.4", Domains: []string{"blog.example.com", "www.blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	got, err := s.InstallWordPress(ctx, 1, "my-blog", wpInput())
	if err != nil {
		t.Fatal(err)
	}

	if got.Database != "my_blog_wp" || len(got.AdminPassword) < agentapi.MinWPPassword || got.Status.State != "running" {
		t.Fatalf("start: %+v", got)
	}

	req := f.reqs[0]
	if req.AdminPassword != got.AdminPassword || req.DBPassword == "" || req.DBPassword == req.AdminPassword ||
		req.Install.DBName != "my_blog_wp" || req.Install.DBUser != "my_blog_wp" || req.Install.DBHost != "localhost" ||
		req.Install.URL != "http://blog.example.com" || req.Domain != "blog.example.com" || req.PHP != "8.4" {
		t.Fatalf("request: %+v", req)
	}

	if ag.dbs["my_blog_wp"] != "my-blog" || ag.dbUsers["my_blog_wp"] != "my_blog_wp" {
		t.Fatalf("database: %v %v", ag.dbs, ag.dbUsers)
	}

	// The generic PHP profile became WordPress.
	if p, err := s.SiteProfile(ctx, "my-blog"); err != nil || p.Profile.Name != profile.WordPress {
		t.Fatalf("profile: %+v %v", p, err)
	}

	if st, err := s.WordPressStatus(ctx, "my-blog"); err != nil || st.State != "succeeded" {
		t.Fatalf("status: %+v %v", st, err)
	}

	// A typed password is used and not returned; the next database name is
	// free.
	in := wpInput()
	in.AdminPassword = "correct horse battery"

	got, err = s.InstallWordPress(ctx, 1, "my-blog", in)
	if err != nil || got.AdminPassword != "" || got.Database != "my_blog_wp2" || f.reqs[1].AdminPassword != in.AdminPassword {
		t.Fatalf("typed password: %+v %v", got, err)
	}
}

func TestInstallWordPressRefuses(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()
	f := &fakeWP{}
	s.EnableWordPress(f)

	for _, r := range []CreateRequest{
		{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}},
		{Name: "docs", Domains: []string{"docs.example.com"}},
	} {
		if _, err := s.Create(ctx, 1, r); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.InstallWordPress(ctx, 1, "docs", wpInput()); !errors.Is(err, ErrSiteType) {
		t.Fatalf("static site: %v", err)
	}

	if _, err := s.InstallWordPress(ctx, 1, "none", wpInput()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	for name, change := range map[string]func(*WordPressInput){
		"locale":      func(in *WordPressInput) { in.Locale = "xx_XX" },
		"admin user":  func(in *WordPressInput) { in.AdminUser = "admin" },
		"option user": func(in *WordPressInput) { in.AdminUser = "-kim" },
		"email":       func(in *WordPressInput) { in.AdminEmail = "Kim <kim@example.com>" },
		"title":       func(in *WordPressInput) { in.Title = "" },
		"password":    func(in *WordPressInput) { in.AdminPassword = "short" },
	} {
		in := wpInput()
		change(&in)

		if _, err := s.InstallWordPress(ctx, 1, "blog", in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if len(ag.dbs) != 0 {
		t.Fatalf("databases after refused input: %v", ag.dbs)
	}

	// The agent refuses (for example a document root that is not empty):
	// the database goes again and the profile stays.
	f.err = &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "the document root is not empty"}

	if _, err := s.InstallWordPress(ctx, 1, "blog", wpInput()); err == nil {
		t.Fatal("agent refusal passed")
	}

	if len(ag.dbs) != 0 || len(ag.dbUsers) != 0 {
		t.Fatalf("database kept: %v %v", ag.dbs, ag.dbUsers)
	}

	if p, err := s.SiteProfile(ctx, "blog"); err != nil || p.Profile.Name != profile.GenericPHP {
		t.Fatalf("profile: %+v %v", p, err)
	}
}
