// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/extdb"
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

	if dbserver.IsPostgres(cfg.Engine) {
		return dbserver.Info{
			Engine: dbserver.EnginePostgreSQL, Version: "18.1", ClientHost: "10.0.0.2", TLS: cfg.TLS != dbserver.TLSNone,
			Superuser: cfg.AdminUser == "postgres",
		}, nil
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
		Engine: engineOf(srv), Host: srv.Host, Port: srv.Port, AdminUser: srv.AdminUser, AdminPassword: f.sealed[srv.ID],
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

func (r fakeRemote) DropUser(_ context.Context, _, user string) error {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	if r.f.opErr != nil {
		return r.f.opErr
	}

	delete(r.f.users, user)

	return nil
}

func (r fakeRemote) CreateJobUser(ctx context.Context, database, user, password string) error {
	return r.CreateUser(ctx, database, user, password)
}

func (r fakeRemote) DropJobUser(ctx context.Context, user string) error {
	return r.DropUser(ctx, "", user)
}

func (r fakeRemote) CreateToolUser(ctx context.Context, database, user, password string) error {
	return r.CreateUser(ctx, database, user, password)
}

func (r fakeRemote) DropToolUser(ctx context.Context, database, user string) error {
	return r.DropUser(ctx, database, user)
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

	d, _, err := s.AddDBServer(t.Context(), 1, NewDBServer{
		Name: name, Host: "db.example.net", AdminUser: "hostashi_admin", AdminPassword: "Admin-pw-1",
	})
	if err != nil {
		t.Fatal(err)
	}

	return d
}

func TestDBServersOffWithoutEnable(t *testing.T) {
	s, _, _ := newService(t)

	_, _, err := s.AddDBServer(t.Context(), 1, NewDBServer{Name: "db1", Host: "db.example.net", AdminUser: "a", AdminPassword: "b"})
	if !errors.Is(err, ErrDBServersOff) {
		t.Fatalf("got %v", err)
	}
}

func TestAddDBServer(t *testing.T) {
	s, _, st := newService(t)
	f := newFakeDBServers()
	s.EnableDBServers(f, "")

	ctx := t.Context()

	d := addServer(t, s, "db1")
	if d.Port != dbserver.DefaultPort || d.TLSMode != string(dbserver.TLSVerify) || d.Status.Version != "8.4.13" ||
		d.Status.ClientHost != "10.0.0.2" || !d.Status.TLSActive || d.IsDefault || f.sealed[d.ID] != "Admin-pw-1" {
		t.Fatalf("server %+v, sealed %v", d, f.sealed)
	}

	if !reflect.DeepEqual(f.checks, []bool{true}) {
		t.Fatalf("checks %v: adding must test the privileges", f.checks)
	}

	if _, _, err := s.AddDBServer(ctx, 1, NewDBServer{Name: "db1", Host: "x.example.net", AdminUser: "a", AdminPassword: "b"}); !errors.Is(err, ErrDBServerExists) {
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
		if _, _, err := s.AddDBServer(ctx, 1, req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	f.checkErr = dbserver.ErrAccess

	_, _, err := s.AddDBServer(ctx, 1, NewDBServer{Name: "db2", Host: "x.example.net", AdminUser: "a", AdminPassword: "b"})
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

// fakePGClient is the agent's client tool installs.
type fakePGClient struct {
	status   agentapi.PGClientStatus
	installs int
	err      error
}

func (f *fakePGClient) Status(context.Context) (agentapi.PGClientStatus, error) { return f.status, nil }

func (f *fakePGClient) Install(context.Context, string) (agentapi.PGClientStatus, error) {
	f.installs++
	if f.err != nil {
		return agentapi.PGClientStatus{}, f.err
	}

	f.status.State = agentapi.PHPInstallRunning

	return f.status, nil
}

func TestAddPostgresServerInstallsClient(t *testing.T) {
	s, _, _ := newService(t)
	f := newFakeDBServers()
	s.EnableDBServers(f, "")

	pg := &fakePGClient{status: agentapi.PGClientStatus{State: agentapi.PHPInstallIdle}}
	s.EnablePGClient(pg)

	ctx := t.Context()
	add := func(name string) ([]string, error) {
		_, w, err := s.AddDBServer(ctx, 1, NewDBServer{
			Engine: dbserver.EnginePostgreSQL, Name: name, Host: "pg.example.net", AdminUser: "hostashi_admin", AdminPassword: "Admin-pw-1",
		})

		return w, err
	}

	// No client yet: the first server starts the install and warns.
	if w, err := add("pg1"); err != nil || pg.installs != 1 || len(w) != 1 || w[0] != PGClientInstallWarning {
		t.Fatalf("first: %v %v, installs %d", w, err, pg.installs)
	}

	// While it runs, no second install.
	if w, err := add("pg2"); err != nil || pg.installs != 1 || len(w) != 1 {
		t.Fatalf("running: %v %v, installs %d", w, err, pg.installs)
	}

	// A client as new as the server (18.1) is enough.
	pg.status = agentapi.PGClientStatus{Installed: []string{"18"}, State: agentapi.PHPInstallSucceeded}
	if w, err := add("pg3"); err != nil || pg.installs != 1 || len(w) != 0 {
		t.Fatalf("installed: %v %v", w, err)
	}

	// An older client starts the newest; a race with another install is fine.
	pg.status = agentapi.PGClientStatus{Installed: []string{"16"}, State: agentapi.PHPInstallSucceeded}
	pg.err = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "busy"}

	if w, err := add("pg4"); err != nil || pg.installs != 2 || len(w) != 1 {
		t.Fatalf("older client: %v %v", w, err)
	}

	pg.err = errors.New("agent gone")
	if _, err := add("pg5"); err == nil {
		t.Fatal("agent error ignored")
	}
}

func TestPGClientTooOld(t *testing.T) {
	s, _, _ := newService(t)

	if _, err := s.checkPGClient(t.Context(), "99.1"); !errors.Is(err, ErrPGClientTooOld) {
		t.Fatalf("got %v", err)
	}

	if w, err := s.checkPGClient(t.Context(), agentapi.PGClientNewest()+".0"); err != nil || w != "" {
		t.Fatalf("newest without installer: %q %v", w, err)
	}
}

func TestAddPostgresServer(t *testing.T) {
	s, _, st := newService(t)
	f := newFakeDBServers()
	manifest := filepath.Join(t.TempDir(), "backup")

	if err := os.Mkdir(manifest, 0o700); err != nil {
		t.Fatal(err)
	}

	s.EnableDBServers(f, manifest)

	ctx := t.Context()

	d, warnings, err := s.AddDBServer(ctx, 1, NewDBServer{
		Engine: dbserver.EnginePostgreSQL, Name: "pg1", Host: "pg.example.net", AdminUser: "hostashi_admin", AdminPassword: "Admin-pw-1",
	})
	if err != nil || d.Port != dbserver.DefaultPostgresPort || d.Status.Engine != dbserver.EnginePostgreSQL || len(warnings) != 0 || !IsPostgres(d) {
		t.Fatalf("server %+v, warnings %v, %v", d, warnings, err)
	}

	cfg, err := f.Config(d)
	if err != nil || cfg.Engine != dbserver.EnginePostgreSQL {
		t.Fatalf("config %+v %v", cfg, err)
	}

	// A superuser works but is warned about.
	_, warnings, err = s.AddDBServer(ctx, 1, NewDBServer{
		Engine: dbserver.EnginePostgreSQL, Name: "pg2", Host: "pg.example.net", AdminUser: "postgres", AdminPassword: "Admin-pw-1",
	})
	if err != nil || len(warnings) != 1 || warnings[0] != SuperuserWarning {
		t.Fatalf("superuser: %v %v", warnings, err)
	}

	if _, _, err := s.AddDBServer(ctx, 1, NewDBServer{
		Engine: "Oracle", Name: "pg3", Host: "pg.example.net", AdminUser: "a", AdminPassword: "b",
	}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown engine: %v", err)
	}

	m, err := extdb.Read(manifest, uint32(os.Getuid()))
	if err != nil || len(m.Servers) != 2 || m.Servers[0].Engine != dbserver.EnginePostgreSQL {
		t.Fatalf("manifest %+v %v", m, err)
	}

	// Import, export and the web database tool refuse PostgreSQL for now.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_pg", User: "shop_pg", Server: "pg1"}); err != nil {
		t.Fatal(err)
	}

	db, err := st.DatabaseByName(ctx, "shop_pg")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.ExportDatabase(ctx, 1, "shop_pg", "dumps/x.sql"); !errors.Is(err, ErrPostgreSQLNotYet) {
		t.Fatalf("export: %v", err)
	}

	if _, err := s.openRemoteTool(ctx, db, 30); !errors.Is(err, ErrPostgreSQLNotYet) {
		t.Fatalf("tool: %v", err)
	}

	// Errors of the client map to the panel's.
	f.opErr = dbserver.ErrPostgresNotYet

	if err := s.remote(ctx, d, func(c RemoteDB) error { return c.CreateDatabase(ctx, "x") }); !errors.Is(err, ErrPostgreSQLNotYet) {
		t.Fatalf("not yet: %v", err)
	}

	f.opErr = dbserver.ErrInvalid

	if err := s.remote(ctx, d, func(c RemoteDB) error { return c.CreateDatabase(ctx, "x") }); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid: %v", err)
	}
}

func TestDatabasesOnExternalServer(t *testing.T) {
	s, ag, st := newService(t)
	f := newFakeDBServers()
	manifest := filepath.Join(t.TempDir(), "backup")

	if err := os.Mkdir(manifest, 0o700); err != nil {
		t.Fatal(err)
	}

	s.EnableDBServers(f, manifest)

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

	// The backup's list has the external databases only.
	m, err := extdb.Read(manifest, uint32(os.Getuid()))
	if err != nil || len(m.Servers) != 1 || m.Servers[0].Name != "db1" || string(m.Servers[0].SealedPassword) == "" ||
		len(m.Databases) != 2 || m.Databases[0].Name != "shop_two" || m.Databases[1].Name != "shop_wp" {
		t.Fatalf("manifest %+v %v", m, err)
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

	// Loading a dump as the owner works only on the local server.
	if _, err := s.LoadDatabaseDump(ctx, 1, "shop_wp", "dumps/old.sql"); !errors.Is(err, ErrDBServerLocalOnly) {
		t.Fatalf("load: %v", err)
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

	if m, err := extdb.Read(manifest, uint32(os.Getuid())); err != nil || len(m.Servers) != 0 || len(m.Databases) != 0 {
		t.Fatalf("manifest after delete %+v %v", m, err)
	}
}

func TestDBServerDoctorChecks(t *testing.T) {
	now := time.Date(2026, 11, 21, 3, 0, 0, 0, time.UTC)
	ok := store.DBServerStatus{Engine: "MariaDB", Version: "11.8.6", CheckedAt: now.Add(-time.Minute)}

	cases := []struct {
		name string
		srv  store.DBServer
		want map[string]string // id -> status
	}{
		{
			"fine",
			store.DBServer{ID: 2, Name: "db1", TLSMode: "verify", Status: withCert(ok, now.Add(60*24*time.Hour))},
			map[string]string{"dbserver.reachable": "ok"},
		},
		{
			"unchecked",
			store.DBServer{ID: 2, Name: "db1", TLSMode: "verify"},
			map[string]string{"dbserver.reachable": "warn"},
		},
		{
			"down",
			store.DBServer{ID: 2, Name: "db1", TLSMode: "verify", Status: withErr(ok, "dbserver: server not reachable (probe)")},
			map[string]string{"dbserver.reachable": "fail"},
		},
		{
			"plain",
			store.DBServer{ID: 2, Name: "db1", TLSMode: "none", Status: ok},
			map[string]string{"dbserver.reachable": "ok", "dbserver.tls": "warn"},
		},
		{
			"cert soon",
			store.DBServer{ID: 2, Name: "db1", TLSMode: "verify", Status: withCert(ok, now.Add(10*24*time.Hour))},
			map[string]string{"dbserver.reachable": "ok", "dbserver.cert": "warn"},
		},
		{
			"cert ending",
			store.DBServer{ID: 2, Name: "db1", TLSMode: "verify", Status: withCert(ok, now.Add(2*24*time.Hour))},
			map[string]string{"dbserver.reachable": "ok", "dbserver.cert": "fail"},
		},
		{
			"stale",
			store.DBServer{ID: 2, Name: "db1", TLSMode: "verify", Status: withChecked(ok, now.Add(-2*time.Hour))},
			map[string]string{"dbserver.reachable": "ok", "dbserver.checked": "warn"},
		},
	}

	for _, c := range cases {
		got := map[string]string{}
		for _, ch := range dbServerChecks(c.srv, now) {
			got[ch.ID] = ch.Status

			if !strings.Contains(ch.Summary, "db1") {
				t.Errorf("%s: summary without the server: %q", c.name, ch.Summary)
			}
		}

		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func withCert(st store.DBServerStatus, t time.Time) store.DBServerStatus {
	st.CertExpires = t
	return st
}
func withErr(st store.DBServerStatus, e string) store.DBServerStatus { st.LastError = e; return st }
func withChecked(st store.DBServerStatus, t time.Time) store.DBServerStatus {
	st.CheckedAt = t

	return st
}

func TestRefreshDBServers(t *testing.T) {
	s, _, st := newService(t)
	f := newFakeDBServers()
	s.EnableDBServers(f, "")
	addServer(t, s, "db1")

	f.checkErr = dbserver.ErrUnavailable

	s.RefreshDBServers(t.Context())

	got, err := st.DBServerByName(t.Context(), "db1")
	if err != nil || got.Status.LastError == "" {
		t.Fatalf("%+v %v", got.Status, err)
	}

	checks, err := s.DBServerDoctor(t.Context(), time.Now())
	if err != nil || len(checks) != 1 || checks[0].Status != "fail" {
		t.Fatalf("%+v %v", checks, err)
	}
}
