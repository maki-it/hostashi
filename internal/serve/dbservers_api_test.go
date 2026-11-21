// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/store"
)

// stubDBServers accepts every server and keeps databases in memory.
type stubDBServers struct {
	mu       sync.Mutex
	checkErr error
	dbs      map[string]bool
}

func (s *stubDBServers) Check(_ context.Context, cfg dbserver.Config, _ bool) (dbserver.Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if dbserver.IsPostgres(cfg.Engine) {
		return dbserver.Info{Engine: dbserver.EnginePostgreSQL, Version: "18.1", ClientHost: "10.0.0.2", TLS: true, Superuser: true}, s.checkErr
	}

	return dbserver.Info{Engine: dbserver.EngineMariaDB, Version: "11.8.6", ClientHost: "10.0.0.2", TLS: true}, s.checkErr
}

func (*stubDBServers) Seal(string, int64) ([]byte, error) { return []byte("sealed"), nil }

func (*stubDBServers) Config(store.DBServer) (dbserver.Config, error) {
	return dbserver.Config{AdminPassword: "Top-secret-admin"}, nil
}

func (s *stubDBServers) With(_ context.Context, _ store.DBServer, fn func(hosting.RemoteDB) error) error {
	return fn(stubRemote{s})
}

type stubRemote struct{ s *stubDBServers }

func (r stubRemote) CreateDatabase(_ context.Context, name string) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()

	r.s.dbs[name] = true

	return nil
}

func (r stubRemote) DropDatabase(_ context.Context, name string) error {
	r.s.mu.Lock()
	defer r.s.mu.Unlock()

	delete(r.s.dbs, name)

	return nil
}

func (stubRemote) CreateUser(context.Context, string, string, string) error     { return nil }
func (stubRemote) DropUser(context.Context, string, string) error               { return nil }
func (stubRemote) SetPassword(context.Context, string, string) error            { return nil }
func (stubRemote) CreateJobUser(context.Context, string, string, string) error  { return nil }
func (stubRemote) DropJobUser(context.Context, string) error                    { return nil }
func (stubRemote) CreateToolUser(context.Context, string, string, string) error { return nil }
func (stubRemote) DropToolUser(context.Context, string) error                   { return nil }

func TestDBServersOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	remote := &stubDBServers{dbs: map[string]bool{}}
	e.sites.EnableDBServers(remote, "")
	c := e.adminCookie(t)

	add := `{"name":"db1","host":"db.example.net","adminUser":"hostashi_admin","adminPassword":"Top-secret-admin"}`

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/db-servers", cookie: c, body: add})
	if rec.Code != http.StatusCreated || strings.Contains(rec.Body.String(), "Top-secret") {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}

	var got dbServerJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Version != "11.8.6" || got.State != "ok" ||
		got.Host != "db.example.net" || got.Port != 3306 || got.TLSMode != "verify" || got.Default {
		t.Fatalf("%+v %v", got, err)
	}

	// A PostgreSQL server gets its own default port and a warning for a
	// superuser.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/db-servers", cookie: c, body: `{"engine":"postgresql","name":"pg1","host":"pg.example.net","adminUser":"postgres","adminPassword":"Top-secret-admin"}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add PostgreSQL: %d %s", rec.Code, rec.Body)
	}

	got = dbServerJSON{}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Engine != "PostgreSQL" || got.Port != 5432 ||
		len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], "superuser") {
		t.Fatalf("PostgreSQL: %+v %v", got, err)
	}

	cases := []struct {
		method, path, body string
		code               int
	}{
		{"POST", "/api/v1/db-servers", add, http.StatusConflict},
		{"POST", "/api/v1/db-servers", `{"name":"localhost","host":"x.example.net","adminUser":"a","adminPassword":"b"}`, http.StatusUnprocessableEntity},
		{"POST", "/api/v1/db-servers", `{"name":"db2","host":"x y","adminUser":"a","adminPassword":"b"}`, http.StatusUnprocessableEntity},
		{"POST", "/api/v1/db-servers", `{"name":"db2","host":"x.example.net","adminUser":"a","adminPassword":"b","tlsMode":"verify-ca"}`, http.StatusUnprocessableEntity},
		{"POST", "/api/v1/db-servers", `{"name":"db2","nope":1}`, http.StatusBadRequest},
		{"POST", "/api/v1/db-servers", `{"engine":"oracle","name":"db2","host":"x.example.net","adminUser":"a","adminPassword":"b"}`, http.StatusUnprocessableEntity},
		{"POST", "/api/v1/db-servers/nope/check", ``, http.StatusNotFound},
		{"PUT", "/api/v1/db-servers/nope/default", ``, http.StatusNotFound},
		{"DELETE", "/api/v1/db-servers/nope", ``, http.StatusNotFound},
		{"DELETE", "/api/v1/db-servers/localhost", ``, http.StatusConflict},
		{"POST", "/api/v1/sites/shop/databases", `{"name":"x1","server":"nope"}`, http.StatusNotFound}, // site missing first
	}
	for _, tc := range cases {
		if rec := e.do(t, reqOpts{method: tc.method, path: tc.path, cookie: c, body: tc.body}); rec.Code != tc.code {
			t.Errorf("%s %s %s: %d, want %d: %s", tc.method, tc.path, tc.body, rec.Code, tc.code, rec.Body)
		}
	}

	// A failed check names the reason and stores nothing.
	remote.mu.Lock()
	remote.checkErr = dbserver.ErrAccess
	remote.mu.Unlock()

	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/db-servers", cookie: c,
		body: `{"name":"db2","host":"x.example.net","adminUser":"a","adminPassword":"b"}`,
	})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "db_server_check_failed") ||
		!strings.Contains(rec.Body.String(), "privilege") {
		t.Fatalf("failed check: %d %s", rec.Code, rec.Body)
	}

	remote.mu.Lock()
	remote.checkErr = nil
	remote.mu.Unlock()

	// A database on db1.
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create site: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/databases", cookie: c, body: `{"name":"shop_wp","server":"db1"}`})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"server":"db1"`) || !remote.dbs["shop_wp"] {
		t.Fatalf("create on db1: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/databases", cookie: c, body: `{"name":"shop_x","server":"nope"}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("unknown server: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/db-servers/db1/default", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("default: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/db-servers/db1/check", cookie: c}); rec.Code != http.StatusOK {
		t.Fatalf("check: %d %s", rec.Code, rec.Body)
	}

	// doctor reports the external server from its last check.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/doctor", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"dbserver.reachable"`) {
		t.Fatalf("doctor: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/db-servers/db1", cookie: c}); rec.Code != http.StatusConflict {
		t.Fatalf("delete in use: %d %s", rec.Code, rec.Body)
	}

	// A user sees the servers to pick one and the host for DB_HOST, but
	// not the administrator or the last error.
	uc, _ := matrixLogin(t, e, c, "uma", "user")

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/db-servers", cookie: uc})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"db1"`) ||
		!strings.Contains(rec.Body.String(), `"host":"db.example.net"`) || strings.Contains(rec.Body.String(), "hostashi_admin") ||
		strings.Contains(rec.Body.String(), "clientHost") {
		t.Fatalf("user list: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/db-servers/db1/check", cookie: uc}); rec.Code != http.StatusForbidden {
		t.Fatalf("user check: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/db-servers", cookie: c})

	var list struct {
		Servers []dbServerJSON `json:"servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Servers) != 3 || !list.Servers[0].Local ||
		list.Servers[0].Default || !list.Servers[1].Default || list.Servers[1].Databases != 1 {
		t.Fatalf("admin list: %s %v", rec.Body, err)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/databases/shop_wp", cookie: c}); rec.Code != http.StatusNoContent || remote.dbs["shop_wp"] {
		t.Fatalf("delete database: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/db-servers/db1", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}
