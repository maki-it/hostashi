// SPDX-License-Identifier: AGPL-3.0-or-later

package dbjob

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/mariadbctl"
)

type fakeUnits struct{ calls []string }

func (f *fakeUnits) Reload(context.Context) error { f.calls = append(f.calls, "reload"); return nil }

func (f *fakeUnits) StartDBJob(_ context.Context, id string) error {
	f.calls = append(f.calls, "start "+id)

	return nil
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

const siteUID = 2000

// writeRegistry writes a registry with the site blog (uid 2000) in pool
// state/pool and returns the site's home, created and owned by the site
// user when the test runs as root.
func writeRegistry(t *testing.T, state string) string {
	t.Helper()

	pool := filepath.Join(state, "pool")
	must(t, os.MkdirAll(pool, 0o755))

	raw, err := json.Marshal(map[string]any{
		"version": 1,
		"pools":   []map[string]string{{"name": "default", "path": pool}},
		"sites":   []map[string]any{{"user": "blog", "uid": siteUID, "gid": siteUID, "pool": "default"}},
	})
	must(t, err)
	must(t, os.WriteFile(filepath.Join(state, "sites.json"), raw, 0o600))

	home := filepath.Join(pool, "blog")
	must(t, os.Mkdir(home, 0o750))

	if os.Geteuid() == 0 {
		must(t, os.Chown(home, siteUID, siteUID))
	}

	return home
}

func newAgent(t *testing.T) (*Agent, *fakeUnits, string) {
	t.Helper()

	root := t.TempDir()
	state := filepath.Join(root, "state")
	must(t, os.Mkdir(state, 0o700))

	home := writeRegistry(t, state)
	u := &fakeUnits{}

	return &Agent{Dir: filepath.Join(state, "dbjobs"), StateDir: state, UnitDir: filepath.Join(root, "units"), Units: u}, u, home
}

func request() agentapi.DBRemoteJobRequest {
	return agentapi.DBRemoteJobRequest{
		Kind: "export", Site: "blog", Database: "blog_db", Path: "blog.sql",
		Host: "db.example.net", Port: 3306, Addrs: []string{"192.0.2.5", "2001:db8::5"}, TLS: "verify",
		User: "hostashi_job_0123456789abcdef", Password: "Abcdefghijklmnopqrstuvwxyz012345",
	}
}

func TestStart(t *testing.T) {
	a, u, home := newAgent(t)

	j, err := a.Start(t.Context(), request())
	must(t, err)

	if j.State != agentapi.JobRunning || j.Database != "blog_db" || j.Kind != "export" || !jobIDRe.MatchString(j.ID) {
		t.Fatalf("job = %+v", j)
	}

	if strings.Join(u.calls, ",") != "reload,start "+j.ID {
		t.Errorf("calls = %v", u.calls)
	}

	drop, err := os.ReadFile(a.dropIn(j.ID))
	must(t, err)

	hostsPath := filepath.Join(a.Dir, hostsFile(j.ID))
	for _, want := range []string{
		"ReadWritePaths=" + home + "\n",
		"IPAddressAllow=192.0.2.5 2001:db8::5\n",
		"BindReadOnlyPaths=" + hostsPath + ":/etc/hosts\n",
	} {
		if !strings.Contains(string(drop), want) {
			t.Errorf("drop-in lacks %q:\n%s", want, drop)
		}
	}

	hs, err := os.ReadFile(hostsPath)
	must(t, err)

	if !strings.Contains(string(hs), "192.0.2.5 db.example.net\n2001:db8::5 db.example.net\n") {
		t.Errorf("hosts:\n%s", hs)
	}

	if st, err := os.Stat(hostsPath); err != nil || st.Mode().Perm() != 0o644 {
		t.Errorf("hosts file mode: %v %v", st, err)
	}

	// The record keeps the request for the job; Get does not show it.
	r, err := readRecord(a.Dir, j.ID, owner())
	must(t, err)

	if r.Request == nil || r.Request.Password != request().Password || r.Site != "blog" {
		t.Errorf("record = %+v", r)
	}

	got, err := a.Get(j.ID)
	must(t, err)

	if got != j {
		t.Errorf("Get = %+v, want %+v", got, j)
	}
}

func TestStartRefused(t *testing.T) {
	a, _, _ := newAgent(t)

	bad := request()
	bad.Password = "short"

	if _, err := a.Start(t.Context(), bad); !errors.Is(err, ErrInvalid) {
		t.Errorf("invalid: %v", err)
	}

	other := request()
	other.Site = "shop"

	if _, err := a.Start(t.Context(), other); !errors.Is(err, ErrNoSite) {
		t.Errorf("no site: %v", err)
	}

	_, err := a.Start(t.Context(), request())
	must(t, err)

	if _, err := a.Start(t.Context(), request()); !errors.Is(err, ErrBusy) {
		t.Errorf("busy: %v", err)
	}

	if _, err := a.Get("0000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Errorf("not found: %v", err)
	}

	if _, err := a.Get("../x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad id: %v", err)
	}
}

func TestHostsForIP(t *testing.T) {
	out := hosts("192.0.2.5", nil)
	if strings.Count(out, "\n") != 3 || strings.Contains(out, "192.0.2.5") {
		t.Errorf("hosts = %q", out)
	}
}

func TestStaleAndPrune(t *testing.T) {
	a, _, _ := newAgent(t)

	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	a.Now = func() time.Time { return now }

	j, err := a.Start(t.Context(), request())
	must(t, err)

	// Long past the unit's limit: failed, and a new job may start.
	now = now.Add(MaxDuration + time.Hour)

	got, err := a.Get(j.ID)
	must(t, err)

	if got.State != agentapi.JobFailed || got.Error != "stopped" {
		t.Errorf("stale job = %+v", got)
	}

	j2, err := a.Start(t.Context(), request())
	must(t, err)

	if _, err := os.Stat(filepath.Dir(a.dropIn(j.ID))); !os.IsNotExist(err) {
		t.Errorf("old drop-in kept: %v", err)
	}

	if _, err := os.Stat(filepath.Join(a.Dir, hostsFile(j.ID))); !os.IsNotExist(err) {
		t.Errorf("old hosts file kept: %v", err)
	}

	if _, err := os.Stat(a.dropIn(j2.ID)); err != nil {
		t.Errorf("new drop-in: %v", err)
	}
}

// fakeTools stands in for mariadb and mariadb-dump.
type fakeTools struct {
	run      mariadbctl.Run
	imported []byte
	dump     string
	fail     error
	caPEM    string
}

func (f *fakeTools) Import(_ context.Context, r mariadbctl.Run, in io.Reader) error {
	f.run = r
	f.readCA(r)

	b, err := io.ReadAll(in)
	f.imported = b

	if err != nil {
		return err
	}

	return f.fail
}

func (f *fakeTools) Export(_ context.Context, r mariadbctl.Run, out io.Writer) error {
	f.run = r
	f.readCA(r)

	if _, err := io.WriteString(out, f.dump); err != nil {
		return err
	}

	return f.fail
}

func (f *fakeTools) readCA(r mariadbctl.Run) {
	if r.Remote != nil && r.Remote.CAFile != "" {
		b, _ := os.ReadFile(r.Remote.CAFile)
		f.caPEM = string(b)
	}
}

func needRoot(t *testing.T) {
	t.Helper()

	if os.Geteuid() != 0 {
		t.Skip("needs root to own files as the site user")
	}
}

// startJob records a job as the agent does and returns the run config.
func startJob(t *testing.T, req agentapi.DBRemoteJobRequest) (*Agent, RunConfig, string, *fakeTools, string) {
	t.Helper()

	a, _, home := newAgent(t)

	j, err := a.Start(t.Context(), req)
	must(t, err)

	tools := &fakeTools{}

	return a, RunConfig{Dir: a.Dir, StateDir: a.StateDir, TempDir: t.TempDir(), Tools: tools}, j.ID, tools, home
}

func TestRunExport(t *testing.T) {
	needRoot(t)

	req := request()
	req.Path = "blog.sql.gz"

	a, cfg, id, tools, home := startJob(t, req)
	tools.dump = "CREATE TABLE t (a int);\n"

	must(t, RunOnce(t.Context(), cfg, id))

	r, err := readRecord(a.Dir, id, owner())
	must(t, err)

	if r.State != agentapi.JobDone || r.Request != nil || r.Finished.IsZero() {
		t.Fatalf("record = %+v", r)
	}

	rn := tools.run
	if rn.Database != "blog_db" || rn.UID != siteUID || rn.User != req.User || rn.Password != req.Password ||
		rn.Remote == nil || rn.Remote.Host != "db.example.net" || rn.Remote.Port != 3306 || rn.Remote.TLS != "verify" || rn.Remote.CAFile != "" {
		t.Errorf("run = %+v remote %+v", rn, rn.Remote)
	}

	f, err := os.Open(filepath.Join(home, "blog.sql.gz"))
	must(t, err)

	defer f.Close()

	zr, err := gzip.NewReader(f)
	must(t, err)

	b, err := io.ReadAll(zr)
	must(t, err)

	if string(b) != tools.dump {
		t.Errorf("dump = %q", b)
	}

	// The job runs once only.
	if err := RunOnce(t.Context(), cfg, id); err == nil {
		t.Error("second run accepted")
	}
}

func TestRunExportFails(t *testing.T) {
	needRoot(t)

	a, cfg, id, tools, home := startJob(t, request())
	tools.dump = "partial"
	tools.fail = mariadbctl.ErrFailed

	if err := RunOnce(t.Context(), cfg, id); !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v", err)
	}

	r, err := readRecord(a.Dir, id, owner())
	must(t, err)

	if r.State != agentapi.JobFailed || r.Request != nil {
		t.Errorf("record = %+v", r)
	}

	if _, err := os.Stat(filepath.Join(home, "blog.sql")); !os.IsNotExist(err) {
		t.Errorf("partial export kept: %v", err)
	}
}

func TestRunImportVerifyCA(t *testing.T) {
	needRoot(t)

	req := request()
	req.Kind, req.TLS, req.CAPEM = "import", "verify-ca", "-----BEGIN CERTIFICATE-----\nx\n-----END CERTIFICATE-----\n"

	a, cfg, id, tools, home := startJob(t, req)

	buf := bytes.NewBufferString("INSERT INTO t VALUES (1);\n")

	p := filepath.Join(home, "blog.sql")
	must(t, os.WriteFile(p, buf.Bytes(), 0o600))
	must(t, os.Chown(p, siteUID, siteUID))

	must(t, RunOnce(t.Context(), cfg, id))

	if string(tools.imported) != buf.String() {
		t.Errorf("imported %q", tools.imported)
	}

	if tools.caPEM != req.CAPEM || tools.run.Remote.TLS != "verify-ca" {
		t.Errorf("CA %q, remote %+v", tools.caPEM, tools.run.Remote)
	}

	// The CA directory is gone after the job.
	if _, err := os.Stat(filepath.Dir(tools.run.Remote.CAFile)); !os.IsNotExist(err) {
		t.Errorf("CA directory kept: %v", err)
	}

	r, err := readRecord(a.Dir, id, owner())
	must(t, err)

	if r.State != agentapi.JobDone {
		t.Errorf("record = %+v", r)
	}
}

func TestRunImportForeignFile(t *testing.T) {
	needRoot(t)

	req := request()
	req.Kind = "import"

	a, cfg, id, _, home := startJob(t, req)

	// A file of root, not of the site user, is refused.
	must(t, os.WriteFile(filepath.Join(home, "blog.sql"), []byte("x"), 0o600))

	if err := RunOnce(t.Context(), cfg, id); !errors.Is(err, ErrFailed) {
		t.Fatalf("err = %v", err)
	}

	r, err := readRecord(a.Dir, id, owner())
	must(t, err)

	if r.State != agentapi.JobFailed || !strings.Contains(r.Error, "not a regular file of the site user") {
		t.Errorf("record = %+v", r)
	}
}

func TestFinish(t *testing.T) {
	a, cfg, id, _, _ := startJob(t, request())

	must(t, Finish(cfg, id))

	r, err := readRecord(a.Dir, id, owner())
	must(t, err)

	if r.State != agentapi.JobFailed || r.Error != "stopped" || r.Request != nil {
		t.Errorf("record = %+v", r)
	}

	// A finished job stays as it is.
	must(t, Finish(cfg, id))

	if err := Finish(cfg, "nope"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad id: %v", err)
	}
}

func TestRunNeedsRoot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("runs as root")
	}

	if err := RunOnce(t.Context(), RunConfig{}, "0123456789abcdef"); err == nil {
		t.Error("ran without root")
	}
}

func TestJobError(t *testing.T) {
	for err, want := range map[error]string{
		ErrNoSite:            "the site does not exist any more",
		errors.New("secret"): "internal error",
	} {
		if got := jobError(err); got != want {
			t.Errorf("jobError(%v) = %q", err, got)
		}
	}
}
