// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/mariadbctl"
)

// fakeTools records what the client programs would have got.
type fakeTools struct {
	mu       sync.Mutex
	imported string
	runs     []mariadbctl.Run
	err      error
	block    chan struct{}
}

func (f *fakeTools) Import(_ context.Context, r mariadbctl.Run, in io.Reader) error {
	if f.block != nil {
		<-f.block
	}

	b, _ := io.ReadAll(in)

	f.mu.Lock()
	defer f.mu.Unlock()

	f.imported = string(b)
	f.runs = append(f.runs, r)

	return f.err
}

func (f *fakeTools) Export(_ context.Context, r mariadbctl.Run, out io.Writer) error {
	f.mu.Lock()
	f.runs = append(f.runs, r)
	f.mu.Unlock()

	_, _ = io.WriteString(out, "-- dump of "+r.Database+"\n")

	return f.err
}

type dumpEnv struct {
	m     *Manager
	db    *fakeDB
	tools *fakeTools
	home  string
}

func newDumpEnv(t *testing.T) *dumpEnv {
	t.Helper()

	home := filepath.Join(t.TempDir(), "shop")
	if err := os.Mkdir(home, 0o750); err != nil {
		t.Fatal(err)
	}

	db := &fakeDB{}
	m := newManager(t, stateDir(t), db)
	tools := &fakeTools{}
	uid, gid := uint32(os.Getuid()), uint32(os.Getgid())

	m.EnableDumps(Dumps{Tools: tools, HomeOf: func(site string) (SiteHome, bool) {
		return SiteHome{Home: home, UID: uid, GID: gid}, site == "shop"
	}})

	if _, err := m.Create(t.Context(), "shop", "shop_wp"); err != nil {
		t.Fatal(err)
	}

	db.stmts = nil

	return &dumpEnv{m: m, db: db, tools: tools, home: home}
}

func (e *dumpEnv) finish(t *testing.T, j agentapi.DBJob) agentapi.DBJob {
	t.Helper()

	e.m.wait(j.ID)

	got, err := e.m.Job(j.ID)
	if err != nil {
		t.Fatal(err)
	}

	return got
}

func TestImportPlainAndGzip(t *testing.T) {
	e := newDumpEnv(t)

	if err := os.WriteFile(filepath.Join(e.home, "a.sql"), []byte("CREATE TABLE t (i int);"), 0o600); err != nil {
		t.Fatal(err)
	}

	var gz bytes.Buffer

	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte("INSERT 1;"))
	_ = zw.Close()

	if err := os.MkdirAll(filepath.Join(e.home, "backups"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(e.home, "backups", "b.sql.gz"), gz.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}

	for path, want := range map[string]string{"a.sql": "CREATE TABLE t (i int);", "backups/b.sql.gz": "INSERT 1;"} {
		e.db.stmts = nil

		j, err := e.m.StartImport("shop_wp", path, false)
		if err != nil || j.State != agentapi.JobRunning || len(j.ID) != 16 {
			t.Fatalf("%s: %+v %v", path, j, err)
		}

		if j = e.finish(t, j); j.State != agentapi.JobDone || e.tools.imported != want {
			t.Fatalf("%s: %+v imported %q", path, j, e.tools.imported)
		}

		run := e.tools.runs[len(e.tools.runs)-1]
		if !jobUserRe.MatchString(run.User) || run.UID != uint32(os.Getuid()) || run.Database != "shop_wp" {
			t.Fatalf("run %+v", run)
		}

		// A temporary user only for this database, dropped afterwards.
		want := []string{
			"CREATE USER '" + run.User + "'@'localhost' IDENTIFIED BY '" + run.Password + "'",
			"GRANT ALL PRIVILEGES ON `shop\\_wp`.* TO '" + run.User + "'@'localhost'",
			"DROP USER IF EXISTS '" + run.User + "'@'localhost'",
		}
		if strings.Join(e.db.stmts, "\n") != strings.Join(want, "\n") {
			t.Fatalf("statements:\n%s", strings.Join(e.db.stmts, "\n"))
		}
	}
}

func TestImportRefusesUnsafeFiles(t *testing.T) {
	e := newDumpEnv(t)
	outside := filepath.Join(t.TempDir(), "secret.sql")

	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(outside, filepath.Join(e.home, "link.sql")); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(e.home, "dir.sql"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Dir(outside), filepath.Join(e.home, "out")); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"missing.sql", "link.sql", "dir.sql", "out/secret.sql"} {
		if _, err := e.m.StartImport("shop_wp", p, false); !errors.Is(err, ErrFileUnsafe) {
			t.Errorf("%s: %v", p, err)
		}
	}

	for _, p := range []string{"../secret.sql", "/etc/shadow.sql", "x.txt"} {
		if _, err := e.m.StartImport("shop_wp", p, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", p, err)
		}
	}

	if _, err := e.m.StartImport("other", "a.sql", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown database: %v", err)
	}

	if len(e.db.stmts) != 0 || len(e.tools.runs) != 0 {
		t.Fatalf("refused requests did something: %q %d", e.db.stmts, len(e.tools.runs))
	}
}

func TestImportFailureAndBusy(t *testing.T) {
	e := newDumpEnv(t)

	if err := os.WriteFile(filepath.Join(e.home, "a.sql"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	e.tools.block = make(chan struct{})
	e.tools.err = errors.New("mariadbctl: failed: server error 1044 at line 2 of the dump")
	e.tools.err = &failedError{e.tools.err}

	j, err := e.m.StartImport("shop_wp", "a.sql", false)
	if err != nil {
		t.Fatal(err)
	}

	// One job per database, and the database cannot go away meanwhile.
	if _, err := e.m.StartImport("shop_wp", "a.sql", false); !errors.Is(err, ErrBusy) {
		t.Fatalf("second job: %v", err)
	}

	if err := e.m.Delete(t.Context(), "shop_wp"); !errors.Is(err, ErrBusy) {
		t.Fatalf("delete while busy: %v", err)
	}

	close(e.tools.block)

	j = e.finish(t, j)
	if j.State != agentapi.JobFailed || j.Error != "server error 1044 at line 2 of the dump" {
		t.Fatalf("%+v", j)
	}

	if last := e.db.stmts[len(e.db.stmts)-1]; !strings.HasPrefix(last, "DROP USER IF EXISTS 'hostashi_job_") {
		t.Fatalf("job user not dropped: %q", last)
	}

	if err := e.m.Delete(t.Context(), "shop_wp"); err != nil {
		t.Fatal(err)
	}
}

// failedError makes an error that is mariadbctl.ErrFailed like the real ones.
type failedError struct{ err error }

func (w *failedError) Error() string { return w.err.Error() }
func (w *failedError) Unwrap() error { return mariadbctl.ErrFailed }

func TestExport(t *testing.T) {
	e := newDumpEnv(t)

	j, err := e.m.StartExport("shop_wp", "shop.sql.gz")
	if err != nil {
		t.Fatal(err)
	}

	if j = e.finish(t, j); j.State != agentapi.JobDone {
		t.Fatalf("%+v", j)
	}

	p := filepath.Join(e.home, "shop.sql.gz")

	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("%v %v", st, err)
	}

	f, _ := os.Open(p)
	defer f.Close()

	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}

	if b, _ := io.ReadAll(zr); string(b) != "-- dump of shop_wp\n" {
		t.Fatalf("%q", b)
	}

	// Never replaces a file.
	if _, err := e.m.StartExport("shop_wp", "shop.sql.gz"); !errors.Is(err, ErrFileExists) {
		t.Fatalf("existing file: %v", err)
	}

	if _, err := e.m.StartExport("shop_wp", "nodir/x.sql"); !errors.Is(err, ErrFileUnsafe) {
		t.Fatalf("missing directory: %v", err)
	}

	// A failed export leaves no partial file.
	e.tools.err = &failedError{errors.New("mariadbctl: failed: exit code 2")}

	j, err = e.m.StartExport("shop_wp", "fail.sql")
	if err != nil {
		t.Fatal(err)
	}

	if j = e.finish(t, j); j.State != agentapi.JobFailed || j.Error != "exit code 2" {
		t.Fatalf("%+v", j)
	}

	if _, err := os.Stat(filepath.Join(e.home, "fail.sql")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial export left: %v", err)
	}
}

func TestDumpsDisabled(t *testing.T) {
	m := newManager(t, stateDir(t), &fakeDB{})

	if _, err := m.Create(t.Context(), "shop", "shop"); err != nil {
		t.Fatal(err)
	}

	if _, err := m.StartImport("shop", "a.sql", false); !errors.Is(err, ErrNoDumps) {
		t.Fatalf("got %v", err)
	}

	if _, err := m.Job("0123456789abcdef"); !errors.Is(err, ErrJobNotFound) {
		t.Fatalf("got %v", err)
	}
}
