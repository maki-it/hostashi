// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"reflect"
	"testing"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

func TestDatabaseLifecycle(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	d, pw, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_wp", User: "shop_wp"})
	if err != nil {
		t.Fatal(err)
	}

	if len(pw) != 32 || !reflect.DeepEqual(d.Users, []string{"shop_wp"}) || ag.dbs["shop_wp"] != "shop" || ag.dbUsers["shop_wp"] != "shop_wp" {
		t.Fatalf("db %+v pw %q agent %v %v", d, pw, ag.dbs, ag.dbUsers)
	}

	// A given password is used and not echoed.
	pw, err = s.CreateDatabaseUser(ctx, 1, "shop_wp", "reader", "Given-pass_123456")
	if err != nil || pw != "" {
		t.Fatalf("%q %v", pw, err)
	}

	if pw, err := s.SetDatabaseUserPassword(ctx, 1, "shop_wp", "reader", ""); err != nil || len(pw) != 32 {
		t.Fatalf("generated reset: %q %v", pw, err)
	}

	// A user is addressed through its own database only.
	if _, err := s.SetDatabaseUserPassword(ctx, 1, "other", "reader", ""); !errors.Is(err, ErrDatabaseNotFound) {
		t.Fatalf("wrong database: %v", err)
	}

	if err := s.DeleteDatabaseUser(ctx, 1, "shop_wp", "reader"); err != nil {
		t.Fatal(err)
	}

	list, err := s.Databases(ctx, "shop")
	if err != nil || len(list) != 1 || !reflect.DeepEqual(list[0].Users, []string{"shop_wp"}) {
		t.Fatalf("list %+v %v", list, err)
	}

	if _, err := s.Databases(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	if err := s.DeleteDatabase(ctx, 1, "shop_wp"); err != nil {
		t.Fatal(err)
	}

	if len(ag.dbs) != 0 || len(ag.dbUsers) != 0 {
		t.Fatalf("agent still has %v %v", ag.dbs, ag.dbUsers)
	}

	if err := s.DeleteDatabase(ctx, 1, "shop_wp"); !errors.Is(err, ErrDatabaseNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestDatabaseInputIsCheckedFirst(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	bad := []NewDatabase{
		{Site: "shop", Name: "mysql"},
		{Site: "shop", Name: "Shop"},
		{Site: "shop", Name: "a-b"},
		{Site: "shop", Name: "ok1", Password: "Given-pass_123456"},
		{Site: "shop", Name: "ok1", User: "root"},
		{Site: "shop", Name: "ok1", User: "u1", Password: "short"},
		{Site: "shop", Name: "ok1", User: "u1", Password: "has space 1234567"},
		{Site: "Shop", Name: "ok1"},
	}
	for _, req := range bad {
		if _, _, err := s.CreateDatabase(ctx, 1, req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", req, err)
		}
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "blog", Name: "blog"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	if len(ag.dbs) != 0 {
		t.Fatalf("agent called: %v", ag.dbs)
	}
}

func TestDatabaseFailures(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	// The agent refuses (the name exists on the server): no row is left.
	ag.dbs["legacy"] = "other"

	_, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "legacy"})
	if !isCode(err, agentrpc.CodeConflict) {
		t.Fatalf("got %v", err)
	}

	if list, _ := st.Databases(ctx, ""); len(list) != 0 {
		t.Fatalf("row left: %+v", list)
	}

	// Duplicate names in the panel never reach the agent.
	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_wp", User: "wp1"}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_wp"}); !errors.Is(err, ErrDatabaseExists) {
		t.Fatalf("duplicate database: %v", err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop2", User: "wp1"}); !errors.Is(err, ErrDatabaseExists) {
		t.Fatalf("duplicate user: %v", err)
	}

	if _, ok := ag.dbs["shop2"]; ok {
		t.Fatal("database created although the user name was taken")
	}

	// The user fails: the database stays, the user row goes.
	ag.userErr = &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "exists"}

	d, pw, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop3", User: "wp3"})
	if err == nil || pw != "" || d.Name != "shop3" {
		t.Fatalf("%+v %q %v", d, pw, err)
	}

	if _, err := st.DatabaseOfUser(ctx, "wp3"); err == nil {
		t.Fatal("user row left after the agent refused")
	}

	ag.userErr = nil

	// The agent is down: the delete fails and can be repeated.
	ag.dbErr = errDown
	if err := s.DeleteDatabase(ctx, 1, "shop3"); !errors.Is(err, errDown) {
		t.Fatalf("got %v", err)
	}

	ag.dbErr = nil

	if err := s.DeleteDatabase(ctx, 1, "shop3"); err != nil {
		t.Fatal(err)
	}
}

func TestSiteDeleteDropsItsDatabases(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	for _, n := range []string{"shop", "blog"} {
		if _, err := s.Create(ctx, 1, CreateRequest{Name: n, Domains: []string{n + ".example.com"}}); err != nil {
			t.Fatal(err)
		}
	}

	for _, d := range []NewDatabase{{Site: "shop", Name: "shop_a", User: "ua"}, {Site: "shop", Name: "shop_b"}, {Site: "blog", Name: "blog"}} {
		if _, _, err := s.CreateDatabase(ctx, 1, d); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Delete(ctx, 1, "shop"); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(ag.dbs, map[string]string{"blog": "blog"}) || len(ag.dbUsers) != 0 {
		t.Fatalf("agent %v %v", ag.dbs, ag.dbUsers)
	}

	if list, _ := st.Databases(ctx, ""); len(list) != 1 || list[0].Name != "blog" {
		t.Fatalf("rows %+v", list)
	}
}
