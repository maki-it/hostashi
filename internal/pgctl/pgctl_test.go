// SPDX-License-Identifier: AGPL-3.0-or-later

package pgctl

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func base() Run {
	return Run{
		Major: "19", Database: "shop", UID: 1000, GID: 1000, User: "site1", Password: "Secret-pass_12345",
		Host: "db.example.com", Port: 5432, TLS: "verify", TempDir: "/tmp",
	}
}

func TestEnvironment(t *testing.T) {
	env, err := environment(base())
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"PGHOST=db.example.com", "PGPORT=5432", "PGDATABASE=shop", "PGUSER=site1", "PGSSLMODE=verify-full", "PGSSLROOTCERT=" + SystemCAFile, "HOME=/nonexistent", "PGSERVICEFILE=/nonexistent"} {
		if !slices.Contains(env, want) {
			t.Errorf("%s missing in %v", want, env)
		}
	}

	for _, kv := range env {
		if strings.Contains(kv, "Secret") || strings.HasPrefix(kv, "PGPASSWORD") {
			t.Errorf("password in the environment: %s", kv)
		}
	}

	r := base()
	r.TLS, r.CAFile = "verify-ca", "/etc/hostashi/db-servers/1.pem"

	if env, err := environment(r); err != nil || !slices.Contains(env, "PGSSLMODE=verify-ca") || !slices.Contains(env, "PGSSLROOTCERT=/etc/hostashi/db-servers/1.pem") {
		t.Errorf("verify-ca: %v %v", env, err)
	}

	r.TLS = "none"
	if env, err := environment(r); err != nil || !slices.Contains(env, "PGSSLMODE=disable") {
		t.Errorf("none: %v %v", env, err)
	}

	for name, mod := range map[string]func(*Run){
		"user with quote":   func(r *Run) { r.User = "a'b" },
		"user upper":        func(r *Run) { r.User = "Admin" },
		"empty user":        func(r *Run) { r.User = "" },
		"host with space":   func(r *Run) { r.Host = "a b" },
		"host conninfo":     func(r *Run) { r.Host = "x host=evil" },
		"host option":       func(r *Run) { r.Host = "-h" },
		"empty host":        func(r *Run) { r.Host = "" },
		"port 0":            func(r *Run) { r.Port = 0 },
		"port 70000":        func(r *Run) { r.Port = 70000 },
		"unknown TLS":       func(r *Run) { r.TLS = "prefer" },
		"verify-ca no file": func(r *Run) { r.TLS = "verify-ca" },
		"relative CA":       func(r *Run) { r.TLS, r.CAFile = "verify-ca", "ca.pem" },
	} {
		r := base()
		mod(&r)

		if _, err := environment(r); !errors.Is(err, ErrFailed) {
			t.Errorf("%s accepted", name)
		}
	}

	r = base()
	r.Host = "2001:db8::1"

	if _, err := environment(r); err != nil {
		t.Errorf("IPv6 address refused: %v", err)
	}
}

func TestPassfileLine(t *testing.T) {
	got, err := passfileLine("site1", `a:b\c d'e"f#g`)
	if err != nil || got != `*:*:*:site1:a\:b\\c d'e"f#g`+"\n" {
		t.Fatalf("%q %v", got, err)
	}

	for _, pw := range []string{"", "a\nb", "a\x00b", "tab\there"} {
		if _, err := passfileLine("site1", pw); !errors.Is(err, ErrFailed) {
			t.Errorf("%q accepted", pw)
		}
	}
}

func TestWritePassfile(t *testing.T) {
	r := base()
	r.TempDir = t.TempDir()
	r.UID, r.GID = uint32(os.Getuid()), uint32(os.Getgid())

	name, err := writePassfile(r)
	if err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(name)
	if err != nil || st.Mode().Perm() != 0o600 || filepath.Dir(name) != r.TempDir {
		t.Fatalf("%v %v", st, err)
	}

	b, _ := os.ReadFile(name)
	if string(b) != "*:*:*:site1:Secret-pass_12345\n" {
		t.Fatalf("%q", b)
	}

	r.TempDir = "relative"
	if _, err := writePassfile(r); !errors.Is(err, ErrFailed) {
		t.Errorf("relative temp dir: %v", err)
	}
}

func TestBinary(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, LibDir, "19", "bin")

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "pg_dump"), []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "psql"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	if p, err := Binary(root, "19", "pg_dump"); err != nil || p != filepath.Join(dir, "pg_dump") {
		t.Fatalf("%q %v", p, err)
	}

	for _, tc := range [][2]string{{"19", "psql"}, {"19", "pg_restore"}, {"18", "pg_dump"}, {"19", "sh"}, {"../19", "pg_dump"}, {"019", "pg_dump"}, {"", "pg_dump"}, {"19", "../pg_dump"}} {
		if _, err := Binary(root, tc[0], tc[1]); !errors.Is(err, ErrFailed) {
			t.Errorf("%v accepted", tc)
		}
	}
}

func TestRunRefuses(t *testing.T) {
	ctx := context.Background()

	r := base()
	r.UID, r.GID = 0, 0

	if err := Dump(ctx, r, &bytes.Buffer{}); !errors.Is(err, ErrFailed) || !strings.Contains(err.Error(), "root") {
		t.Fatalf("root: %v", err)
	}

	r = base()
	r.Database = "--all"

	if err := Dump(ctx, r, &bytes.Buffer{}); !errors.Is(err, ErrFailed) {
		t.Fatalf("option as name: %v", err)
	}

	for _, role := range []string{"", "a b", "x;drop", `"x"`} {
		if err := Restore(ctx, base(), role, strings.NewReader("")); !errors.Is(err, ErrFailed) {
			t.Errorf("restore role %q accepted", role)
		}

		if err := Exec(ctx, base(), role, strings.NewReader("")); !errors.Is(err, ErrFailed) {
			t.Errorf("exec role %q accepted", role)
		}
	}
}

func TestDescribe(t *testing.T) {
	for stderr, want := range map[string]string{
		"psql:<stdin>:3: ERROR:  relation \"secret_table\" does not exist":                                                  "SQL error at line 3 of the file",
		"pg_dump: error: connection to server at \"x\" failed: FATAL:  password authentication failed for user \"u\"":       "the server refused the login",
		"pg_dump: error: connection to server at \"x\" (192.0.2.1), port 5432 failed: SSL error: certificate verify failed": "TLS connection failed",
		"pg_dump: error: connection to server at \"x\" (192.0.2.1), port 5432 failed: Connection refused":                   "cannot connect to the server",
		"pg_dump: error: aborting because of server version mismatch":                                                       "the server is newer than the client",
		"something with secret data": "exit code",
	} {
		err := describe(&fakeExitError{}, []byte(stderr))
		if err == nil || !strings.Contains(err.Error(), want) && want != "exit code" {
			t.Errorf("%q: %v", stderr, err)
		}

		if strings.Contains(err.Error(), "secret") {
			t.Errorf("stderr leaked: %v", err)
		}
	}
}

type fakeExitError struct{}

func (*fakeExitError) Error() string { return "exit status 1" }

// TestAgainstServer runs the tools against a real server (CI job
// "PostgreSQL client"). The server has the role site1 (password in
// HOSTASHI_TEST_PG_PW) owning the empty databases shop and shopcopy; the
// test runs as root and the tools as HOSTASHI_TEST_PG_UID.
func TestAgainstServer(t *testing.T) {
	addr := os.Getenv("HOSTASHI_TEST_PG")
	if addr == "" {
		t.Skip("HOSTASHI_TEST_PG not set")
	}

	host, port, _ := strings.Cut(addr, ":")
	p, _ := strconv.Atoi(port)
	uid, _ := strconv.Atoi(os.Getenv("HOSTASHI_TEST_PG_UID"))
	ctx := context.Background()

	r := Run{
		Major: os.Getenv("HOSTASHI_TEST_PG_MAJOR"), Database: "shop", UID: uint32(uid), GID: uint32(uid),
		User: "site1", Password: os.Getenv("HOSTASHI_TEST_PG_PW"), Host: host, Port: p,
		TLS: os.Getenv("HOSTASHI_TEST_PG_TLS"), CAFile: os.Getenv("HOSTASHI_TEST_PG_CA"),
	}

	// The tool's user must reach the passfile; t.TempDir's parent is 0700.
	dir, err := os.MkdirTemp("", "pgctl-")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	if err := os.Chmod(dir, 0o1777); err != nil {
		t.Fatal(err)
	}

	r.TempDir = dir

	sql := "CREATE TABLE t (id int PRIMARY KEY, s text);\nINSERT INTO t VALUES (1, 'it''s'), (2, E'back\\\\slash');\n"
	if err := Exec(ctx, r, "site1", strings.NewReader(sql)); err != nil {
		t.Fatalf("exec: %v", err)
	}

	var dump bytes.Buffer
	if err := Dump(ctx, r, &dump); err != nil {
		t.Fatalf("dump: %v", err)
	}

	if !bytes.HasPrefix(dump.Bytes(), []byte("PGDMP")) {
		t.Fatalf("not a custom-format dump: %q", dump.Bytes()[:min(16, dump.Len())])
	}

	cp := r
	cp.Database = "shopcopy"

	// Twice: --clean --if-exists replaces what the first run created.
	for i := range 2 {
		if err := Restore(ctx, cp, "site1", bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatalf("restore %d: %v", i, err)
		}
	}

	check := "DO $$ BEGIN IF (SELECT count(*) FROM t WHERE s IN ('it''s', E'back\\\\slash')) <> 2 THEN RAISE EXCEPTION 'rows'; END IF; END $$;\n"
	if err := Exec(ctx, cp, "site1", strings.NewReader(check)); err != nil {
		t.Fatalf("restored rows: %v", err)
	}

	// A plain dump loads with Exec into the other database: no owners, no
	// grants, and from a newer pg_dump the preamble line an older server
	// refuses is dropped.
	var plain bytes.Buffer
	if err := DumpSQL(ctx, r, &plain); err != nil {
		t.Fatalf("plain dump: %v", err)
	}

	if bytes.Contains(plain.Bytes(), []byte("OWNER TO")) || bytes.Contains(plain.Bytes(), []byte("GRANT ")) {
		t.Fatal("plain dump has owners or grants")
	}

	if err := Exec(ctx, cp, "site1", strings.NewReader("DROP TABLE t;\n")); err != nil {
		t.Fatalf("drop before plain load: %v", err)
	}

	if err := Exec(ctx, cp, "site1", bytes.NewReader(plain.Bytes())); err != nil {
		t.Fatalf("plain load: %v", err)
	}

	if err := Exec(ctx, cp, "site1", strings.NewReader(check)); err != nil {
		t.Fatalf("rows after plain load: %v", err)
	}

	// The first error stops the file and rolls it back.
	bad := "CREATE TABLE u (a int);\nSELECT 1;\nSELECT * FROM missing_table;\n"
	if err := Exec(ctx, r, "site1", strings.NewReader(bad)); err == nil || !strings.Contains(err.Error(), "line 4") {
		// Line 1 is the SET ROLE that Exec puts first.
		t.Fatalf("bad file: %v", err)
	}

	if err := Exec(ctx, r, "site1", strings.NewReader("DO $$ BEGIN IF to_regclass('u') IS NOT NULL THEN RAISE EXCEPTION 'kept'; END IF; END $$;\n")); err != nil {
		t.Fatalf("failed file was not rolled back: %v", err)
	}

	wrong := r
	wrong.Password = "wrong:pass"

	if err := Dump(ctx, wrong, &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "refused the login") {
		t.Fatalf("wrong password: %v", err)
	}

	if left, _ := filepath.Glob(filepath.Join(r.TempDir, "pgpass-*")); len(left) != 0 {
		t.Fatalf("passfiles left: %v", left)
	}
}

func TestPreambleFilter(t *testing.T) {
	script := "--\n-- PostgreSQL database dump\n--\n\n\\restrict abc123\n\nSET statement_timeout = 0;\nSET transaction_timeout = 0;\nSELECT pg_catalog.set_config('search_path', '', false);\n\nCREATE TABLE public.t (s text);\nSET transaction_timeout = 0;\n"
	want := strings.Replace(script, "SET transaction_timeout = 0;\n", "", 1)

	for _, size := range []int{1, 3, 7, 64, 4096} {
		var out bytes.Buffer

		f := &preambleFilter{w: &out}

		for b := []byte(script); len(b) > 0; {
			k := min(size, len(b))
			if n, err := f.Write(b[:k]); err != nil || n != k {
				t.Fatalf("write: %d %v", n, err)
			}

			b = b[k:]
		}

		if out.String() != want {
			t.Errorf("chunks of %d: %q", size, out.String())
		}
	}
}
