// SPDX-License-Identifier: AGPL-3.0-or-later

package deployunit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type fakeCtl struct {
	props map[string]string
	// units holds the properties of single units; others have props.
	units   map[string]map[string]string
	started []string
	stopped []string
	reloads int
}

func (f *fakeCtl) DaemonReload(context.Context) error        { f.reloads++; return nil }
func (f *fakeCtl) ResetFailed(context.Context, string) error { return nil }
func (f *fakeCtl) Stop(_ context.Context, unit string) error {
	f.stopped = append(f.stopped, unit)
	return nil
}

func (f *fakeCtl) StartNoBlock(_ context.Context, u string) error {
	f.started = append(f.started, u)

	return nil
}

func (f *fakeCtl) Show(_ context.Context, unit string, props ...string) (map[string]string, error) {
	src := f.props
	if u, ok := f.units[unit]; ok {
		src = u
	}

	out := map[string]string{}
	for _, p := range props {
		out[p] = src[p]
	}

	return out, nil
}

type env struct {
	m    *Manager
	ctl  *fakeCtl
	home string
	now  time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()

	base := t.TempDir()
	home := filepath.Join(base, "home", "blog")

	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}

	e := &env{ctl: &fakeCtl{props: map[string]string{"ActiveState": "inactive"}}, home: home, now: time.Unix(2_000_000_000, 0)}
	e.m = &Manager{
		Dir: filepath.Join(base, "state"), UnitDir: filepath.Join(base, "units"), ScriptDir: filepath.Join(base, "scripts"),
		CredDir: filepath.Join(base, "creds"), LogDir: filepath.Join(base, "logs"),
		Site: func(s string) (Site, error) {
			if s != "blog" {
				return Site{}, errors.New("unknown site")
			}

			return Site{Home: home, UID: uint32(os.Getuid()), GID: uint32(os.Getgid()), Slice: "hostashi-site-blog.slice"}, nil
		},
		Ctl: e.ctl, now: func() time.Time { return e.now },
	}

	return e
}

func startReq(id int64) agentapi.DeployStartRequest {
	return agentapi.DeployStartRequest{
		Site: "blog", ID: id, URL: "https://git.example.com/acme/site.git", Branch: "main",
		Auth: agentapi.GitAuthToken, Token: "s3cret", TokenUser: "deploy",
		Shared:   []string{".env", "storage"},
		Commands: []string{"composer install --no-dev", "echo 'it''s done' > done.txt"},
		Node:     "22", TimeoutSeconds: 600,
	}
}

func TestStartWritesRun(t *testing.T) {
	e := newEnv(t)

	st, err := e.m.Start(t.Context(), startReq(7))
	if err != nil {
		t.Fatal(err)
	}

	if st.State != agentapi.DeployRunning || !slices.Equal(e.ctl.started, []string{"hostashi-deploy@blog.service"}) || e.ctl.reloads != 1 {
		t.Fatalf("status %+v, started %v, reloads %d", st, e.ctl.started, e.ctl.reloads)
	}

	dropIn, err := os.ReadFile(filepath.Join(e.m.UnitDir, "hostashi-deploy@blog.service.d", DropIn))
	if err != nil {
		t.Fatal(err)
	}

	d := string(dropIn)
	rel := e.home + "/.deploy/releases/7"

	for _, want := range []string{
		"ExecStart=\nExecStart=/usr/bin/hostashi git-sync \"-url=https://git.example.com/acme/site.git\" \"-branch=main\" \"-auth=token\"",
		`"-release=` + rel + `"`, `"-cache=` + e.home + `/.deploy/repo"`, `"-result=` + e.home + `/.deploy/results/7.json"`,
		"-token-file=%d/token", `"-token-user=deploy"`, `"-shared-dir=` + e.home + `/.deploy/shared"`, `"-shared=.env"`, `"-shared=storage"`,
		"LoadCredential=token:" + filepath.Join(e.m.CredDir, "blog", "token") + "\n",
		"TimeoutStartSec=900\n",
		"StandardOutput=truncate:" + filepath.Join(e.m.LogDir, "blog", "deploy-7.log") + "\n",
		"Environment=PATH=/usr/local/bin:/usr/bin:/bin\n",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("drop-in lacks %q:\n%s", want, d)
		}
	}

	if strings.Contains(d, "s3cret") || strings.Contains(d, "/bin/sh") {
		t.Fatalf("token or build in the deploy drop-in:\n%s", d)
	}

	// The build runs in its own unit, without the credentials.
	build, err := os.ReadFile(filepath.Join(e.m.UnitDir, "hostashi-build@blog.service.d", DropIn))
	if err != nil {
		t.Fatal(err)
	}

	b := string(build)

	for _, want := range []string{
		"ExecStart=\nExecStart=/bin/sh " + filepath.Join(e.m.ScriptDir, "blog", "build.sh") + "\n",
		"TimeoutStartSec=600\n",
		"StandardOutput=append:" + filepath.Join(e.m.LogDir, "blog", "deploy-7.log") + "\n",
		"Environment=PATH=/opt/hostashi/node/22/bin:/usr/local/bin:/usr/bin:/bin\n",
		"HOSTASHI_RELEASE=" + rel + "\n",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("build drop-in lacks %q:\n%s", want, b)
		}
	}

	if strings.Contains(b, "Credential") || strings.Contains(b, "token") || strings.Contains(b, "git-sync") {
		t.Fatalf("credentials or git-sync in the build drop-in:\n%s", b)
	}

	if _, err := os.Stat(filepath.Join(e.m.UnitDir, "hostashi-build@blog.service.d", "50-hostashi-slice.conf")); err != nil {
		t.Fatalf("build unit without the slice drop-in: %v", err)
	}

	if fi, err := os.Stat(filepath.Join(e.m.CredDir, "blog", "token")); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("token file %v %v", fi, err)
	}

	script, err := os.ReadFile(filepath.Join(e.m.ScriptDir, "blog", "build.sh"))
	if err != nil {
		t.Fatal(err)
	}

	want := "#!/bin/sh\n# Written by the Hostashi agent; changes are overwritten.\nset -e\ncd '" + rel + "'\n" +
		"printf '\\n$ %s\\n' 'composer install --no-dev'\ncomposer install --no-dev\n" +
		"printf '\\n$ %s\\n' 'echo '\\''it'\\'''\\''s done'\\'' > done.txt'\necho 'it''s done' > done.txt\n"
	if string(script) != want {
		t.Fatalf("script:\n%s\nwant:\n%s", script, want)
	}

	if fi, _ := os.Stat(filepath.Join(e.m.ScriptDir, "blog", "build.sh")); fi.Mode().Perm() != 0o640 {
		t.Fatalf("script mode %v", fi.Mode())
	}

	// Queued: a second start is refused.
	if _, err := e.m.Start(t.Context(), startReq(8)); !errors.Is(err, ErrBusy) {
		t.Fatalf("second start: %v", err)
	}

	e.ctl.props = map[string]string{"ActiveState": "activating"}
	if _, err := e.m.Start(t.Context(), startReq(8)); !errors.Is(err, ErrBusy) {
		t.Fatalf("start while running: %v", err)
	}
}

func TestStartSSHAndNoCommands(t *testing.T) {
	e := newEnv(t)

	// A token left from an earlier run goes.
	if _, err := e.m.Start(t.Context(), startReq(1)); err != nil {
		t.Fatal(err)
	}

	e.ctl.props = map[string]string{"ActiveState": "active", "Result": "success"}

	req := agentapi.DeployStartRequest{
		Site: "blog", ID: 2, URL: "ssh://git@git.example.com/acme/site.git", Branch: "main",
		Auth: agentapi.GitAuthSSH, Key: "-----BEGIN OPENSSH PRIVATE KEY-----\n", HostKey: "ssh-ed25519 AAAAC3Nza",
		Python: "3.13", TimeoutSeconds: 60,
	}

	if _, err := e.m.Start(t.Context(), req); err != nil {
		t.Fatal(err)
	}

	d, _ := os.ReadFile(filepath.Join(e.m.UnitDir, "hostashi-deploy@blog.service.d", DropIn))
	for _, want := range []string{"-key-file=%d/key", `"-host-key=ssh-ed25519 AAAAC3Nza"`, "LoadCredential=key:"} {
		if !strings.Contains(string(d), want) {
			t.Errorf("drop-in lacks %q:\n%s", want, d)
		}
	}

	if strings.Contains(string(d), "/bin/sh") || strings.Contains(string(d), "BEGIN") {
		t.Fatalf("drop-in:\n%s", d)
	}

	if _, err := os.Stat(filepath.Join(e.m.CredDir, "blog", "token")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old token kept: %v", err)
	}

	// Without commands the build unit of the earlier run loses its run.
	if _, err := os.Stat(filepath.Join(e.m.UnitDir, "hostashi-build@blog.service.d")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("build drop-ins kept: %v", err)
	}
}

func TestStartRefuses(t *testing.T) {
	e := newEnv(t)

	if err := os.MkdirAll(filepath.Join(e.home, ".deploy/releases/3"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := e.m.Start(t.Context(), startReq(3)); err == nil || !strings.Contains(err.Error(), "exists already") {
		t.Fatalf("existing release: %v", err)
	}

	bad := startReq(4)
	bad.Commands = []string{"echo a\necho b"}

	if _, err := e.m.Start(t.Context(), bad); err == nil {
		t.Fatal("two-line command accepted")
	}

	bad = startReq(4)
	bad.Site = "other"

	if _, err := e.m.Start(t.Context(), bad); err == nil {
		t.Fatal("unknown site accepted")
	}

	if len(e.ctl.started) != 0 {
		t.Fatalf("started %v", e.ctl.started)
	}
}

// noBuild is startReq without build commands: one unit runs.
func noBuild(id int64) agentapi.DeployStartRequest {
	req := startReq(id)
	req.Commands = nil

	return req
}

func TestStatus(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	e.ctl.props = map[string]string{"ActiveState": "inactive"}

	if _, err := e.m.Start(ctx, noBuild(5)); err != nil {
		t.Fatal(err)
	}

	status := func(id int64) agentapi.DeployStatus {
		t.Helper()

		st, err := e.m.Status(ctx, agentapi.DeployStatusRequest{Site: "blog", ID: id})
		if err != nil {
			t.Fatal(err)
		}

		return st
	}

	if st := status(4); st.State != agentapi.DeployUnknown {
		t.Fatalf("other id %+v", st)
	}

	// Queued: the unit has not left "inactive" yet.
	if st := status(5); st.State != agentapi.DeployRunning {
		t.Fatalf("queued %+v", st)
	}

	e.ctl.props = map[string]string{"ActiveState": "activating"}

	if st := status(5); st.State != agentapi.DeployRunning {
		t.Fatalf("running %+v", st)
	}

	// Finished (RemainAfterExit keeps it active), but git-sync left no
	// result.
	e.ctl.props = map[string]string{"ActiveState": "active", "Result": "success"}
	if err := os.WriteFile(e.m.LogFile("blog", 5), []byte("fetching\ndone\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if st := status(5); st.State != agentapi.DeployFailed || st.Error != "git-sync left no result" || st.Log != "fetching\ndone\n" {
		t.Fatalf("no result %+v", st)
	}

	if _, err := os.Stat(filepath.Join(e.m.CredDir, "blog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credentials kept after the run: %v", err)
	}

	results := filepath.Join(e.home, ".deploy/results")
	if err := os.MkdirAll(results, 0o700); err != nil {
		t.Fatal(err)
	}

	write := func(s string) {
		t.Helper()

		if err := os.WriteFile(filepath.Join(results, "5.json"), []byte(s), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(`{"commit":"` + strings.Repeat("ab", 20) + `","author":"Kim\u0007","message":"` + strings.Repeat("ä", 400) + `"}`)

	st := status(5)
	if st.State != agentapi.DeploySucceeded || st.Commit != strings.Repeat("ab", 20) || st.Author != "Kim " || len(st.Message) > 500 || !strings.HasPrefix(st.Message, "ää") {
		t.Fatalf("succeeded %+v", st)
	}

	write(`{"commit":"nope"}`)

	if st := status(5); st.State != agentapi.DeployFailed || st.Error != "git-sync left an invalid commit" {
		t.Fatalf("bad commit %+v", st)
	}

	// The finished run's unit was stopped once, so systemd released its
	// credentials; its outcome is kept for later calls.
	if n := strings.Count(strings.Join(e.ctl.stopped, " "), "hostashi-deploy@blog.service"); n != 2 {
		t.Fatalf("unit stopped %d times (start and finish), want 2: %v", n, e.ctl.stopped)
	}

	e.ctl.props = map[string]string{"ActiveState": "inactive"}

	if busy, err := e.m.running(ctx, "blog"); err != nil || busy {
		t.Fatalf("finished run counts as running: %v %v", busy, err)
	}

	// A run that timed out.
	if _, err := e.m.Start(ctx, noBuild(7)); err != nil {
		t.Fatal(err)
	}

	e.ctl.props = map[string]string{"ActiveState": "failed", "Result": "timeout"}

	if st := status(7); st.State != agentapi.DeployFailed || !strings.Contains(st.Error, "time limit") {
		t.Fatalf("timeout %+v", st)
	}

	e.ctl.props = map[string]string{"ActiveState": "inactive"}

	if st := status(7); st.State != agentapi.DeployFailed || !strings.Contains(st.Error, "time limit") {
		t.Fatalf("timeout after the stop %+v", st)
	}

	// A queued start that never ran.
	if _, err := e.m.Start(ctx, noBuild(6)); err != nil {
		t.Fatal(err)
	}

	e.ctl.props = map[string]string{"ActiveState": "inactive"}

	e.now = e.now.Add(10 * time.Minute)

	if st := status(6); st.State != agentapi.DeployFailed || st.Error != "the deploy did not start" {
		t.Fatalf("never started %+v", st)
	}
}

func TestBuildUnit(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	deploy, build := "hostashi-deploy@blog.service", "hostashi-build@blog.service"

	status := func(id int64) agentapi.DeployStatus {
		t.Helper()

		st, err := e.m.Status(ctx, agentapi.DeployStatusRequest{Site: "blog", ID: id})
		if err != nil {
			t.Fatal(err)
		}

		return st
	}

	result := func(id int64) {
		t.Helper()

		dir := filepath.Join(e.home, ".deploy/results")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}

		body := `{"commit":"` + strings.Repeat("cd", 20) + `","author":"Kim","message":"x"}`
		if err := os.WriteFile(filepath.Join(dir, strconv.FormatInt(id, 10)+".json"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	set := func(unit, state, res string) {
		if e.ctl.units == nil {
			e.ctl.units = map[string]map[string]string{}
		}

		e.ctl.units[unit] = map[string]string{"ActiveState": state, "Result": res}
	}

	if _, err := e.m.Start(ctx, startReq(9)); err != nil {
		t.Fatal(err)
	}

	set(deploy, "activating", "")
	set(build, "inactive", "")

	if st := status(9); st.State != agentapi.DeployRunning || !slices.Equal(e.ctl.started, []string{deploy}) {
		t.Fatalf("git-sync running: %+v, started %v", st, e.ctl.started)
	}

	// git-sync is done: its unit is stopped, so systemd releases the
	// token, the agent's copy goes, and only then the build starts.
	set(deploy, "active", "success")
	result(9)

	stops := len(e.ctl.stopped)

	if st := status(9); st.State != agentapi.DeployRunning || !slices.Equal(e.ctl.started, []string{deploy, build}) {
		t.Fatalf("build queued: %+v, started %v", st, e.ctl.started)
	}

	if !slices.Equal(e.ctl.stopped[stops:], []string{deploy}) {
		t.Fatalf("stopped %v, want the deploy unit before the build", e.ctl.stopped[stops:])
	}

	if _, err := os.Stat(filepath.Join(e.m.CredDir, "blog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credentials kept while the build runs: %v", err)
	}

	// The stopped deploy unit looks inactive; the build is what counts now.
	set(deploy, "inactive", "")

	if _, err := e.m.Start(ctx, startReq(10)); !errors.Is(err, ErrBusy) {
		t.Fatalf("start while the build is queued: %v", err)
	}

	set(build, "activating", "")

	if st := status(9); st.State != agentapi.DeployRunning {
		t.Fatalf("building %+v", st)
	}

	set(build, "failed", "exit-code")

	if st := status(9); st.State != agentapi.DeployFailed || st.Error != "a step of the deploy failed" {
		t.Fatalf("build failed %+v", st)
	}

	if e.ctl.stopped[len(e.ctl.stopped)-1] != build {
		t.Fatalf("finished build unit not stopped: %v", e.ctl.stopped)
	}

	// A build that succeeds.
	set(deploy, "inactive", "")
	set(build, "inactive", "")

	if _, err := e.m.Start(ctx, startReq(11)); err != nil {
		t.Fatal(err)
	}

	set(deploy, "active", "success")
	result(11)

	if st := status(11); st.State != agentapi.DeployRunning {
		t.Fatalf("build queued %+v", st)
	}

	set(build, "active", "success")

	if st := status(11); st.State != agentapi.DeploySucceeded || st.Commit != strings.Repeat("cd", 20) {
		t.Fatalf("build succeeded %+v", st)
	}

	// git-sync ended without its result: no build on that release, and
	// the run stays failed.
	set(deploy, "inactive", "")
	set(build, "inactive", "")

	if _, err := e.m.Start(ctx, startReq(12)); err != nil {
		t.Fatal(err)
	}

	set(deploy, "active", "success")

	started := len(e.ctl.started)

	if st := status(12); st.State != agentapi.DeployFailed || st.Error != "git-sync left no result" || len(e.ctl.started) != started {
		t.Fatalf("no result %+v, started %v", st, e.ctl.started)
	}

	result(12)

	if st := status(12); st.State != agentapi.DeployFailed {
		t.Fatalf("no result, later %+v", st)
	}
}

func TestPruneLogs(t *testing.T) {
	e := newEnv(t)
	dir := filepath.Join(e.m.LogDir, "blog")

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	for i := int64(1); i <= 40; i++ {
		if err := os.WriteFile(e.m.LogFile("blog", i), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	e.m.pruneLogs("blog", 41)

	entries, _ := os.ReadDir(dir)
	if len(entries) != keepLogs-1 {
		t.Fatalf("%d logs left", len(entries))
	}

	if _, err := os.Stat(e.m.LogFile("blog", 40)); err != nil {
		t.Fatal("newest log deleted")
	}
}

func TestRemove(t *testing.T) {
	e := newEnv(t)

	if _, err := e.m.Start(t.Context(), startReq(1)); err != nil {
		t.Fatal(err)
	}

	if err := e.m.Remove(t.Context(), "blog"); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{
		filepath.Join(e.m.UnitDir, "hostashi-deploy@blog.service.d"), filepath.Join(e.m.UnitDir, "hostashi-build@blog.service.d"),
		filepath.Join(e.m.ScriptDir, "blog"), filepath.Join(e.m.CredDir, "blog"),
	} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left: %v", p, err)
		}
	}

	// Start stopped the last run, Remove the units.
	units := []string{"hostashi-deploy@blog.service", "hostashi-build@blog.service"}
	if !slices.Equal(e.ctl.stopped, append(units, units...)) {
		t.Fatalf("stopped %v", e.ctl.stopped)
	}

	st, _ := e.m.load()
	if _, ok := st["blog"]; ok {
		t.Fatal("state kept")
	}
}

func TestLog(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	if l, err := e.m.RunLog(ctx, agentapi.DeployStatusRequest{Site: "blog", ID: 3}); err != nil || l.Log != "" || l.Truncated {
		t.Fatalf("missing log %+v %v", l, err)
	}

	if err := os.MkdirAll(filepath.Join(e.m.LogDir, "blog"), 0o700); err != nil {
		t.Fatal(err)
	}

	big := strings.Repeat("a", agentapi.MaxDeployLog) + "end\n"
	if err := os.WriteFile(e.m.LogFile("blog", 3), []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}

	l, err := e.m.RunLog(ctx, agentapi.DeployStatusRequest{Site: "blog", ID: 3})
	if err != nil || !l.Truncated || len(l.Log) != agentapi.MaxDeployLog || !strings.HasSuffix(l.Log, "end\n") {
		t.Fatalf("log %d %v %v", len(l.Log), l.Truncated, err)
	}

	if _, err := e.m.RunLog(ctx, agentapi.DeployStatusRequest{Site: "other", ID: 3}); err == nil {
		t.Fatal("unknown site accepted")
	}
}

// The build unit has the deploy unit's sandbox and no credentials.
func TestBuildTemplate(t *testing.T) {
	read := func(name string) string {
		t.Helper()

		b, err := os.ReadFile(filepath.Join("..", "..", "packaging", "systemd", name))
		if err != nil {
			t.Fatal(err)
		}

		return string(b)
	}

	deploy, build := read("hostashi-deploy@.service"), read("hostashi-build@.service")

	sandbox := func(s string) string {
		_, after, ok := strings.Cut(s, "# A /tmp of its own.")
		if !ok {
			t.Fatal("template without the sandbox section")
		}

		return after
	}

	if sandbox(deploy) != sandbox(build) {
		t.Fatal("the sandboxes of hostashi-deploy@ and hostashi-build@ differ")
	}

	for _, s := range []string{"User=%i", "Group=%i", "Type=oneshot", "RemainAfterExit=yes", "KillMode=control-group", "LimitCORE=0"} {
		if !strings.Contains(build, "\n"+s+"\n") {
			t.Errorf("build template lacks %s", s)
		}
	}

	if strings.Contains(build, "Credential=") {
		t.Fatal("build template has credentials")
	}
}
