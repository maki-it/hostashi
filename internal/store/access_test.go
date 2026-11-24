// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

const testBcrypt = "$2a$05$7tXviV9DiqwPre8Yge0sUu000CCwS9nkgKr.DLJbkteSq.2Ar/Hv2"

func TestSiteAccess(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if a, err := s.SiteAccess(ctx, site.ID); err != nil || !reflect.DeepEqual(a.Settings, AccessSettings{}) || !a.UpdatedAt.IsZero() {
		t.Fatalf("none: %+v %v", a, err)
	}

	want := AccessSettings{IPMode: "deny", IPs: []string{"203.0.113.7"}, Password: true, Paths: []string{"/wp-admin"}, Bypass: []string{"198.51.100.0/24"}}

	if _, err := s.SetSiteAccess(ctx, SiteAccess{SiteID: site.ID, Settings: want}, now); err != nil {
		t.Fatal(err)
	}

	all, err := s.AllSiteAccess(ctx)
	if err != nil || !reflect.DeepEqual(all[site.ID].Settings, want) || !all[site.ID].UpdatedAt.Equal(now) {
		t.Fatalf("all: %+v %v", all, err)
	}

	old, err := s.SetSiteAccess(ctx, SiteAccess{SiteID: site.ID}, now)
	if err != nil || !reflect.DeepEqual(old.Settings, want) {
		t.Fatalf("clear: %+v %v", old, err)
	}

	if all, _ := s.AllSiteAccess(ctx); len(all) != 0 {
		t.Errorf("row left: %+v", all)
	}
}

func TestAuthUsers(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	site, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	for _, n := range []string{"zed", "kim"} {
		if err := s.AddAuthUser(ctx, AuthUser{SiteID: site.ID, Name: n, Hash: testBcrypt}, now); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.AddAuthUser(ctx, AuthUser{SiteID: site.ID, Name: "kim", Hash: testBcrypt}, now); !errors.Is(err, ErrAuthUserExists) {
		t.Errorf("twice: %v", err)
	}

	// The schema refuses what would break the password file.
	for _, u := range []AuthUser{{Name: "a:b", Hash: testBcrypt}, {Name: "a\nb", Hash: testBcrypt}, {Name: "ok", Hash: "plain"}, {Name: "ok", Hash: strings.Repeat("x", 60)}} {
		u.SiteID = site.ID
		if err := s.AddAuthUser(ctx, u, now); err == nil {
			t.Errorf("stored %+v", u)
		}
	}

	us, err := s.AuthUsers(ctx, site.ID)
	if err != nil || len(us) != 2 || us[0].Name != "kim" || !us[0].CreatedAt.Equal(now) {
		t.Fatalf("list: %+v %v", us, err)
	}

	later := now.Add(time.Hour)
	other := "$2a$05$" + strings.Repeat("a", 53)

	old, err := s.SetAuthUserHash(ctx, site.ID, "kim", other, 0, later)
	if err != nil || old.Hash != testBcrypt {
		t.Fatalf("set: %+v %v", old, err)
	}

	if err := s.RestoreAuthUser(ctx, old); err != nil {
		t.Fatal(err)
	}

	all, err := s.AllAuthUsers(ctx)
	if err != nil || all[site.ID][0].Hash != testBcrypt || !all[site.ID][0].UpdatedAt.Equal(now) {
		t.Fatalf("restore: %+v %v", all, err)
	}

	if _, err := s.SetAuthUserHash(ctx, site.ID, "nobody", other, 0, later); !errors.Is(err, ErrNotFound) {
		t.Errorf("set unknown: %v", err)
	}

	if u, err := s.DeleteAuthUser(ctx, site.ID, "zed"); err != nil || u.Name != "zed" {
		t.Fatalf("delete: %+v %v", u, err)
	}

	if _, err := s.DeleteAuthUser(ctx, site.ID, "zed"); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete twice: %v", err)
	}
}
