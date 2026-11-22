// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestRedirectSite(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	r := SiteRedirect{Target: "https://new.example.com", Status: 308, KeepPath: true}

	created, err := s.CreateRedirectSite(ctx, "old", "default", r, []string{"old.example.com"}, now)
	if err != nil || created.Type != SiteTypeRedirect || created.Account || !reflect.DeepEqual(created.Redirect, &r) {
		t.Fatalf("create: %+v %v", created, err)
	}

	got, err := s.SiteByName(ctx, "old")
	if err != nil || got.Type != SiteTypeRedirect || got.Account || !reflect.DeepEqual(got.Redirect, &r) || got.PHP != "" {
		t.Fatalf("read: %+v %v", got, err)
	}

	// An ordinary site has an account and no redirect.
	blog, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now)
	if err != nil || !blog.Account || blog.Redirect != nil {
		t.Fatalf("blog: %+v %v", blog, err)
	}

	if got, _ := s.SiteByName(ctx, "blog"); !got.Account || got.Redirect != nil {
		t.Fatalf("blog read: %+v", got)
	}

	next := SiteRedirect{Target: "http://other.example.com/x", Status: 302}

	old, err := s.SetSiteRedirect(ctx, "old", next, now)
	if err != nil || old != r {
		t.Fatalf("set: %+v %v", old, err)
	}

	if got, _ := s.SiteByName(ctx, "old"); !reflect.DeepEqual(got.Redirect, &next) {
		t.Fatalf("after set: %+v", got.Redirect)
	}

	// No PHP, files or redirect where they do not fit.
	if _, err := s.SetSiteRedirect(ctx, "blog", next, now); !errors.Is(err, ErrSiteType) {
		t.Fatalf("redirect on php: %v", err)
	}

	if _, err := s.SetSitePHP(ctx, "old", "8.4", now); !errors.Is(err, ErrSiteType) {
		t.Fatalf("php on redirect: %v", err)
	}

	if _, err := s.SetSiteWebOptions(ctx, "old", WebOptions{Dir: "x"}, now); !errors.Is(err, ErrSiteType) {
		t.Fatalf("web options on redirect: %v", err)
	}

	if _, err := s.SetSiteRedirect(ctx, "nope", next, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}

	// The database refuses what the code would never write.
	for _, q := range []string{
		`UPDATE sites SET redirect_status = 303 WHERE name = 'old'`,
		`UPDATE sites SET redirect_target = 'ftp://x' WHERE name = 'old'`,
		`UPDATE sites SET account = 2 WHERE name = 'old'`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err == nil {
			t.Errorf("%s accepted", q)
		}
	}
}
