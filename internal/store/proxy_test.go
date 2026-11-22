// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestProxySite(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	p := SiteProxy{Target: "https://10.0.0.5:8443/app", TargetHost: true, Insecure: true}

	created, err := s.CreateProxySite(ctx, "app", "default", p, []string{"app.example.com"}, now)
	if err != nil || created.Type != SiteTypeProxy || created.Account || !reflect.DeepEqual(created.Proxy, &p) || created.Redirect != nil {
		t.Fatalf("create: %+v %v", created, err)
	}

	got, err := s.SiteByName(ctx, "app")
	if err != nil || got.Type != SiteTypeProxy || got.Account || !reflect.DeepEqual(got.Proxy, &p) || got.Redirect != nil {
		t.Fatalf("read: %+v %v", got, err)
	}

	if _, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.SiteByName(ctx, "blog"); got.Proxy != nil {
		t.Fatalf("blog has a proxy: %+v", got.Proxy)
	}

	next := SiteProxy{Target: "http://backend.example.net"}

	old, err := s.SetSiteProxy(ctx, "app", next, now)
	if err != nil || old != p {
		t.Fatalf("set: %+v %v", old, err)
	}

	if got, _ := s.SiteByName(ctx, "app"); !reflect.DeepEqual(got.Proxy, &next) {
		t.Fatalf("after set: %+v", got.Proxy)
	}

	if _, err := s.SetSiteProxy(ctx, "blog", next, now); !errors.Is(err, ErrSiteType) {
		t.Fatalf("proxy on php: %v", err)
	}

	if _, err := s.SetSiteRedirect(ctx, "app", SiteRedirect{Target: "https://x.example.com", Status: 301}, now); !errors.Is(err, ErrSiteType) {
		t.Fatalf("redirect on proxy: %v", err)
	}

	if _, err := s.SetSitePHP(ctx, "app", "8.4", now); !errors.Is(err, ErrSiteType) {
		t.Fatalf("php on proxy: %v", err)
	}

	if _, err := s.SetSiteProxy(ctx, "nope", next, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}

	for _, q := range []string{
		`UPDATE sites SET proxy_target = 'ftp://x' WHERE name = 'app'`,
		`UPDATE sites SET proxy_target_host = 2 WHERE name = 'app'`,
		`UPDATE sites SET proxy_insecure = 2 WHERE name = 'app'`,
	} {
		if _, err := s.w.ExecContext(ctx, q); err == nil {
			t.Errorf("%s accepted", q)
		}
	}
}
