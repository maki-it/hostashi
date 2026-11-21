// SPDX-License-Identifier: AGPL-3.0-or-later

package dbserver

import (
	"crypto/x509"
	"database/sql"
	"errors"
	"net"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

// serverConfig reads a real server from the environment. CI starts MariaDB
// and MySQL containers with TLS (see .github/workflows/go.yml):
//
//	HOSTASHI_TEST_DBSERVER       host:port
//	HOSTASHI_TEST_DBSERVER_USER  admin user
//	HOSTASHI_TEST_DBSERVER_PW    admin password
//	HOSTASHI_TEST_DBSERVER_CA    path of the CA that signed the server certificate
//	HOSTASHI_TEST_DBSERVER_TLS   "verify-ca" (default) or "none"
func serverConfig(t *testing.T) Config {
	t.Helper()

	addr := os.Getenv("HOSTASHI_TEST_DBSERVER")
	if addr == "" {
		t.Skip("HOSTASHI_TEST_DBSERVER not set")
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
		Host: host, Port: p, AdminUser: os.Getenv("HOSTASHI_TEST_DBSERVER_USER"),
		AdminPassword: os.Getenv("HOSTASHI_TEST_DBSERVER_PW"), TLS: TLSVerifyCA,
	}

	if m := os.Getenv("HOSTASHI_TEST_DBSERVER_TLS"); m != "" {
		c.TLS = TLSMode(m)
	}

	if c.TLS == TLSVerifyCA {
		b, err := os.ReadFile(os.Getenv("HOSTASHI_TEST_DBSERVER_CA"))
		if err != nil {
			t.Fatal(err)
		}

		c.CAPEM = string(b)
	}

	return c
}

func open(t *testing.T, c Config) *sql.DB {
	t.Helper()

	db, err := Open(c)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	return db
}

func TestServer(t *testing.T) {
	cfg := serverConfig(t)
	ctx := t.Context()

	var notAfter time.Time

	cfg.OnCertificate = func(c *x509.Certificate) { notAfter = c.NotAfter }
	admin := open(t, cfg)

	info, err := Probe(ctx, admin)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("server: %s, client host %s, TLS %v", info.Display(), info.ClientHost, info.TLS)

	if info.TLS != (cfg.TLS != TLSNone) {
		t.Fatalf("TLS %v with mode %s", info.TLS, cfg.TLS)
	}

	// The CI certificate is valid for one day.
	if cfg.TLS != TLSNone && (notAfter.Before(time.Now()) || notAfter.After(time.Now().Add(49*time.Hour))) {
		t.Fatalf("certificate end %v", notAfter)
	}

	c, err := NewClient(admin, info.ClientHost)
	if err != nil {
		t.Fatal(err)
	}

	if err := c.CheckPrivileges(ctx); err != nil {
		t.Fatal(err)
	}

	const pw = "Abcdefghijklmnop1234"

	// A second database whose name the first one's grant would match if
	// "_" were not escaped.
	for _, db := range []string{"shop_wp", "shopxwp"} {
		_ = c.DropDatabase(ctx, db)

		if err := c.CreateDatabase(ctx, db); err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = c.DropDatabase(ctx, db) })
	}

	if err := c.CreateDatabase(ctx, "shop_wp"); !errors.Is(err, ErrExists) {
		t.Fatalf("second create: %v", err)
	}

	_ = c.DropUser(ctx, "shop_user")

	if err := c.CreateUser(ctx, "shop_wp", "shop_user", pw); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = c.DropUser(ctx, "shop_user") })

	if err := c.CreateUser(ctx, "shop_wp", "shop_user", pw); !errors.Is(err, ErrExists) {
		t.Fatalf("second user: %v", err)
	}

	user := cfg
	user.AdminUser, user.AdminPassword = "shop_user", pw
	udb := open(t, user)

	if _, err := udb.ExecContext(ctx, "CREATE TABLE shop_wp.t (a INT)"); err != nil {
		t.Fatalf("user cannot use its database: %v", err)
	}

	var me *mysql.MySQLError
	if _, err := udb.ExecContext(ctx, "CREATE TABLE shopxwp.t (a INT)"); !errors.As(err, &me) {
		t.Fatalf("user can use another database: %v", err)
	}

	if err := c.SetPassword(ctx, "shop_user", pw+"9"); err != nil {
		t.Fatal(err)
	}

	_ = udb.Close()

	if err := open(t, user).PingContext(ctx); err == nil {
		t.Fatal("old password still works")
	}

	user.AdminPassword = pw + "9"
	if err := open(t, user).PingContext(ctx); err != nil {
		t.Fatalf("new password: %v", err)
	}

	if err := c.DropUser(ctx, "shop_user"); err != nil {
		t.Fatal(err)
	}

	if err := c.DropUser(ctx, "shop_user"); err != nil {
		t.Fatalf("second drop: %v", err)
	}
}

func TestServerWrongPassword(t *testing.T) {
	cfg := serverConfig(t)
	cfg.AdminPassword += "x"

	if _, err := Probe(t.Context(), open(t, cfg)); !errors.Is(err, ErrAccess) {
		t.Fatalf("got %v", err)
	}
}

// TestServerUntrustedCertificate checks that verify mode, which uses the
// system roots, refuses the test server's private CA.
func TestServerUntrustedCertificate(t *testing.T) {
	cfg := serverConfig(t)
	if cfg.TLS == TLSNone {
		t.Skip("server without TLS")
	}

	cfg.TLS, cfg.CAPEM = TLSVerify, ""

	if _, err := Probe(t.Context(), open(t, cfg)); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("got %v", err)
	}
}
