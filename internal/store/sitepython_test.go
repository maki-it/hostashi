// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// TestMigrationSitePython rebuilds sites (0056) under a database with rows
// in the tables that reference it: none of them may go, and foreign keys
// are on again afterwards.
func TestMigrationSitePython(t *testing.T) {
	ctx := t.Context()

	db, err := sql.Open("sqlite", dsn(dbPath(t), false))
	if err != nil {
		t.Fatal(err)
	}

	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if err := migrate(ctx, db, migrationsBefore(t, "0056_")); err != nil {
		t.Fatal(err)
	}

	for _, q := range []string{
		`INSERT INTO sites (id, name, pool, state, created_at, updated_at, type, node_version, app_start_kind, app_start, app_port)
		 VALUES (7, 'shop', 'default', 'active', 1, 2, 'node', '24', 'script', 'server.js', 20001)`,
		`INSERT INTO sites (id, name, pool, state, created_at, updated_at, type, app_start_kind, app_start, app_port, app_args)
		 VALUES (8, 'prog', 'default', 'active', 1, 2, 'app', 'binary', 'bin/server', 20002, '["--x"]')`,
		`INSERT INTO site_domains (domain, site_id, position) VALUES ('shop.example.com', 7, 0), ('www.shop.example.com', 7, 1)`,
		`INSERT INTO site_app_env (site_id, name, value) VALUES (7, 'TOKEN', x'00')`,
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}

	if err := migrate(ctx, db, embedded); err != nil {
		t.Fatal(err)
	}

	count := func(q string) int {
		t.Helper()

		var n int
		if err := db.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}

		return n
	}

	if n := count(`SELECT count(*) FROM site_domains WHERE site_id = 7`); n != 2 {
		t.Errorf("domains: %d", n)
	}

	if n := count(`SELECT count(*) FROM site_app_env WHERE site_id = 7`); n != 1 {
		t.Errorf("env: %d", n)
	}

	if n := count(`PRAGMA foreign_keys`); n != 1 {
		t.Error("foreign keys still off")
	}

	var args, kind, py string
	if err := db.QueryRowContext(ctx, `SELECT app_args, app_start_kind, python_version FROM sites WHERE id = 8`).Scan(&args, &kind, &py); err != nil ||
		args != `["--x"]` || kind != "binary" || py != "" {
		t.Errorf("app site: %q %q %q %v", args, kind, py, err)
	}

	// The new type, and the checks that stay.
	if _, err := db.ExecContext(ctx, `INSERT INTO sites (name, pool, state, created_at, updated_at, type, python_version, app_start_kind, app_start, app_port, app_args)
		VALUES ('py', 'default', 'active', 1, 1, 'python', '3.13', 'wsgi', 'shop.wsgi:application', 20003, '["--workers","3"]')`); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []string{
		`INSERT INTO sites (name, pool, state, created_at, updated_at, type) VALUES ('x1', 'default', 'active', 1, 1, 'ruby')`,
		`INSERT INTO sites (name, pool, state, created_at, updated_at, python_version) VALUES ('x2', 'default', 'active', 1, 1, '2.7')`,
		`INSERT INTO sites (name, pool, state, created_at, updated_at, app_args, app_start_kind, app_start) VALUES ('x3', 'default', 'active', 1, 1, '["a"]', 'script', 'a.js')`,
		`INSERT INTO sites (name, pool, state, created_at, updated_at, app_port) VALUES ('x4', 'default', 'active', 1, 1, 20003)`,
		`INSERT INTO site_domains (domain, site_id, position) VALUES ('nope.example.com', 99, 0)`,
	} {
		if _, err := db.ExecContext(ctx, bad); err == nil {
			t.Errorf("accepted: %s", bad)
		}
	}

	// Deleting a site still cascades.
	if _, err := db.ExecContext(ctx, `DELETE FROM sites WHERE id = 7`); err != nil {
		t.Fatal(err)
	}

	if n := count(`SELECT count(*) FROM site_domains`); n != 0 {
		t.Errorf("domains after delete: %d", n)
	}
}

// TestMigrationForeignKeyCheck: a rebuild that leaves a dangling reference
// is rolled back.
func TestMigrationForeignKeyCheck(t *testing.T) {
	ctx := t.Context()

	db, err := sql.Open("sqlite", dsn(dbPath(t), false))
	if err != nil {
		t.Fatal(err)
	}

	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	fsys := fstest.MapFS{
		"migrations/0001_a.sql": {Data: []byte(`CREATE TABLE p (id INTEGER PRIMARY KEY); CREATE TABLE c (p INTEGER REFERENCES p (id)); INSERT INTO p VALUES (1); INSERT INTO c VALUES (1);`)},
		"migrations/0002_b.sql": {Data: []byte(fkOffMarker + `CREATE TABLE p2 (id INTEGER PRIMARY KEY); DROP TABLE p; ALTER TABLE p2 RENAME TO p;`)},
	}

	if err := migrate(ctx, db, fsys); err == nil || !strings.Contains(err.Error(), "foreign key violation") {
		t.Fatalf("migrate: %v", err)
	}

	var v, fk int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&v); err != nil || v != 1 {
		t.Errorf("version %d %v", v, err)
	}

	if err := db.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Errorf("foreign keys %d %v", fk, err)
	}
}

func TestPythonSites(t *testing.T) {
	st := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(1_800_000_000, 0)

	a := SiteApp{Python: "3.13", Node: "24", Workdir: "htdocs", StartKind: agentapi.AppStartWSGI, Start: "shop.wsgi:application", Args: []string{"--workers", "3"}}

	s1, err := st.CreatePythonSite(ctx, "shop", "default", a, []string{"shop.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if s1.Type != SiteTypePython || !s1.Account || s1.App == nil || s1.App.Port != agentapi.MinAppPort || s1.App.Node != "" {
		t.Fatalf("created %+v %+v", s1, s1.App)
	}

	want := SiteApp{Python: "3.13", Workdir: "htdocs", StartKind: agentapi.AppStartWSGI, Start: "shop.wsgi:application", Args: []string{"--workers", "3"}, Port: agentapi.MinAppPort}

	got, err := st.SiteByName(ctx, "shop")
	if err != nil || got.App == nil || !reflect.DeepEqual(*got.App, want) {
		t.Fatalf("read %+v %v", got.App, err)
	}

	old, err := st.SetSiteApp(ctx, "shop", SiteApp{Python: "3.14", Node: "24", Workdir: "app", StartKind: agentapi.AppStartASGI, Start: "main:app", Stopped: true}, now)
	if err != nil || !reflect.DeepEqual(old, want) {
		t.Fatalf("set: %+v %v", old, err)
	}

	got, _ = st.SiteByName(ctx, "shop")
	if !reflect.DeepEqual(*got.App, SiteApp{Python: "3.14", Workdir: "app", StartKind: agentapi.AppStartASGI, Start: "main:app", Port: agentapi.MinAppPort, Stopped: true}) {
		t.Errorf("after set %+v", got.App)
	}

	// A Node.js site becomes a Python site and keeps its port; back to
	// Node.js, the Python line goes.
	n, err := st.CreateNodeSite(ctx, "blog", "default", SiteApp{Node: "24", Workdir: "htdocs"}, []string{"blog.example.com"}, now)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.SetSiteType(ctx, "blog", SiteTypeChange{Type: SiteTypePython, Account: true, App: &SiteApp{Python: "3.13", Workdir: "htdocs", Stopped: true}}, now); err != nil {
		t.Fatal(err)
	}

	got, _ = st.SiteByName(ctx, "blog")
	if got.Type != SiteTypePython || got.App.Python != "3.13" || got.App.Node != "" || got.App.Port != n.App.Port {
		t.Errorf("to python: %s %+v", got.Type, got.App)
	}

	prev, err := st.SetSiteType(ctx, "blog", SiteTypeChange{Type: SiteTypeNode, Account: true, App: &SiteApp{Node: "24", Workdir: "htdocs", Stopped: true}}, now)
	if err != nil || prev.Type != SiteTypePython || prev.App == nil || prev.App.Python != "3.13" {
		t.Fatalf("to node: %+v %v", prev, err)
	}

	got, _ = st.SiteByName(ctx, "blog")
	if got.App.Python != "" || got.App.Node != "24" {
		t.Errorf("node again: %+v", got.App)
	}

	// Arguments of a Node.js app are still refused.
	if _, err := st.SetSiteApp(ctx, "blog", SiteApp{Node: "24", Workdir: "htdocs", StartKind: agentapi.AppStartScript, Start: "a.js", Args: []string{"x"}}, now); err == nil {
		t.Error("node arguments stored")
	}

	if _, err := st.SetSiteApp(ctx, "shop", SiteApp{Python: "2.7", Workdir: "htdocs"}, now); err == nil {
		t.Error("python 2.7 stored")
	}

	if _, err := st.SetSiteApp(ctx, "shop", SiteApp{Python: "3.13", Workdir: "htdocs", StartKind: "gunicorn", Start: "x:y"}, now); err == nil || errors.Is(err, ErrSiteType) {
		t.Errorf("unknown kind: %v", err)
	}
}
