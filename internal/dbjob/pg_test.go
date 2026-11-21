// SPDX-License-Identifier: AGPL-3.0-or-later

package dbjob

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/pgctl"
)

// fakePG stands in for pg_dump, pg_restore and psql.
type fakePG struct {
	calls    []string
	run      pgctl.Run
	role     string
	imported []byte
	dump     string
	fail     error
	caPEM    string
	dirMode  os.FileMode
}

func (f *fakePG) note(call string, r pgctl.Run, role string) {
	f.calls, f.run, f.role = append(f.calls, call), r, role

	if st, err := os.Stat(r.TempDir); err == nil {
		f.dirMode = st.Mode().Perm()
	}

	if r.CAFile != "" {
		b, _ := os.ReadFile(r.CAFile)
		f.caPEM = string(b)
	}
}

func (f *fakePG) write(out io.Writer) error {
	if _, err := io.WriteString(out, f.dump); err != nil {
		return err
	}

	return f.fail
}

func (f *fakePG) read(in io.Reader) error {
	b, err := io.ReadAll(in)
	f.imported = b

	if err != nil {
		return err
	}

	return f.fail
}

func (f *fakePG) Dump(_ context.Context, r pgctl.Run, out io.Writer) error {
	f.note("dump", r, "")

	return f.write(out)
}

func (f *fakePG) DumpSQL(_ context.Context, r pgctl.Run, out io.Writer) error {
	f.note("dumpsql", r, "")

	return f.write(out)
}

func (f *fakePG) Restore(_ context.Context, r pgctl.Run, role string, in io.Reader) error {
	f.note("restore", r, role)

	return f.read(in)
}

func (f *fakePG) Exec(_ context.Context, r pgctl.Run, role string, in io.Reader) error {
	f.note("exec", r, role)

	return f.read(in)
}

func pgRequest(kind, path string) agentapi.DBRemoteJobRequest {
	req := request()
	req.Engine, req.Kind, req.Path, req.Port = agentapi.RemoteEnginePostgreSQL, kind, path, 5432

	return req
}

// pgRoot makes a fake /usr/lib/postgresql with the given majors.
func pgRoot(t *testing.T, majors ...string) string {
	t.Helper()

	root := t.TempDir()

	for _, m := range majors {
		bin := filepath.Join(root, "usr/lib/postgresql", m, "bin")
		must(t, os.MkdirAll(bin, 0o755))
		must(t, os.WriteFile(filepath.Join(bin, "pg_dump"), []byte("#!/bin/false\n"), 0o755))
	}

	return root
}

func startPG(t *testing.T, req agentapi.DBRemoteJobRequest) (*Agent, RunConfig, string, *fakePG, string) {
	t.Helper()

	a, cfg, id, _, home := startJob(t, req)
	pg := &fakePG{}
	cfg.PGTools, cfg.PGRoot = pg, pgRoot(t, "17", "19")

	return a, cfg, id, pg, home
}

func siteFile(t *testing.T, home, name string, data []byte) {
	t.Helper()

	p := filepath.Join(home, name)
	must(t, os.WriteFile(p, data, 0o600))
	must(t, os.Chown(p, siteUID, siteUID))
}

func TestRunPGExport(t *testing.T) {
	needRoot(t)

	for _, tc := range []struct{ path, call string }{
		{"blog.dump", "dump"}, {"blog.sql", "dumpsql"}, {"out/blog.sql.gz", "dumpsql"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			a, cfg, id, pg, home := startPG(t, pgRequest("export", tc.path))
			pg.dump = "PGDMP data"

			if strings.HasPrefix(tc.path, "out/") {
				must(t, os.Mkdir(filepath.Join(home, "out"), 0o755))
				must(t, os.Chown(filepath.Join(home, "out"), siteUID, siteUID))
			}

			must(t, RunOnce(t.Context(), cfg, id))

			r, err := readRecord(a.Dir, id, owner())
			must(t, err)

			if r.State != agentapi.JobDone || r.Request != nil {
				t.Fatalf("record = %+v", r)
			}

			rn := pg.run
			if len(pg.calls) != 1 || pg.calls[0] != tc.call || rn.Major != "19" || rn.Database != "blog_db" || rn.UID != siteUID ||
				rn.GID != siteUID || rn.User != "hostashi_job_0123456789abcdef" || rn.Host != "db.example.net" || rn.Port != 5432 ||
				rn.TLS != "verify" || rn.CAFile != "" || rn.Root != cfg.PGRoot {
				t.Fatalf("calls %v, run %+v", pg.calls, rn)
			}

			// The passfile directory is the site user's alone, and gone
			// after the job.
			if pg.dirMode != 0o700 {
				t.Errorf("temp dir mode %v", pg.dirMode)
			}

			if _, err := os.Stat(rn.TempDir); !os.IsNotExist(err) {
				t.Errorf("temp dir kept: %v", err)
			}

			b, err := os.ReadFile(filepath.Join(home, tc.path))
			must(t, err)

			if strings.HasSuffix(tc.path, ".gz") {
				zr, err := gzip.NewReader(bytes.NewReader(b))
				must(t, err)

				b, err = io.ReadAll(zr)
				must(t, err)
			}

			if string(b) != pg.dump {
				t.Errorf("file = %q", b)
			}
		})
	}
}

func TestRunPGImport(t *testing.T) {
	needRoot(t)

	var gz bytes.Buffer

	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("INSERT INTO t VALUES (1);\n"))
	must(t, zw.Close())

	for _, tc := range []struct {
		path, call string
		data       []byte
	}{
		{"blog.dump", "restore", []byte("PGDMP")},
		{"blog.sql", "exec", []byte("INSERT INTO t VALUES (1);\n")},
		{"blog.sql.gz", "exec", gz.Bytes()},
	} {
		t.Run(tc.path, func(t *testing.T) {
			req := pgRequest("import", tc.path)
			req.TLS, req.CAPEM = "verify-ca", "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n"

			_, cfg, id, pg, home := startPG(t, req)
			siteFile(t, home, tc.path, tc.data)

			must(t, RunOnce(t.Context(), cfg, id))

			want := string(tc.data)
			if tc.path == "blog.sql.gz" {
				want = "INSERT INTO t VALUES (1);\n"
			}

			if len(pg.calls) != 1 || pg.calls[0] != tc.call || pg.role != dbserver.OwnerRole("blog_db") || string(pg.imported) != want {
				t.Fatalf("calls %v, role %q, imported %q", pg.calls, pg.role, pg.imported)
			}

			if pg.caPEM != req.CAPEM || pg.run.TLS != "verify-ca" || filepath.Dir(pg.run.CAFile) != pg.run.TempDir {
				t.Errorf("CA %q, run %+v", pg.caPEM, pg.run)
			}
		})
	}
}

func TestRunPGFails(t *testing.T) {
	needRoot(t)

	// A failed export leaves no file and shows pgctl's fixed text.
	a, cfg, id, pg, home := startPG(t, pgRequest("export", "blog.dump"))
	pg.dump = "partial"
	pg.fail = errFixed

	if err := RunOnce(t.Context(), cfg, id); !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v", err)
	}

	r, err := readRecord(a.Dir, id, owner())
	must(t, err)

	if r.State != agentapi.JobFailed || r.Error != "the server refused the login" {
		t.Errorf("record = %+v", r)
	}

	if _, err := os.Stat(filepath.Join(home, "blog.dump")); !os.IsNotExist(err) {
		t.Errorf("partial export kept: %v", err)
	}

	// Without an installed client nothing runs.
	a, cfg, id, pg, _ = startPG(t, pgRequest("export", "blog.dump"))
	cfg.PGRoot = pgRoot(t)

	if err := RunOnce(t.Context(), cfg, id); !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v", err)
	}

	r, err = readRecord(a.Dir, id, owner())
	must(t, err)

	if r.Error != "no PostgreSQL client is installed" || len(pg.calls) != 0 {
		t.Errorf("record = %+v, calls %v", r, pg.calls)
	}
}

var errFixed = wrapFailed("the server refused the login")

func wrapFailed(s string) error { return &fixedError{s} }

type fixedError struct{ s string }

func (e *fixedError) Error() string { return "pgctl: failed: " + e.s }
func (e *fixedError) Unwrap() error { return pgctl.ErrFailed }

func TestPGRequestPaths(t *testing.T) {
	for path, ok := range map[string]bool{"a.dump": true, "a.sql": true, "a.sql.gz": true, "a.dump.gz": false, "a.zip": false} {
		if err := pgRequest("import", path).Validate(); (err == nil) != ok {
			t.Errorf("postgresql %s: %v", path, err)
		}
	}

	// MariaDB has no custom format.
	req := request()
	req.Path = "a.dump"

	if req.Validate() == nil {
		t.Error("MariaDB took a .dump")
	}

	req.Engine = "oracle"
	req.Path = "a.sql"

	if req.Validate() == nil {
		t.Error("unknown engine accepted")
	}
}
