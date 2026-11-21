// SPDX-License-Identifier: AGPL-3.0-or-later

package dbserver

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestParsePostgresVersion(t *testing.T) {
	for in, want := range map[string]string{"140000": "14.0", "170006": "17.6", "180001": "18.1", "190000": "19.0"} {
		got, err := ParsePostgresVersion(in)
		if err != nil || got != want {
			t.Errorf("%s: %q, %v", in, got, err)
		}

		if PostgresMajor(got) != int(in[0]-'0')*10+int(in[1]-'0') {
			t.Errorf("major of %s", got)
		}
	}

	for _, in := range []string{"", "x", "-1", "130012", "90624"} {
		if _, err := ParsePostgresVersion(in); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%q: %v", in, err)
		}
	}
}

func TestPostgresNames(t *testing.T) {
	for _, n := range []string{"postgres", "template0", "template1", "pg_shop", strings.Repeat("a", 64), "hostashi_x", "Shop"} {
		if ValidatePostgresDBName(n) == nil {
			t.Errorf("database %q accepted", n)
		}
	}

	if err := ValidatePostgresDBName(strings.Repeat("a", 63)); err != nil {
		t.Error(err)
	}

	for _, n := range []string{"postgres", "public", "none", "current_user", "session_user", "user", "pg_x", "hostashi_x"} {
		if ValidatePostgresUser(n) == nil {
			t.Errorf("user %q accepted", n)
		}
	}

	if err := ValidatePostgresUser("shop_user"); err != nil {
		t.Error(err)
	}

	if r := OwnerRole("shop"); !regexp.MustCompile(`^hostashi_db_[0-9a-f]{16}$`).MatchString(r) || r == OwnerRole("shop2") || r != OwnerRole("shop") {
		t.Errorf("owner role %q", r)
	}
}

func TestScramVerifier(t *testing.T) {
	// The salt and iteration count of RFC 7677, section 3. The keys were
	// computed separately with Python's hashlib; the integration test
	// logs in with such a verifier.
	got, err := scramVerifierWith("pencil", []byte{
		0x5b, 0x6d, 0x99, 0x68, 0x9d, 0x12, 0x35, 0x8e, 0xec, 0xa0, 0x4b, 0x14, 0x12, 0x36, 0xfa, 0x81,
	}, 4096)
	if err != nil {
		t.Fatal(err)
	}

	const want = "SCRAM-SHA-256$4096:W22ZaJ0SNY7soEsUEjb6gQ==$WG5d8oPm3OtcPnkdi4Uo7BkeZkBFzpcXkuLmtbsT4qY=:wfPLwcE6nTWhTAmQ7tl2KeoiWGPlZqQxSrmfPwDl2dU="
	if got != want {
		t.Fatalf("got %s", got)
	}

	a, _ := scramVerifier("pencil")
	b, _ := scramVerifier("pencil")

	if a == b || strings.Contains(a, "pencil") || strings.ContainsAny(a, `'"\`) {
		t.Fatalf("verifiers %s %s", a, b)
	}
}

func newFakePG() (*PGClient, *fakeExec, *fakeExec) {
	main, inDB := &fakeExec{fail: map[string]error{}}, &fakeExec{fail: map[string]error{}}

	return &PGClient{db: main, in: func(_ context.Context, database string, fn func(Execer) error) error {
		inDB.stmts = append(inDB.stmts, "-- in "+database)

		return fn(inDB)
	}}, main, inDB
}

func pgErr(code string) error {
	return fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: code, Message: "secret 'pw'"})
}

func TestPGCreateDatabase(t *testing.T) {
	c, f, _ := newFakePG()
	owner := `"` + OwnerRole("shop") + `"`

	if err := c.CreateDatabase(t.Context(), "shop"); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"CREATE ROLE " + owner + " NOLOGIN",
		"GRANT " + owner + " TO CURRENT_USER",
		`CREATE DATABASE "shop" OWNER ` + owner + " ENCODING 'UTF8' TEMPLATE template0",
		`REVOKE CONNECT, TEMPORARY ON DATABASE "shop" FROM PUBLIC`,
	}
	if strings.Join(f.stmts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("statements:\n%s", strings.Join(f.stmts, "\n"))
	}

	// An existing database is ErrExists and leaves the owner role of the
	// existing database alone only if that role was new: here the role
	// is created first, so an existing role is ErrExists at once.
	c, f, _ = newFakePG()
	f.fail["CREATE ROLE"] = pgErr(pgDuplicateObject)

	if err := c.CreateDatabase(t.Context(), "shop"); !errors.Is(err, ErrExists) || len(f.stmts) != 1 {
		t.Fatalf("existing role: %v, %v", err, f.stmts)
	}

	// A failed CREATE DATABASE removes the new role again.
	c, f, _ = newFakePG()
	f.fail["CREATE DATABASE"] = pgErr(pgDuplicateDatabase)

	err := c.CreateDatabase(t.Context(), "shop")
	if !errors.Is(err, ErrExists) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("got %v", err)
	}

	if last := f.stmts[len(f.stmts)-1]; last != "DROP ROLE IF EXISTS "+owner {
		t.Fatalf("no undo: %v", f.stmts)
	}

	if err := c.CreateDatabase(t.Context(), "postgres"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reserved: %v", err)
	}
}

func TestPGCreateUser(t *testing.T) {
	const pw = "Abcdefghijklmnop1234"

	c, f, _ := newFakePG()

	if err := c.CreateUser(t.Context(), "shop", "shop_user", pw); err != nil {
		t.Fatal(err)
	}

	all := strings.Join(f.stmts, "\n")
	if strings.Contains(all, pw) || !strings.Contains(all, "PASSWORD 'SCRAM-SHA-256$4096:") {
		t.Fatalf("password not as verifier:\n%s", all)
	}

	if !strings.Contains(f.stmts[0], `IN ROLE "`+OwnerRole("shop")+`"`) || f.stmts[1] != `GRANT "shop_user" TO CURRENT_USER` ||
		f.stmts[2] != `ALTER ROLE "shop_user" SET role = '`+OwnerRole("shop")+`'` {
		t.Fatalf("statements:\n%s", all)
	}

	c, f, _ = newFakePG()
	f.fail["ALTER ROLE"] = pgErr(pgInsufficientPriv)

	if err := c.CreateUser(t.Context(), "shop", "shop_user", pw); !errors.Is(err, ErrAccess) ||
		f.stmts[len(f.stmts)-1] != `DROP ROLE IF EXISTS "shop_user"` {
		t.Fatalf("got %v, %v", err, f.stmts)
	}

	for _, bad := range [][3]string{{"shop", "postgres", pw}, {"shop", "shop_user", "short"}, {"pg_x", "shop_user", pw}} {
		if err := c.CreateUser(t.Context(), bad[0], bad[1], bad[2]); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: %v", bad, err)
		}
	}
}

func TestPGDropUser(t *testing.T) {
	c, f, in := newFakePG()

	if err := c.DropUser(t.Context(), "shop", "shop_user"); err != nil {
		t.Fatal(err)
	}

	if len(f.stmts) != 3 || f.stmts[0] != `ALTER ROLE "shop_user" NOLOGIN` || !strings.Contains(f.stmts[1], "pg_terminate_backend") ||
		f.stmts[2] != `DROP ROLE IF EXISTS "shop_user"` {
		t.Fatalf("statements: %v", f.stmts)
	}

	if strings.Join(in.stmts, "\n") != "-- in shop\nREASSIGN OWNED BY \"shop_user\" TO \""+OwnerRole("shop")+"\"\nDROP OWNED BY \"shop_user\"" {
		t.Fatalf("in the database: %v", in.stmts)
	}

	// A missing user is not an error and touches nothing else.
	c, f, in = newFakePG()
	f.fail["ALTER ROLE"] = pgErr(pgUndefinedObject)

	if err := c.DropUser(t.Context(), "shop", "shop_user"); err != nil || len(f.stmts) != 1 || len(in.stmts) != 0 {
		t.Fatalf("missing user: %v, %v, %v", err, f.stmts, in.stmts)
	}

	// A database that is already gone still drops the role.
	c, f, in = newFakePG()
	in.fail["REASSIGN"] = pgErr(pgInvalidCatalog)

	if err := c.DropUser(t.Context(), "shop", "shop_user"); err != nil || f.stmts[len(f.stmts)-1] != `DROP ROLE IF EXISTS "shop_user"` {
		t.Fatalf("missing database: %v, %v", err, f.stmts)
	}
}

func TestPGDropDatabase(t *testing.T) {
	c, f, _ := newFakePG()

	if err := c.DropDatabase(t.Context(), "shop"); err != nil {
		t.Fatal(err)
	}

	if strings.Join(f.stmts, "\n") != `DROP DATABASE IF EXISTS "shop" WITH (FORCE)`+"\nDROP ROLE IF EXISTS \""+OwnerRole("shop")+`"` {
		t.Fatalf("statements: %v", f.stmts)
	}
}

func TestPGJobUser(t *testing.T) {
	const (
		user = "hostashi_job_0123456789abcdef"
		pw   = "Abcdefghijklmnopqrstuvwxyz012345"
	)

	c, f, _ := newFakePG()
	c.Now = func() time.Time { return time.Date(2026, 11, 21, 10, 0, 0, 0, time.FixedZone("x", 3600)) }

	if err := c.CreateJobUser(t.Context(), "shop", user, pw); err != nil {
		t.Fatal(err)
	}

	// 09:00 UTC plus 6 h 30 min.
	if !strings.Contains(f.stmts[0], ` LOGIN PASSWORD 'SCRAM-SHA-256$4096:`) ||
		!strings.HasSuffix(f.stmts[0], `' CONNECTION LIMIT 2 VALID UNTIL '2026-11-21 15:30:00+00' IN ROLE "`+OwnerRole("shop")+`"`) ||
		strings.Contains(strings.Join(f.stmts, "\n"), pw) {
		t.Fatalf("create: %v", f.stmts)
	}

	if f.stmts[2] != `ALTER ROLE "`+user+`" SET role = '`+OwnerRole("shop")+`'` {
		t.Fatalf("set role: %v", f.stmts)
	}

	for _, bad := range [][3]string{{"shop", "shop_user", pw}, {"shop", user, "short"}, {"shop", user, ""}, {"pg_x", user, pw}} {
		if err := c.CreateJobUser(t.Context(), bad[0], bad[1], bad[2]); !errors.Is(err, ErrInvalid) {
			t.Errorf("%v: %v", bad, err)
		}
	}

	c, f, in := newFakePG()

	if err := c.DropJobUser(t.Context(), "shop", user); err != nil {
		t.Fatal(err)
	}

	if f.stmts[0] != `ALTER ROLE "`+user+`" NOLOGIN` || f.stmts[len(f.stmts)-1] != `DROP ROLE IF EXISTS "`+user+`"` ||
		in.stmts[0] != "-- in shop" {
		t.Fatalf("drop: %v, %v", f.stmts, in.stmts)
	}

	// Only job roles: never a site's user or another role.
	for _, u := range []string{"shop_user", "postgres", "hostashi_db_0123456789abcdef"} {
		if err := c.DropJobUser(t.Context(), "shop", u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", u, err)
		}
	}
}

func TestPGToolUser(t *testing.T) {
	const (
		user = "hostashi_tool_0123456789abcdef"
		pw   = "Abcdefghijklmnopqrstuvwxyz012345"
	)

	c, f, _ := newFakePG()
	ctx := t.Context()

	if err := c.CreateToolUser(ctx, "shop", user, pw); err != nil {
		t.Fatal(err)
	}

	all := strings.Join(f.stmts, "\n")
	if strings.Contains(all, pw) || !strings.Contains(f.stmts[0], "LOGIN PASSWORD 'SCRAM-SHA-256$4096:") ||
		!strings.Contains(f.stmts[0], " CONNECTION LIMIT 2 IN ROLE \""+OwnerRole("shop")+`"`) ||
		f.stmts[2] != `ALTER ROLE "`+user+`" SET role = '`+OwnerRole("shop")+`'` {
		t.Fatalf("statements:\n%s", all)
	}

	c, f, in := newFakePG()
	if err := c.DropToolUser(ctx, "shop", user); err != nil {
		t.Fatal(err)
	}

	if len(f.stmts) != 3 || f.stmts[0] != `ALTER ROLE "`+user+`" NOLOGIN` || f.stmts[2] != `DROP ROLE IF EXISTS "`+user+`"` ||
		!strings.Contains(strings.Join(in.stmts, "\n"), "REASSIGN OWNED BY \""+user+"\" TO") {
		t.Fatalf("statements: %v, %v", f.stmts, in.stmts)
	}

	// Only the tool's own users, its own passwords and valid databases;
	// nothing reaches the server.
	c, f, _ = newFakePG()

	for name, err := range map[string]error{
		"site user":   c.CreateToolUser(ctx, "shop", "shop_user", pw),
		"job user":    c.CreateToolUser(ctx, "shop", "hostashi_job_0123456789abcdef", pw),
		"short pw":    c.CreateToolUser(ctx, "shop", user, "Abcdefghijklmnop1234"),
		"no pw":       c.CreateToolUser(ctx, "shop", user, ""),
		"quote pw":    c.CreateToolUser(ctx, "shop", user, "Abcdefghijklmnopqrstuvwxyz01234'"),
		"reserved db": c.CreateToolUser(ctx, "postgres", user, pw),
		"pg_ db":      c.CreateToolUser(ctx, "pg_shop", user, pw),
		"drop site":   c.DropToolUser(ctx, "shop", "shop_user"),
		"drop db":     c.DropToolUser(ctx, "template1", user),
	} {
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if len(f.stmts) != 0 {
		t.Fatalf("statements sent: %v", f.stmts)
	}
}

func TestClassifyPG(t *testing.T) {
	for code, want := range map[string]error{
		pgDuplicateDatabase: ErrExists, pgDuplicateObject: ErrExists, pgInsufficientPriv: ErrAccess,
		pgInvalidPassword: ErrAccess, pgUndefinedObject: ErrNotFound, pgInvalidCatalog: ErrNotFound,
	} {
		if err := classifyPG("x", pgErr(code)); !errors.Is(err, want) || strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: %v", code, err)
		}
	}

	if err := classifyPG("x", pgErr("XX000")); strings.Contains(err.Error(), "secret") || !strings.Contains(err.Error(), "XX000") {
		t.Errorf("other: %v", err)
	}

	if err := classifyPG("x", context.DeadlineExceeded); !errors.Is(err, ErrUnavailable) {
		t.Errorf("timeout: %v", err)
	}
}

func TestPostgresConfigIgnoresEnvironment(t *testing.T) {
	t.Setenv("PGHOST", "evil.example")
	t.Setenv("PGPORT", "1")
	t.Setenv("PGUSER", "evil")
	t.Setenv("PGSSLMODE", "prefer")
	t.Setenv("PGOPTIONS", "-c log_statement=none")
	t.Setenv("PGAPPNAME", "evil")

	cfg := validConfig()
	cfg.Engine, cfg.Port = EnginePostgreSQL, 5432

	for _, mode := range []TLSMode{TLSVerify, TLSNone} {
		cfg.TLS = mode

		pc, err := postgresConfig(cfg, "shop")
		if err != nil {
			t.Fatal(err)
		}

		if pc.Host != cfg.Host || pc.Port != 5432 || pc.User != cfg.AdminUser || pc.Password != cfg.AdminPassword ||
			pc.Database != "shop" || len(pc.Fallbacks) != 0 || len(pc.RuntimeParams) != 2 ||
			pc.RuntimeParams["application_name"] != "hostashi" || pc.RuntimeParams["statement_timeout"] != "30000" {
			t.Fatalf("%s: %+v", mode, pc.Config)
		}

		if (pc.TLSConfig == nil) != (mode == TLSNone) || (pc.TLSConfig != nil && pc.TLSConfig.ServerName != cfg.Host) {
			t.Fatalf("%s: TLS %+v", mode, pc.TLSConfig)
		}
	}

	db, err := Open(cfg)
	if err != nil {
		t.Fatal(err)
	}

	_ = db.Close()
}
