// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/store"
)

// fakeDBServers is one external server in memory.
type fakeDBServers struct {
	mu       sync.Mutex
	checkErr error
	opErr    error
	checks   []bool // privileges flag of each Check
	dbs      map[string]bool
	users    map[string]string // user -> database
	sealed   map[int64]string
}

func newFakeDBServers() *fakeDBServers {
	return &fakeDBServers{dbs: map[string]bool{}, users: map[string]string{}, sealed: map[int64]string{}}
}

func (f *fakeDBServers) Check(_ context.Context, cfg dbserver.Config, privileges bool) (dbserver.Info, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.checks = append(f.checks, privileges)

	if f.checkErr != nil {
		return dbserver.Info{}, f.checkErr
	}

	if cfg.AdminPassword == "" {
		return dbserver.Info{}, errors.New("no password")
	}

	return dbserver.Info{Engine: dbserver.EngineMySQL, Version: "8.4.13", ClientHost: "10.0.0.2", TLS: cfg.TLS != dbserver.TLSNone}, nil
}

func (f *fakeDBServers) Seal(pw string, id int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.sealed[id] = pw

	return []byte("sealed:" + strconv.FormatInt(id, 10)), nil
}

func (f *fakeDBServers) Config(srv store.DBServer) (dbserver.Config, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if string(srv.SealedPassword) != "sealed:"+strconv.FormatInt(srv.ID, 10) {
		return dbserver.Config{}, errors.New("wrong seal")
	}

	return dbserver.Config{
		Host: srv.Host, Port: srv.Port, AdminUser: srv.AdminUser, AdminPassword: f.sealed[srv.ID],
		TLS: dbserver.TLSMode(srv.TLSMode),
	}, nil
}

func (f *fakeDBServers) With(_ context.Context, srv store.DBServer, fn func(RemoteDB) error) error {
	if _, err := f.Config(srv); err != nil {
		return err
	}

	return fn(fakeRemote{f})
}

type fakeRemote struct{ f *fakeDBServers }

func (r fakeRemote) CreateDatabase(_ context.Context, name string) error {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	if r.f.opErr != nil {
		return r.f.opErr
	}

	if r.f.dbs[name] {
		return dbserver.ErrExists
	}

	r.f.dbs[name] = true

	return nil
}

func (r fakeRemote) DropDatabase(_ context.Context, name string) error {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	if r.f.opErr != nil {
		return r.f.opErr
	}

	delete(r.f.dbs, name)

	return nil
}

func (r fakeRemote) CreateUser(_ context.Context, database, user, _ string) error {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	if r.f.opErr != nil {
		return r.f.opErr
	}

	if _, ok := r.f.users[user]; ok {
		return dbserver.ErrExists
	}

	r.f.users[user] = database

	return nil
}

func (r fakeRemote) DropUser(_ context.Context, user string) error {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	if r.f.opErr != nil {
		return r.f.opErr
	}

	delete(r.f.users, user)

	return nil
}

func (r fakeRemote) SetPassword(_ context.Context, user, _ string) error {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	if _, ok := r.f.users[user]; !ok {
		return dbserver.ErrNotFound
	}

	return r.f.opErr
}

func addServer(t *testing.T, s *Service, name string) store.DBServer {
	t.Helper()

	d, err := s.AddDBServer(t.Context(), 1, NewDBServer{
		Name: name, Host: "db.example.net", AdminUser: "hostashi_admin", AdminPassword: "Admin-pw-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	return d
}

func TestDBServersOffWithoutEnable(t *testing.T) {
	s, _, _ := newService(t)

	_, err := s.AddDBServer(t.Context(), 1, NewDBServer{Name: "db1", Host: "db.example.net", AdminUser: "a", AdminPassword: "b"})
	if !errors.Is(err, ErrDBServersOff) {
		t.Fatalf("got %v", err)
	}
}

func TestAddDBServer(t *testing.T) {
	s, _, st := newService(t)
	f := newFakeDBServers()
	s.EnableDBServers(f)

	ctx := t.Context()

	d := addServer(t, s, "db1")
	if d.Port != dbserver.DefaultPort || d.TLSMode != string(dbserver.TLSVerify) || d.Status.Version != "8.4.13" ||
		d.Status.ClientHost != "10.0.0.2" || !d.Status.TLSActive || d.IsDefault || f.sealed[d.ID] != "Admin-pw-1" {
		t.Fatalf("server %+v, sealed %v", d, f.sealed)
	}

	if !reflect.DeepEqual(f.checks, []bool{true}) {
		t.Fatalf("checks %v: adding must test the privileges", f.checks)
	}

	if _, err := s.AddDBServer(ctx, 1, NewDBServer{Name: "db1", Host: "x.example.net", AdminUser: "a", AdminPassword: "b"}); !errors.Is(err, ErrDBServerExists) {
		t.Fatalf("duplicate: %v", err)
	}

	bad := map[string]NewDBServer{
		"localhost":   {Name: "localhost", Host: "x.example.net", AdminUser: "a", AdminPassword: "b"},
		"upper name":  {Name: "DB", Host: "x.example.net", AdminUser: "a", AdminPassword: "b"},
		"long name":   {Name: "a123456789012345678901234567890123", Host: "x.example.net", AdminUser: "a", AdminPassword: "b"},
		"bad host":    {Name: "db2", Host: "x y", AdminUser: "a", AdminPassword: "b"},
		"no password": {Name: "db2", Host: "x.example.net", AdminUser: "a"},
		"bad tls":     {Name: "db2", Host: "x.example.net", AdminUser: "a", AdminPassword: "b", TLS: "maybe"},
	}

	for name, req := range bad {
		if _, err := s.AddDBServer(ctx, 1, req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	f.checkErr = dbserver.ErrAccess

	_, err := s.AddDBServer(ctx, 1, NewDBServer{Name: "db2", Host: "x.example.net", AdminUser: "a", AdminPassword: "b"})
	if !errors.Is(err, ErrDBServerCheck) || !errors.Is(err, dbserver.ErrAccess) {
		t.Fatalf("failed check: %v", err)
	}

	if _, err := st.DBServerByName(ctx, "db2"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("a server that failed the check was stored: %v", err)
	}

	// A later check records the failure and keeps what the server was.
	got, err := s.CheckDBServer(ctx, "db1")
	if err != nil || got.Status.LastError == "" || got.Status.Version != "8.4.13" || got.Status.TLSActive {
		t.Fatalf("check: %+v %v", got.Status, err)
	}

	if f.checks[len(f.checks)-1] {
		t.Fatal("a periodic check must not create probe users")
	}

	f.checkErr = nil

	if got, err := s.CheckDBServer(ctx, "db1"); err != nil || got.Status.LastError != "" {
		t.Fatalf("check ok: %+v %v", got.Status, err)
	}

	if _, err := s.CheckDBServer(ctx, "nope"); !errors.Is(err, ErrDBServerNotFound) {
		t.Fatalf("check unknown: %v", err)
	}
}

func TestDatabasesOnExternalServer(t *testing.T) {
	s, ag, st := newService(t)
	f := newFakeDBServers()
	s.EnableDBServers(f)

	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	addServer(t, s, "db1")

	d, pw, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_wp", User: "shop_wp", Server: "db1"})
	if err != nil || d.Server != "db1" || len(pw) != 32 {
		t.Fatalf("%+v %q %v", d, pw, err)
	}

	if !f.dbs["shop_wp"] || f.users["shop_wp"] != "shop_wp" || len(ag.dbs) != 0 || len(ag.dbUsers) != 0 {
		t.Fatalf("remote %v %v, agent %v %v", f.dbs, f.users, ag.dbs, ag.dbUsers)
	}

	// Without a server the default is used: localhost, then db1.
	if d, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_local"}); err != nil || d.Server != "localhost" || ag.dbs["shop_local"] != "shop" {
		t.Fatalf("default local: %+v %v", d, err)
	}

	if err := s.SetDefaultDBServer(ctx, 1, "db1"); err != nil {
		t.Fatal(err)
	}

	if d, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_two"}); err != nil || d.Server != "db1" || !f.dbs["shop_two"] {
		t.Fatalf("default db1: %+v %v", d, err)
	}

	if err := s.SetDefaultDBServer(ctx, 1, "nope"); !errors.Is(err, ErrDBServerNotFound) {
		t.Fatalf("unknown default: %v", err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_x", Server: "nope"}); !errors.Is(err, ErrDBServerNotFound) {
		t.Fatalf("unknown server: %v", err)
	}

	// An existing database on the server is never adopted, and the row goes.
	f.dbs["shop_taken"] = true

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_taken", Server: "db1"}); !errors.Is(err, ErrDatabaseExists) {
		t.Fatalf("taken: %v", err)
	}

	if _, err := st.DatabaseByName(ctx, "shop_taken"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("row kept: %v", err)
	}

	// Users and passwords go to the database's server.
	if _, err := s.CreateDatabaseUser(ctx, 1, "shop_wp", "reader", ""); err != nil || f.users["reader"] != "shop_wp" {
		t.Fatalf("user: %v %v", err, f.users)
	}

	if _, err := s.SetDatabaseUserPassword(ctx, 1, "shop_wp", "reader", ""); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteDatabaseUser(ctx, 1, "shop_wp", "reader"); err != nil || f.users["reader"] != "" {
		t.Fatalf("drop user: %v %v", err, f.users)
	}

	// A server with databases cannot go.
	if err := s.DeleteDBServer(ctx, 1, "db1"); !errors.Is(err, ErrDBServerInUse) {
		t.Fatalf("delete in use: %v", err)
	}

	if err := s.DeleteDBServer(ctx, 1, "localhost"); !errors.Is(err, ErrDBServerInUse) {
		t.Fatalf("delete local: %v", err)
	}

	// An unreachable server keeps the row, so nothing is forgotten.
	f.opErr = dbserver.ErrUnavailable

	if err := s.DeleteDatabase(ctx, 1, "shop_wp"); !errors.Is(err, dbserver.ErrUnavailable) {
		t.Fatalf("delete unreachable: %v", err)
	}

	if _, err := st.DatabaseByName(ctx, "shop_wp"); err != nil {
		t.Fatalf("row lost: %v", err)
	}

	f.opErr = nil

	// Deleting the site drops its databases on both servers.
	if err := s.Delete(ctx, 1, "shop"); err != nil {
		t.Fatal(err)
	}

	if len(f.dbs) != 1 || !f.dbs["shop_taken"] || len(f.users) != 0 || len(ag.dbs) != 0 {
		t.Fatalf("after site delete: remote %v %v, agent %v", f.dbs, f.users, ag.dbs)
	}

	if err := s.DeleteDBServer(ctx, 1, "db1"); err != nil {
		t.Fatal(err)
	}

	if def, err := st.DefaultDBServer(ctx); err != nil || def.Name != "localhost" {
		t.Fatalf("default after delete: %+v %v", def, err)
	}
}
