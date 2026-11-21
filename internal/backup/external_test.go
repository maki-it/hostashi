// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/extdb"
	"github.com/maki-it/hostashi/internal/secrets"
)

type fakeBox map[string]string // purpose -> password

func (b fakeBox) Open(data []byte, purpose string) ([]byte, error) {
	if pw, ok := b[purpose]; ok && string(data) == "sealed" {
		return []byte(pw), nil
	}

	return nil, errors.New("cannot open")
}

func manifest() extdb.Manifest {
	srv := func(id int64, name string) extdb.Server {
		return extdb.Server{ID: id, Name: name, Host: name + ".example.net", Port: 3306, AdminUser: "adm", SealedPassword: []byte("sealed"), TLSMode: "verify"}
	}

	return extdb.Manifest{
		Version: 1,
		Servers: []extdb.Server{srv(2, "db1"), srv(3, "db2")},
		Databases: []extdb.Database{
			{Name: "shop_z", Site: "shop", Server: "db1"},
			{Name: "shop_a", Site: "shop", Server: "db2"},
		},
	}
}

func TestAddExternal(t *testing.T) {
	sites := []Site{{Name: "shop", Home: "/home/shop", Databases: []string{"shop"}}}

	got, err := AddExternal(sites, manifest(), fakeBox{"db-server:2": "pw1"})
	if err != nil {
		t.Fatal(err)
	}

	ext := got[0].External
	if len(ext) != 2 || ext[0].Name != "shop_z" || ext[0].Server == nil || ext[0].Server.AdminPassword != "pw1" ||
		ext[0].Server.Host != "db1.example.net" || ext[0].Err != nil {
		t.Fatalf("db1: %+v", ext)
	}

	// db2's password cannot be opened: its database fails with the reason.
	if ext[1].Server != nil || ext[1].Err == nil || !strings.Contains(ext[1].Err.Error(), "db2") {
		t.Fatalf("db2: %+v", ext[1])
	}

	if len(sites[0].External) != 0 {
		t.Fatal("AddExternal changed its input")
	}

	// A PostgreSQL server's databases are dumped with its engine.
	m := manifest()
	m.Servers[0].Engine = dbserver.EnginePostgreSQL

	got, err = AddExternal(sites, m, fakeBox{"db-server:2": "pw1"})
	if err != nil {
		t.Fatal(err)
	}

	if e := got[0].External[0]; e.Server == nil || e.Err != nil || e.Server.Engine != dbserver.EnginePostgreSQL || e.Server.AdminPassword != "pw1" {
		t.Fatalf("PostgreSQL: %+v", e)
	}

	m = manifest()
	m.Databases = append(m.Databases, extdb.Database{Name: "gone_db", Site: "gone", Server: "db1"})

	if _, err := AddExternal(sites, m, fakeBox{}); err == nil || !strings.Contains(err.Error(), "gone_db") {
		t.Fatalf("unknown site: %v", err)
	}
}

func TestRunWithExternalDatabases(t *testing.T) {
	now := time.Date(2026, 11, 21, 2, 30, 0, 0, time.UTC)
	repo := &fakeRepo{failItem: "site:shop,kind:db,db:shop_down"}
	r := &Runner{
		Repo: repo, Settings: settingsWithTarget(), Now: func() time.Time { return now },
		Lstat:   fakeFS([]string{"/home/shop"}, nil),
		Warning: "external databases of unknown sites not saved: [gone_db]",
	}

	cfg := &dbserver.Config{Host: "db1.example.net", Port: 3306, AdminUser: "adm", AdminPassword: "pw", TLS: dbserver.TLSVerify}

	st := r.Run(context.Background(), Plan([]Site{{
		Name: "shop", Home: "/home/shop", Databases: []string{"shop"},
		External: []ExternalDB{
			{Name: "shop_z", Server: cfg},
			{Name: "shop_down", Server: cfg},
			{Name: "shop_key", Err: errors.New("open the admin password of database server db2")},
		},
	}}, nil))

	want := []string{
		"unlock",
		"backup /home/shop site:shop,kind:files",
		"dump shop site:shop,kind:db,db:shop",
		"remote-dump db1.example.net shop_down site:shop,kind:db,db:shop_down",
		"remote-dump db1.example.net shop_z site:shop,kind:db,db:shop_z",
		"forget",
		"prune",
		"check 1/12",
		"snapshots",
	}

	if !reflect.DeepEqual(repo.calls, want) {
		t.Errorf("calls\n got %q\nwant %q", repo.calls, want)
	}

	// The local part and the reachable external database are saved; the
	// run needs attention for the rest.
	if st.OK || !strings.Contains(st.Error, "gone_db") || len(st.Items) != 5 {
		t.Fatalf("status %+v", st)
	}

	errs := map[string]string{}
	for _, it := range st.Items {
		errs[it.Database] = it.Error
	}

	if errs["shop"] != "" || errs["shop_z"] != "" || errs["shop_down"] == "" || !strings.Contains(errs["shop_key"], "db2") {
		t.Fatalf("items %v", errs)
	}
}

func TestExternalFromFiles(t *testing.T) {
	dir := t.TempDir()
	panel := filepath.Join(dir, "panel")

	if err := os.Mkdir(panel, 0o700); err != nil {
		t.Fatal(err)
	}

	keyFile := filepath.Join(dir, "secret.key")

	box, err := secrets.LoadOrCreate(keyFile)
	if err != nil {
		t.Fatal(err)
	}

	sealed, err := box.Seal([]byte("Admin-pw"), extdb.PasswordPurpose(2))
	if err != nil {
		t.Fatal(err)
	}

	uid := uint32(os.Getuid())
	cfg := RunConfig{PanelDir: panel, KeyFile: keyFile, PanelUID: func() (uint32, error) { return uid, nil }}
	sites := []Site{{Name: "shop", Home: "/home/shop"}}

	// No list yet: nothing to add, no error.
	if got, err := external(cfg, sites); err != nil || len(got[0].External) != 0 {
		t.Fatalf("no list: %+v %v", got, err)
	}

	m := manifest()
	m.Servers = m.Servers[:1]
	m.Servers[0].SealedPassword = sealed
	m.Databases = m.Databases[:1]

	if err := extdb.Write(panel, m); err != nil {
		t.Fatal(err)
	}

	got, err := external(cfg, sites)
	if err != nil || len(got[0].External) != 1 || got[0].External[0].Server.AdminPassword != "Admin-pw" {
		t.Fatalf("%+v %v", got, err)
	}

	// The list must belong to the panel user.
	cfg.PanelUID = func() (uint32, error) { return uid + 1, nil }

	if _, err := external(cfg, sites); err == nil {
		t.Fatal("foreign owner accepted")
	}

	// Without the key the external databases are reported, not dropped
	// silently.
	cfg.PanelUID = func() (uint32, error) { return uid, nil }
	cfg.KeyFile = filepath.Join(dir, "missing.key")

	if _, err := external(cfg, sites); err == nil || !strings.Contains(err.Error(), "external databases not saved") {
		t.Fatalf("missing key: %v", err)
	}
}
