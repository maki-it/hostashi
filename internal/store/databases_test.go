// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestDatabaseStore(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	if _, err := s.CreateDatabase(ctx, "shop", "shop_wp", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	for _, n := range []string{"shop", "blog"} {
		if _, err := s.CreateSite(ctx, n, "default", "", []string{n + ".example.com"}, now); err != nil {
			t.Fatal(err)
		}
	}

	d, err := s.CreateDatabase(ctx, "shop", "shop_wp", now)
	if err != nil || d.ID == 0 || d.Site != "shop" {
		t.Fatalf("%+v %v", d, err)
	}

	if _, err := s.CreateDatabase(ctx, "blog", "shop_wp", now); !errors.Is(err, ErrDatabaseExists) {
		t.Fatalf("duplicate: %v", err)
	}

	if _, err := s.CreateDatabase(ctx, "blog", "blog", now); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateDatabaseUser(ctx, "shop_wp", "wp", now); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateDatabaseUser(ctx, "shop_wp", "reader", now); err != nil {
		t.Fatal(err)
	}

	if err := s.CreateDatabaseUser(ctx, "blog", "wp", now); !errors.Is(err, ErrDatabaseUserExists) {
		t.Fatalf("duplicate user: %v", err)
	}

	if err := s.CreateDatabaseUser(ctx, "nope", "x1", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown database: %v", err)
	}

	got, err := s.DatabaseByName(ctx, "shop_wp")
	if err != nil || !reflect.DeepEqual(got.Users, []string{"reader", "wp"}) {
		t.Fatalf("%+v %v", got, err)
	}

	if db, err := s.DatabaseOfUser(ctx, "wp"); err != nil || db != "shop_wp" {
		t.Fatalf("%q %v", db, err)
	}

	all, err := s.Databases(ctx, "")
	if err != nil || len(all) != 2 || all[0].Name != "blog" {
		t.Fatalf("all: %+v %v", all, err)
	}

	mine, err := s.Databases(ctx, "shop")
	if err != nil || len(mine) != 1 || mine[0].Name != "shop_wp" {
		t.Fatalf("shop: %+v %v", mine, err)
	}

	// A site with databases cannot be removed by accident.
	if err := s.DeleteSite(ctx, "shop"); err == nil {
		t.Fatal("site with databases deleted")
	}

	if err := s.DeleteDatabaseUser(ctx, "reader"); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteDatabase(ctx, "shop_wp"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DatabaseOfUser(ctx, "wp"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("user survived its database: %v", err)
	}

	if err := s.DeleteSite(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
}
