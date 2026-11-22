// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestSiteTypeFollowsPHP(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	php, err := s.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com"}, now)
	if err != nil || php.Type != SiteTypePHP {
		t.Fatalf("php site: %+v %v", php, err)
	}

	static, err := s.CreateSite(ctx, "docs", "default", "", []string{"docs.example.com"}, now)
	if err != nil || static.Type != SiteTypeStatic {
		t.Fatalf("static site: %+v %v", static, err)
	}

	for _, c := range []struct{ site, php, want string }{
		{"blog", "", SiteTypeStatic}, // PHP off: static
		{"blog", "8.3", SiteTypePHP}, // back on
		{"docs", "8.4", SiteTypePHP}, // static gets PHP
		{"docs", "8.5", SiteTypePHP}, // another version stays php
		{"docs", "", SiteTypeStatic}, // and off again
	} {
		if _, err := s.SetSitePHP(ctx, c.site, c.php, now); err != nil {
			t.Fatalf("%s php %q: %v", c.site, c.php, err)
		}

		got, err := s.SiteByName(ctx, c.site)
		if err != nil || got.Type != c.want || got.PHP != c.php {
			t.Fatalf("%s php %q: type %q php %q %v, want %q", c.site, c.php, got.Type, got.PHP, err, c.want)
		}
	}
}

func TestSiteTypeRefusesPHPForOtherTypes(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	if _, err := s.CreateSite(ctx, "proxy", "default", "", []string{"proxy.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	// No code sets the other types yet; the column takes them.
	for _, typ := range []string{SiteTypeNode, SiteTypeApp, SiteTypeProxy, SiteTypeRedirect} {
		if _, err := s.w.ExecContext(ctx, `UPDATE sites SET type = ? WHERE name = 'proxy'`, typ); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}

		if _, err := s.SetSitePHP(ctx, "proxy", "8.4", now); !errors.Is(err, ErrSiteType) {
			t.Fatalf("%s: set php: %v", typ, err)
		}

		got, err := s.SiteByName(ctx, "proxy")
		if err != nil || got.Type != typ || got.PHP != "" {
			t.Fatalf("%s: changed to %q %q %v", typ, got.Type, got.PHP, err)
		}
	}

	for _, bad := range []string{"", "python", "PHP", "static "} {
		if _, err := s.w.ExecContext(ctx, `UPDATE sites SET type = ? WHERE name = 'proxy'`, bad); err == nil {
			t.Fatalf("type %q stored", bad)
		}
	}
}

func TestSiteTypeBackfill(t *testing.T) {
	ctx := context.Background()

	// A database at 0044 with one PHP and one static site.
	old := fstest.MapFS{}

	entries, err := fs.ReadDir(embedded, "migrations")
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		if e.Name() < "0045" {
			b, err := fs.ReadFile(embedded, "migrations/"+e.Name())
			if err != nil {
				t.Fatal(err)
			}

			old["migrations/"+e.Name()] = &fstest.MapFile{Data: b}
		}
	}

	db, err := sql.Open("sqlite", dsn(dbPath(t), false))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if err := migrate(ctx, db, old); err != nil {
		t.Fatal(err)
	}

	for name, php := range map[string]string{"blog": "8.4", "docs": ""} {
		if _, err := db.ExecContext(ctx, `INSERT INTO sites (name, pool, state, php, created_at, updated_at) VALUES (?, 'default', 'active', ?, 1, 1)`,
			name, php); err != nil {
			t.Fatal(err)
		}
	}

	if err := migrate(ctx, db, embedded); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]string{"blog": SiteTypePHP, "docs": SiteTypeStatic} {
		var got string
		if err := db.QueryRowContext(ctx, `SELECT type FROM sites WHERE name = ?`, name).Scan(&got); err != nil || got != want {
			t.Errorf("%s: type %q %v, want %q", name, got, err, want)
		}
	}
}

func TestSiteWebOptions(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	if _, err := s.CreateSite(ctx, "docs", "default", "", []string{"docs.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	got, err := s.SiteByName(ctx, "docs")
	if err != nil || got.WebDir != "" || got.SPA || got.AssetCache {
		t.Fatalf("defaults: %+v %v", got, err)
	}

	want := WebOptions{Dir: "dist/site", SPA: true, AssetCache: true, AssetCacheSeconds: 3600, AssetCacheExts: []string{"css", "json"}}

	old, err := s.SetSiteWebOptions(ctx, "docs", want, now)
	if err != nil || !reflect.DeepEqual(old, WebOptions{}) {
		t.Fatalf("set: %+v %v", old, err)
	}

	got, err = s.SiteByName(ctx, "docs")
	if err != nil || !reflect.DeepEqual(WebOptions{got.WebDir, got.SPA, got.AssetCache, got.AssetCacheSeconds, got.AssetCacheExts}, want) {
		t.Fatalf("got %+v %v", got, err)
	}

	// The column refuses what the panel's check refuses too.
	for _, bad := range []string{"/etc", "dist/", "../x", "a/../b", strings.Repeat("a", 256)} {
		if _, err := s.SetSiteWebOptions(ctx, "docs", WebOptions{Dir: bad}, now); err == nil {
			t.Errorf("dir %q stored", bad)
		}
	}

	for _, bad := range []WebOptions{{AssetCacheSeconds: 59}, {AssetCacheSeconds: 31536001}, {AssetCacheExts: []string{"c|s"}}, {AssetCacheExts: []string{"CSS"}}} {
		if _, err := s.SetSiteWebOptions(ctx, "docs", bad, now); err == nil {
			t.Errorf("%+v stored", bad)
		}
	}

	if _, err := s.SetSiteWebOptions(ctx, "nope", want, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	// The options survive a PHP round trip (ADR-0054 addendum 3).
	if _, err := s.SetSitePHP(ctx, "docs", "8.4", now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetSitePHP(ctx, "docs", "", now); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.SiteByName(ctx, "docs"); !reflect.DeepEqual(WebOptions{got.WebDir, got.SPA, got.AssetCache, got.AssetCacheSeconds, got.AssetCacheExts}, want) {
		t.Fatalf("after php round trip: %+v", got)
	}

	if _, err := s.w.ExecContext(ctx, `UPDATE sites SET type = 'redirect' WHERE name = 'docs'`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetSiteWebOptions(ctx, "docs", want, now); !errors.Is(err, ErrSiteType) {
		t.Fatalf("redirect: %v", err)
	}
}
