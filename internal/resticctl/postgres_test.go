// SPDX-License-Identifier: AGPL-3.0-or-later

package resticctl

import (
	"archive/tar"
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/maki-it/hostashi/internal/dbserver"
)

func TestBackupPostgresNoClient(t *testing.T) {
	r := &Repo{Repository: "/repo", PasswordFile: "/pw", CacheDir: "/cache", Host: "h", TempDir: t.TempDir(), PGRoot: t.TempDir()}
	cfg := dbserver.Config{
		Engine: dbserver.EnginePostgreSQL, Host: "10.0.0.5", Port: 5432, AdminUser: "adm", AdminPassword: "pw", TLS: dbserver.TLSNone,
	}

	if _, err := r.BackupRemoteDatabase(t.Context(), []string{"site:a", "kind:db", "db:shop"}, "shop", cfg); !errors.Is(err, ErrNoPGClient) {
		t.Fatalf("got %v", err)
	}

	if left, _ := os.ReadDir(r.TempDir); len(left) != 0 {
		t.Fatalf("files left behind: %v", left)
	}
}

// TestResticPostgres dumps a database of a real PostgreSQL server through
// restic, as hostashi-backup does (CI: the restic job). Needs
// HOSTASHI_TEST_RESTIC_PG=host:port, HOSTASHI_TEST_RESTIC_PG_USER and
// HOSTASHI_TEST_RESTIC_PG_PW (an administrator with CREATEDB and
// CREATEROLE), a PostgreSQL client under /usr/lib/postgresql and restic
// (HOSTASHI_TEST_RESTIC_BIN, default the distribution's).
func TestResticPostgres(t *testing.T) {
	addr := os.Getenv("HOSTASHI_TEST_RESTIC_PG")
	if addr == "" {
		t.Skip("HOSTASHI_TEST_RESTIC_PG not set")
	}

	ctx := context.Background()
	host, port, _ := strings.Cut(addr, ":")
	cfg := dbserver.Config{
		Engine: dbserver.EnginePostgreSQL, Host: host, Port: mustAtoi(t, port), AdminUser: os.Getenv("HOSTASHI_TEST_RESTIC_PG_USER"),
		AdminPassword: os.Getenv("HOSTASHI_TEST_RESTIC_PG_PW"), TLS: dbserver.TLSNone,
	}

	admin, err := dbserver.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = admin.Close() })

	pc := dbserver.NewPGClient(admin, cfg)
	_ = pc.DropUser(ctx, "resticpg", "resticpg_u")
	_ = pc.DropDatabase(ctx, "resticpg")

	if err := pc.CreateDatabase(ctx, "resticpg"); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = pc.DropUser(context.Background(), "resticpg", "resticpg_u")
		_ = pc.DropDatabase(context.Background(), "resticpg")
	})

	if err := pc.CreateUser(ctx, "resticpg", "resticpg_u", "Abcdefghijklmnop1234"); err != nil {
		t.Fatal(err)
	}

	// The site's user makes a table; the admin dumps it as the owner role.
	uc := cfg
	uc.AdminUser, uc.AdminPassword = "resticpg_u", "Abcdefghijklmnop1234"

	udb, err := sql.Open("pgx", "host="+host+" port="+port+" dbname=resticpg sslmode=disable user="+uc.AdminUser+" password="+uc.AdminPassword)
	if err != nil {
		t.Fatal(err)
	}

	defer udb.Close()

	if _, err := udb.ExecContext(ctx, "CREATE TABLE t (a text); INSERT INTO t VALUES ('restic-pg-row')"); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	pw := filepath.Join(dir, "password")

	if err := os.WriteFile(pw, []byte("test-password-0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := &Repo{
		Repository: filepath.Join(dir, "repo"), PasswordFile: pw, CacheDir: filepath.Join(dir, "cache"), Host: "test-host",
		Binary: os.Getenv("HOSTASHI_TEST_RESTIC_BIN"), TempDir: t.TempDir(),
	}

	if err := r.Init(ctx); err != nil {
		t.Fatal(err)
	}

	tags := []string{"site:blog", "kind:db", "db:resticpg"}

	sum, err := r.BackupRemoteDatabase(ctx, tags, "resticpg", cfg)
	if err != nil || !ValidSnapshotID(sum.SnapshotID) {
		t.Fatalf("dump: %+v, %v", sum, err)
	}

	if left, _ := os.ReadDir(r.TempDir); len(left) != 0 {
		t.Fatalf("passfile left behind: %v", left)
	}

	var archive bytes.Buffer
	if err := r.Dump(ctx, sum.SnapshotID, &archive); err != nil {
		t.Fatal(err)
	}

	tr := tar.NewReader(&archive)

	var data []byte

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}

		if err != nil {
			t.Fatal(err)
		}

		if filepath.Base(h.Name) == "resticpg.dump" {
			data, _ = io.ReadAll(tr)
		}
	}

	if !bytes.HasPrefix(data, []byte("PGDMP")) {
		t.Fatalf("no custom-format resticpg.dump in the snapshot (%d bytes)", len(data))
	}

	// A failed pg_dump fails the snapshot: restic keeps nothing.
	wrong := cfg
	wrong.AdminPassword = "wrong"

	if _, err := r.BackupRemoteDatabase(ctx, tags, "resticpg", wrong); err == nil {
		t.Fatal("dump with a wrong password succeeded")
	}

	if snaps, err := r.Snapshots(ctx, tags); err != nil || len(snaps) != 1 {
		t.Fatalf("snapshots after the failed dump: %d, %v", len(snaps), err)
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()

	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}

	return n
}
