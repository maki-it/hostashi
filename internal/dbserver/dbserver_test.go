// SPDX-License-Identifier: AGPL-3.0-or-later

package dbserver

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/pem"
	"errors"
	"math/big"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
)

func testCA(t *testing.T) string {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func validConfig() Config {
	return Config{Host: "db.example.net", Port: 3306, AdminUser: "hostashi_admin", AdminPassword: "s3cret'\"`\\ x", TLS: TLSVerify}
}

func TestConfigValidate(t *testing.T) {
	ca := testCA(t)

	ok := []func(*Config){
		func(*Config) {},
		func(c *Config) { c.Host = "10.0.0.5" },
		func(c *Config) { c.Host = "fd00::5" },
		func(c *Config) { c.TLS = TLSNone },
		func(c *Config) { c.TLS = TLSVerifyCA; c.CAPEM = ca },
		func(c *Config) { c.Port = 65535 },
	}

	for i, f := range ok {
		c := validConfig()
		f(&c)

		if err := c.Validate(); err != nil {
			t.Errorf("ok %d: %v", i, err)
		}
	}

	bad := map[string]func(*Config){
		"empty host":       func(c *Config) { c.Host = "" },
		"upper host":       func(c *Config) { c.Host = "DB.example.net" },
		"host with port":   func(c *Config) { c.Host = "db:3306" },
		"host with space":  func(c *Config) { c.Host = "db example" },
		"host with slash":  func(c *Config) { c.Host = "db/x" },
		"leading dash":     func(c *Config) { c.Host = "-db" },
		"long host":        func(c *Config) { c.Host = strings.Repeat("a.", 127) + "a" },
		"port 0":           func(c *Config) { c.Port = 0 },
		"port high":        func(c *Config) { c.Port = 65536 },
		"no user":          func(c *Config) { c.AdminUser = "" },
		"user newline":     func(c *Config) { c.AdminUser = "a\nb" },
		"long user":        func(c *Config) { c.AdminUser = strings.Repeat("a", MaxAdminUser+1) },
		"no password":      func(c *Config) { c.AdminPassword = "" },
		"long password":    func(c *Config) { c.AdminPassword = strings.Repeat("a", MaxAdminPwLen+1) },
		"password newline": func(c *Config) { c.AdminPassword = "a\nb" },
		"password utf8":    func(c *Config) { c.AdminPassword = "pässword" },
		"unknown tls":      func(c *Config) { c.TLS = "require" },
		"empty tls":        func(c *Config) { c.TLS = "" },
		"verify-ca no CA":  func(c *Config) { c.TLS = TLSVerifyCA },
		"verify-ca bad CA": func(c *Config) { c.TLS = TLSVerifyCA; c.CAPEM = "not pem" },
		"CA with verify":   func(c *Config) { c.CAPEM = ca },
		"CA with none":     func(c *Config) { c.TLS = TLSNone; c.CAPEM = ca },
		"huge CA":          func(c *Config) { c.TLS = TLSVerifyCA; c.CAPEM = strings.Repeat("a", MaxCAPEMLen+1) },
	}

	for name, f := range bad {
		c := validConfig()
		f(&c)

		if err := c.Validate(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
}

func TestTLSConfig(t *testing.T) {
	c := validConfig()

	tc, err := c.tlsConfig()
	if err != nil || tc == nil || tc.ServerName != c.Host || tc.InsecureSkipVerify || tc.RootCAs != nil {
		t.Fatalf("verify: %+v, %v", tc, err)
	}

	c.TLS, c.CAPEM = TLSVerifyCA, testCA(t)

	tc, err = c.tlsConfig()
	if err != nil || tc == nil || tc.ServerName != c.Host || tc.InsecureSkipVerify || tc.RootCAs == nil {
		t.Fatalf("verify-ca: %+v, %v", tc, err)
	}

	c.TLS, c.CAPEM = TLSNone, ""

	tc, err = c.tlsConfig()
	if err != nil || tc != nil {
		t.Fatalf("none: %+v, %v", tc, err)
	}
}

func TestOpenRejectsInvalid(t *testing.T) {
	c := validConfig()
	c.Port = 0

	if _, err := Open(c); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestValidateClientHost(t *testing.T) {
	for _, h := range []string{"10.0.0.5", "fd00::1", "::ffff:172.17.0.1", "web1.example.net", "Web1"} {
		if err := ValidateClientHost(h); err != nil {
			t.Errorf("%q: %v", h, err)
		}
	}

	for _, h := range []string{"", "%", "10.0.0.%", "web_1", "a'b", "a b", "a@b", strings.Repeat("a", 254)} {
		if err := ValidateClientHost(h); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: got %v", h, err)
		}
	}
}

func TestParseVersion(t *testing.T) {
	cases := []struct{ in, engine, version string }{
		{"11.8.6-MariaDB-0+deb13u1", EngineMariaDB, "11.8.6"},
		{"10.11.14-MariaDB-log", EngineMariaDB, "10.11.14"},
		{"10.5.0-MariaDB", EngineMariaDB, "10.5.0"},
		{"8.4.7", EngineMySQL, "8.4.7"},
		{"8.0.44-0ubuntu0.24.04.1", EngineMySQL, "8.0.44"},
		{"9.5.0", EngineMySQL, "9.5.0"},
	}

	for _, c := range cases {
		e, v, err := ParseVersion(c.in)
		if err != nil || e != c.engine || v != c.version {
			t.Errorf("%q: %q %q %v", c.in, e, v, err)
		}
	}

	for _, in := range []string{"", "x", "5.7.44", "10.4.34-MariaDB", "5.5.5-10.3.0-MariaDB", "a.b", "26.10.0", "10.0.1"} {
		if _, _, err := ParseVersion(in); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%q: got %v", in, err)
		}
	}
}

type fakeExec struct {
	stmts []string
	fail  map[string]error // statement prefix -> error
}

func (f *fakeExec) ExecContext(_ context.Context, q string, _ ...any) (sql.Result, error) {
	f.stmts = append(f.stmts, q)

	for p, err := range f.fail {
		if strings.HasPrefix(q, p) {
			return nil, err
		}
	}

	return driverResult{}, nil
}

type driverResult struct{}

func (driverResult) LastInsertId() (int64, error) { return 0, nil }
func (driverResult) RowsAffected() (int64, error) { return 0, nil }

func TestNewClientRejectsWildcardHost(t *testing.T) {
	if _, err := NewClient(&fakeExec{}, "%"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}
}

func TestStatements(t *testing.T) {
	f := &fakeExec{}

	c, err := NewClient(f, "10.0.0.7")
	if err != nil {
		t.Fatal(err)
	}

	ctx := t.Context()
	pw := "Abcdefghijklmnop1234"

	for _, err := range []error{
		c.CreateDatabase(ctx, "shop_wp"),
		c.CreateUser(ctx, "shop_wp", "shop_user", pw),
		c.SetPassword(ctx, "shop_user", pw+"x"),
		c.DropUser(ctx, "", "shop_user"),
		c.DropDatabase(ctx, "shop_wp"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	want := []string{
		"CREATE DATABASE `shop_wp` CHARACTER SET utf8mb4",
		"CREATE USER 'shop_user'@'10.0.0.7' IDENTIFIED BY '" + pw + "'",
		"GRANT ALL PRIVILEGES ON `shop\\_wp`.* TO 'shop_user'@'10.0.0.7'",
		"ALTER USER 'shop_user'@'10.0.0.7' IDENTIFIED BY '" + pw + "x'",
		"DROP USER IF EXISTS 'shop_user'@'10.0.0.7'",
		"DROP DATABASE IF EXISTS `shop_wp`",
	}

	if strings.Join(f.stmts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("statements:\n%s\nwant:\n%s", strings.Join(f.stmts, "\n"), strings.Join(want, "\n"))
	}
}

func TestStatementsRejectInvalid(t *testing.T) {
	f := &fakeExec{}
	c, _ := NewClient(f, "10.0.0.7")
	ctx := t.Context()
	pw := "Abcdefghijklmnop1234"

	for name, err := range map[string]error{
		"db backtick":    c.CreateDatabase(ctx, "a`b"),
		"db reserved":    c.CreateDatabase(ctx, "mysql"),
		"db hostashi":    c.DropDatabase(ctx, "hostashi_x"),
		"user quote":     c.CreateUser(ctx, "shop", "a'b", pw),
		"user reserved":  c.DropUser(ctx, "", "root"),
		"pw quote":       c.CreateUser(ctx, "shop", "shop", "Abcdefghijkl'mnop1234"),
		"pw short":       c.SetPassword(ctx, "shop", "short"),
		"pw backslash":   c.SetPassword(ctx, "shop", `Abcdefghijklmnop\1234`),
		"db in user req": c.CreateUser(ctx, "a b", "shop", pw),
	} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}

	if len(f.stmts) != 0 {
		t.Fatalf("statements ran: %q", f.stmts)
	}
}

func TestCreateUserUndoesOnGrantFailure(t *testing.T) {
	f := &fakeExec{fail: map[string]error{"GRANT": &mysql.MySQLError{Number: errDBAccess, Message: "secret text"}}}
	c, _ := NewClient(f, "10.0.0.7")

	err := c.CreateUser(t.Context(), "shop", "shop", "Abcdefghijklmnop1234")
	if !errors.Is(err, ErrAccess) {
		t.Fatalf("got %v", err)
	}

	if strings.Contains(err.Error(), "secret text") {
		t.Fatalf("error leaks the server message: %v", err)
	}

	if last := f.stmts[len(f.stmts)-1]; last != "DROP USER IF EXISTS 'shop'@'10.0.0.7'" {
		t.Fatalf("no undo: %q", f.stmts)
	}
}

func TestCheckPrivilegesCleansUp(t *testing.T) {
	f := &fakeExec{fail: map[string]error{"GRANT": &mysql.MySQLError{Number: errNoGrantOption}}}
	c, _ := NewClient(f, "10.0.0.7")

	if err := c.CheckPrivileges(t.Context()); !errors.Is(err, ErrAccess) {
		t.Fatalf("got %v", err)
	}

	n := len(f.stmts)
	if n != 5 || !strings.HasPrefix(f.stmts[3], "DROP USER IF EXISTS 'hostashi_probe_") ||
		!strings.HasPrefix(f.stmts[4], "DROP DATABASE IF EXISTS `hostashi_probe_") {
		t.Fatalf("statements: %q", f.stmts)
	}
}

func TestCheckPrivilegesSuccess(t *testing.T) {
	f := &fakeExec{}
	c, _ := NewClient(f, "10.0.0.7")

	if err := c.CheckPrivileges(t.Context()); err != nil {
		t.Fatal(err)
	}

	// create db, create user, grant, drop user, drop db, then the two
	// deferred IF EXISTS drops that find nothing.
	if len(f.stmts) != 7 {
		t.Fatalf("statements: %q", f.stmts)
	}

	if !probeStmt.MatchString(f.stmts[0]) {
		t.Fatalf("odd probe name: %q", f.stmts[0])
	}
}

var probeStmt = regexp.MustCompile("^CREATE DATABASE `hostashi_probe_[a-z0-9]{8}` CHARACTER SET utf8mb4$")

func TestClassify(t *testing.T) {
	cases := []struct {
		err  error
		want error
	}{
		{&mysql.MySQLError{Number: errDBCreateExists}, ErrExists},
		{&mysql.MySQLError{Number: errCannotUser}, ErrExists},
		{&mysql.MySQLError{Number: errAccessDenied}, ErrAccess},
		{&mysql.MySQLError{Number: errSpecificAccess}, ErrAccess},
		{context.DeadlineExceeded, ErrUnavailable},
		{mysql.ErrInvalidConn, ErrUnavailable},
		{&x509.CertificateInvalidError{}, nil},
	}

	for _, c := range cases {
		got := classify("step", c.err)
		if c.want != nil && !errors.Is(got, c.want) {
			t.Errorf("%v: got %v", c.err, got)
		}
	}
}

func TestClientOptions(t *testing.T) {
	c := validConfig()

	got, err := c.ClientOptions("")
	if err != nil {
		t.Fatal(err)
	}

	want := "[client]\nhost=db.example.net\nport=3306\nuser=\"hostashi_admin\"\n" +
		`password="s3cret'\"` + "`" + `\\ x"` + "\nssl\nssl-verify-server-cert\nssl-ca=" + SystemCAFile + "\n"
	if got != want {
		t.Fatalf("verify:\n%s\nwant:\n%s", got, want)
	}

	c.TLS, c.CAPEM = TLSVerifyCA, testCA(t)

	if got, err := c.ClientOptions("/tmp/ca.pem"); err != nil || !strings.HasSuffix(got, "ssl\nssl-verify-server-cert\nssl-ca=/tmp/ca.pem\n") {
		t.Fatalf("verify-ca: %q %v", got, err)
	}

	for _, p := range []string{"", "ca.pem", "/tmp/a\nssl=0", "/tmp/a#b"} {
		if _, err := c.ClientOptions(p); !errors.Is(err, ErrInvalid) {
			t.Errorf("CA path %q: %v", p, err)
		}
	}

	c.TLS, c.CAPEM = TLSNone, ""

	if got, err := c.ClientOptions(""); err != nil || !strings.HasSuffix(got, "\nskip-ssl\n") {
		t.Fatalf("none: %q %v", got, err)
	}

	c.Host = "x\ny"
	if _, err := c.ClientOptions(""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad host: %v", err)
	}
}

func TestJobUserStatements(t *testing.T) {
	f := &fakeExec{}
	c, _ := NewClient(f, "10.0.0.7")
	ctx := t.Context()

	const (
		user = "hostashi_job_0123456789abcdef"
		pw   = "Abcdefghijklmnopqrstuvwxyz012345"
	)

	if err := c.CreateJobUser(ctx, "shop_wp", user, pw); err != nil {
		t.Fatal(err)
	}

	if err := c.DropJobUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"CREATE USER '" + user + "'@'10.0.0.7' IDENTIFIED BY '" + pw + "'",
		"GRANT ALL PRIVILEGES ON `shop\\_wp`.* TO '" + user + "'@'10.0.0.7'",
		"DROP USER IF EXISTS '" + user + "'@'10.0.0.7'",
	}

	if strings.Join(f.stmts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("statements:\n%s", strings.Join(f.stmts, "\n"))
	}

	f.stmts = nil

	for name, err := range map[string]error{
		"site user":  c.CreateJobUser(ctx, "shop_wp", "shop_user", pw),
		"no pw":      c.CreateJobUser(ctx, "shop_wp", user, ""),
		"short pw":   c.CreateJobUser(ctx, "shop_wp", user, "Abc"),
		"bad db":     c.CreateJobUser(ctx, "mysql", user, pw),
		"drop other": c.DropJobUser(ctx, "root"),
	} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}

	if len(f.stmts) != 0 {
		t.Fatalf("statements ran: %q", f.stmts)
	}
}

func TestToolUserStatements(t *testing.T) {
	f := &fakeExec{}
	c, _ := NewClient(f, "10.0.0.7")
	ctx := t.Context()
	user, pw := "hostashi_tool_0123456789abcdef", "Abcdefghijklmnopqrstuvwxyz012345"

	for name, err := range map[string]error{
		"site user": c.CreateToolUser(ctx, "shop_wp", "shop_user", pw),
		"job user":  c.DropToolUser(ctx, "hostashi_job_0123456789abcdef"),
		"short pw":  c.CreateToolUser(ctx, "shop_wp", user, "Abcdefghijklmnop1234"),
		"no pw":     c.CreateToolUser(ctx, "shop_wp", user, ""),
		"db":        c.CreateToolUser(ctx, "mysql", user, pw),
	} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v", name, err)
		}
	}

	if len(f.stmts) != 0 {
		t.Fatalf("statements ran: %q", f.stmts)
	}

	if err := c.CreateToolUser(ctx, "shop_wp", user, pw); err != nil {
		t.Fatal(err)
	}

	if err := c.DropToolUser(ctx, user); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"CREATE USER '" + user + "'@'10.0.0.7' IDENTIFIED BY '" + pw + "' WITH MAX_USER_CONNECTIONS 2",
		"GRANT ALL PRIVILEGES ON `shop\\_wp`.* TO '" + user + "'@'10.0.0.7'",
		"DROP USER IF EXISTS '" + user + "'@'10.0.0.7'",
		"KILL HARD CONNECTION USER '" + user + "'@'10.0.0.7'",
	}

	if strings.Join(f.stmts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("statements:\n%s\nwant:\n%s", strings.Join(f.stmts, "\n"), strings.Join(want, "\n"))
	}
}
