// SPDX-License-Identifier: AGPL-3.0-or-later

package dbserver

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"net"
	"os"
	"strconv"
	"testing"
	"time"
)

// pgServerConfig reads a real PostgreSQL server from the environment. CI
// starts PostgreSQL containers (see .github/workflows/go.yml):
//
//	HOSTASHI_TEST_PGSERVER       host:port
//	HOSTASHI_TEST_PGSERVER_USER  admin role with CREATEDB and CREATEROLE
//	HOSTASHI_TEST_PGSERVER_PW    its password
//	HOSTASHI_TEST_PGSERVER_CA    path of the CA that signed the server certificate
//	HOSTASHI_TEST_PGSERVER_TLS   "verify-ca" (default) or "none"
func pgServerConfig(t *testing.T) Config {
	t.Helper()

	addr := os.Getenv("HOSTASHI_TEST_PGSERVER")
	if addr == "" {
		t.Skip("HOSTASHI_TEST_PGSERVER not set")
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}

	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}

	c := Config{
		Engine: EnginePostgreSQL, Host: host, Port: p, AdminUser: os.Getenv("HOSTASHI_TEST_PGSERVER_USER"),
		AdminPassword: os.Getenv("HOSTASHI_TEST_PGSERVER_PW"), TLS: TLSVerifyCA,
	}

	if m := os.Getenv("HOSTASHI_TEST_PGSERVER_TLS"); m != "" {
		c.TLS = TLSMode(m)
	}

	if c.TLS == TLSVerifyCA {
		b, err := os.ReadFile(os.Getenv("HOSTASHI_TEST_PGSERVER_CA"))
		if err != nil {
			t.Fatal(err)
		}

		c.CAPEM = string(b)
	}

	return c
}

func openPG(t *testing.T, c Config, database string) *sql.DB {
	t.Helper()

	db, err := openPostgres(c, database)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	return db
}

func TestPGServer(t *testing.T) {
	cfg := pgServerConfig(t)
	ctx := t.Context()

	var notAfter time.Time

	cfg.OnCertificate = func(c *x509.Certificate) { notAfter = c.NotAfter }
	admin := open(t, cfg)

	info, err := ProbePostgres(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("server: %s, client host %s, TLS %v, superuser %v", info.Display(), info.ClientHost, info.TLS, info.Superuser)

	if info.TLS != (cfg.TLS != TLSNone) {
		t.Fatalf("TLS %v with mode %s", info.TLS, cfg.TLS)
	}

	if cfg.TLS != TLSNone && (notAfter.Before(time.Now()) || notAfter.After(time.Now().Add(49*time.Hour))) {
		t.Fatalf("certificate end %v", notAfter)
	}

	if info.Superuser {
		t.Fatal("the CI administrator is not a superuser")
	}

	cfg.OnCertificate = nil
	c := NewPGClient(admin, cfg)

	if err := c.CheckPrivileges(ctx); err != nil {
		t.Fatal(err)
	}

	const pw = "Abcdefghijklmnop1234"

	for _, db := range []string{"shop_wp", "other"} {
		_ = c.DropUser(ctx, db, db+"_u")
		_ = c.DropDatabase(ctx, db)

		if err := c.CreateDatabase(ctx, db); err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() {
			ctx := context.WithoutCancel(ctx)
			_ = c.DropUser(ctx, db, db+"_u")
			_ = c.DropUser(ctx, db, "shop_wp_2")
			_ = c.DropDatabase(ctx, db)
		})

		if err := c.CreateUser(ctx, db, db+"_u", pw); err != nil {
			t.Fatal(err)
		}
	}

	if err := c.CreateDatabase(ctx, "shop_wp"); !errors.Is(err, ErrExists) {
		t.Fatalf("second create: %v", err)
	}

	if err := c.CreateUser(ctx, "shop_wp", "shop_wp_u", pw); !errors.Is(err, ErrExists) {
		t.Fatalf("second user: %v", err)
	}

	if err := c.CreateUser(ctx, "missing", "lost_u", pw); !errors.Is(err, ErrNotFound) {
		t.Fatalf("user of a missing database: %v", err)
	}

	user := cfg
	user.AdminUser, user.AdminPassword = "shop_wp_u", pw

	// A second user of the same database works on the first one's tables:
	// both act as the owner role.
	_ = c.DropUser(ctx, "shop_wp", "shop_wp_2")

	if err := c.CreateUser(ctx, "shop_wp", "shop_wp_2", pw); err != nil {
		t.Fatal(err)
	}

	udb := openPG(t, user, "shop_wp")
	if _, err := udb.ExecContext(ctx, "CREATE TABLE t (a int)"); err != nil {
		t.Fatalf("user cannot use its database: %v", err)
	}

	// An object made after RESET ROLE belongs to the user; dropping the
	// user hands it to the owner role.
	if _, err := udb.ExecContext(ctx, "RESET ROLE; CREATE TABLE mine (a int)"); err != nil {
		t.Fatalf("own table: %v", err)
	}

	second := cfg
	second.AdminUser, second.AdminPassword = "shop_wp_2", pw

	if _, err := openPG(t, second, "shop_wp").ExecContext(ctx, "INSERT INTO t VALUES (1); DROP TABLE t"); err != nil {
		t.Fatalf("second user cannot use the shared table: %v", err)
	}

	if err := openPG(t, user, "other").PingContext(ctx); !errors.Is(classifyPG("connect", err), ErrAccess) {
		t.Fatalf("user reaches another database: %v", err)
	}

	if err := c.SetPassword(ctx, "shop_wp_u", pw+"9"); err != nil {
		t.Fatal(err)
	}

	if err := openPG(t, user, "shop_wp").PingContext(ctx); err == nil {
		t.Fatal("old password still works")
	}

	user.AdminPassword = pw + "9"
	if err := openPG(t, user, "shop_wp").PingContext(ctx); err != nil {
		t.Fatalf("new password: %v", err)
	}

	// Dropping a user ends its open session.
	if err := c.DropUser(ctx, "shop_wp", "shop_wp_u"); err != nil {
		t.Fatal(err)
	}

	if _, err := udb.ExecContext(ctx, "SELECT 1"); err == nil {
		t.Fatal("session of the dropped user still works")
	}

	if err := c.DropUser(ctx, "shop_wp", "shop_wp_u"); err != nil {
		t.Fatalf("second drop: %v", err)
	}

	var owner string
	if err := openPG(t, second, "shop_wp").QueryRowContext(ctx,
		"SELECT tableowner FROM pg_tables WHERE tablename = 'mine'").Scan(&owner); err != nil || owner != OwnerRole("shop_wp") {
		t.Fatalf("owner of the dropped user's table: %q, %v", owner, err)
	}

	// Dropping the database ends the second user's session too.
	sdb := openPG(t, second, "shop_wp")
	if err := sdb.PingContext(ctx); err != nil {
		t.Fatal(err)
	}

	if err := c.DropUser(ctx, "shop_wp", "shop_wp_2"); err != nil {
		t.Fatal(err)
	}

	if err := c.DropDatabase(ctx, "shop_wp"); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := admin.QueryRowContext(ctx, "SELECT count(*) FROM pg_roles WHERE rolname = $1", OwnerRole("shop_wp")).Scan(&n); err != nil || n != 0 {
		t.Fatalf("owner role left: %d, %v", n, err)
	}

	if err := c.DropDatabase(ctx, "shop_wp"); err != nil {
		t.Fatalf("second drop: %v", err)
	}
}

// TestPGServerVerifierStored checks that a login works with the password
// whose SCRAM verifier the panel computed.
func TestPGServerVerifierStored(t *testing.T) {
	cfg := pgServerConfig(t)
	ctx := t.Context()
	admin := open(t, cfg)
	c := NewPGClient(admin, cfg)

	_ = c.DropUser(ctx, "verif", "verif_u")
	_ = c.DropDatabase(ctx, "verif")

	if err := c.CreateDatabase(ctx, "verif"); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		_ = c.DropUser(ctx, "verif", "verif_u")
		_ = c.DropDatabase(ctx, "verif")
	})

	if err := c.CreateUser(ctx, "verif", "verif_u", "Abcdefghijklmnop1234"); err != nil {
		t.Fatal(err)
	}

	u := cfg
	u.AdminUser, u.AdminPassword = "verif_u", "Abcdefghijklmnop1234"

	if err := openPG(t, u, "verif").PingContext(ctx); err != nil {
		t.Fatalf("login with the verifier's password: %v", err)
	}
}

func TestPGServerWrongPassword(t *testing.T) {
	cfg := pgServerConfig(t)
	cfg.AdminPassword += "x"

	if _, err := ProbePostgres(t.Context(), open(t, cfg)); !errors.Is(err, ErrAccess) {
		t.Fatalf("got %v", err)
	}
}

func TestPGServerUntrustedCertificate(t *testing.T) {
	cfg := pgServerConfig(t)
	if cfg.TLS == TLSNone {
		t.Skip("server without TLS")
	}

	cfg.TLS, cfg.CAPEM = TLSVerify, ""

	if _, err := ProbePostgres(t.Context(), open(t, cfg)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}
