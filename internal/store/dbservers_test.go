// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"strconv"
	"testing"
	"time"
)

func sealID(id int64) ([]byte, error) { return []byte("sealed-" + strconv.FormatInt(id, 10)), nil }

func newServer(name string) NewDBServer {
	return NewDBServer{
		Name: name, Host: "db.example.net", Port: 3306, AdminUser: "hostashi_admin", TLSMode: "verify",
		Status: DBServerStatus{
			Engine: "MariaDB", Version: "11.8.6", ClientHost: "10.0.0.2", TLSActive: true,
			CheckedAt: time.Unix(2_000_000, 0),
		},
		Seal: sealID,
	}
}

func TestDBServersLocalRow(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()

	list, err := s.DBServers(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("%+v %v", list, err)
	}

	l := list[0]
	if !l.Local() || l.Name != LocalDBServer || !l.IsDefault || l.TLSMode != DBServerSocket || l.SealedPassword != nil {
		t.Fatalf("local row: %+v", l)
	}

	if err := s.DeleteDBServer(ctx, LocalDBServer); !errors.Is(err, ErrDBServerInUse) {
		t.Fatalf("delete local: %v", err)
	}

	if err := s.DeleteDBServer(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete unknown: %v", err)
	}
}

func TestDBServerLifecycle(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	d, err := s.CreateDBServer(ctx, newServer("db1"), now)
	if err != nil {
		t.Fatal(err)
	}

	if d.ID == 1 || d.IsDefault || string(d.SealedPassword) != "sealed-"+strconv.FormatInt(d.ID, 10) ||
		d.Status.Version != "11.8.6" || !d.Status.TLSActive || d.Status.CheckedAt.Unix() != 2_000_000 {
		t.Fatalf("created: %+v", d)
	}

	if _, err := s.CreateDBServer(ctx, newServer("db1"), now); !errors.Is(err, ErrDBServerExists) {
		t.Fatalf("duplicate: %v", err)
	}

	if _, err := s.CreateDBServer(ctx, newServer(LocalDBServer), now); !errors.Is(err, ErrDBServerExists) {
		t.Fatalf("localhost name: %v", err)
	}

	bad := newServer("db2")
	bad.Seal = func(int64) ([]byte, error) { return nil, errors.New("no key") }

	if _, err := s.CreateDBServer(ctx, bad, now); err == nil {
		t.Fatal("seal failure ignored")
	}

	if _, err := s.DBServerByName(ctx, "db2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row kept after a failed seal: %v", err)
	}

	sock := newServer("db3")
	sock.TLSMode = DBServerSocket

	if _, err := s.CreateDBServer(ctx, sock, now); err == nil {
		t.Fatal("socket mode accepted for an external server")
	}

	// Default moves, and exactly one stays.
	if err := s.SetDefaultDBServer(ctx, "db1"); err != nil {
		t.Fatal(err)
	}

	if def, err := s.DefaultDBServer(ctx); err != nil || def.Name != "db1" {
		t.Fatalf("default: %+v %v", def, err)
	}

	if err := s.SetDefaultDBServer(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown default: %v", err)
	}

	if def, _ := s.DefaultDBServer(ctx); def.Name != "db1" {
		t.Fatalf("failed change moved the default to %q", def.Name)
	}

	// Status.
	st := DBServerStatus{LastError: "unreachable", CheckedAt: now.Add(time.Minute)}
	if err := s.SetDBServerStatus(ctx, "db1", st); err != nil {
		t.Fatal(err)
	}

	got, err := s.DBServerByName(ctx, "db1")
	if err != nil || got.Status.LastError != "unreachable" || got.Status.Version != "" || got.Status.TLSActive {
		t.Fatalf("status: %+v %v", got, err)
	}

	if err := s.SetDBServerStatus(ctx, "nope", st); !errors.Is(err, ErrNotFound) {
		t.Fatalf("status unknown: %v", err)
	}

	// A database pins the server.
	if _, err := s.CreateSite(ctx, "shop", "default", "", []string{"shop.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	db, err := s.CreateDatabaseOn(ctx, "shop", "shop_wp", "db1", now)
	if err != nil || db.Server != "db1" {
		t.Fatalf("%+v %v", db, err)
	}

	if _, err := s.CreateDatabaseOn(ctx, "shop", "shop_x", "nope", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown server: %v", err)
	}

	if l, err := s.DatabaseByName(ctx, "shop_wp"); err != nil || l.Server != "db1" {
		t.Fatalf("by name: %+v %v", l, err)
	}

	if local, err := s.CreateDatabase(ctx, "shop", "shop_local", now); err != nil || local.Server != LocalDBServer {
		t.Fatalf("local: %+v %v", local, err)
	}

	list, err := s.DBServers(ctx)
	if err != nil || len(list) != 2 || list[0].Name != LocalDBServer || list[1].Databases != 1 || list[0].Databases != 1 {
		t.Fatalf("list: %+v %v", list, err)
	}

	if err := s.DeleteDBServer(ctx, "db1"); !errors.Is(err, ErrDBServerInUse) {
		t.Fatalf("delete in use: %v", err)
	}

	if err := s.RawExec(ctx, `UPDATE databases SET server_id = 1 WHERE name = 'shop_wp'`); err == nil {
		t.Fatal("a database moved to another server")
	}

	if err := s.RawExec(ctx, `INSERT INTO databases (name, site_id, server_id, created_at) VALUES ('x', 1, 99, 0)`); err == nil {
		t.Fatal("database on an unknown server")
	}

	if err := s.DeleteDatabase(ctx, "shop_wp"); err != nil {
		t.Fatal(err)
	}

	// Deleting the default hands it back to localhost.
	if err := s.DeleteDBServer(ctx, "db1"); err != nil {
		t.Fatal(err)
	}

	if def, err := s.DefaultDBServer(ctx); err != nil || def.Name != LocalDBServer {
		t.Fatalf("default after delete: %+v %v", def, err)
	}
}
