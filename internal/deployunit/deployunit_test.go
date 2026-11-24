// SPDX-License-Identifier: AGPL-3.0-or-later

package deployunit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type fakeCtl struct {
	props   map[string]string
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

func (f *fakeCtl) Show(_ context.Context, _ string, props ...string) (map[string]string, error) {
	out := map[string]string{}
	for _, p := range props {
		out[p] = f.props[p]
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
		"ExecStart=\nExecStart=/usr/local/bin/hostashi git-sync \"-url=https://git.example.com/acme/site.git\" \"-branch=main\" \"-auth=token\"",
		`"-release=` + rel + `"`, `"-cache=` + e.home + `/.deploy/repo"`, `"-result=` + e.home + `/.deploy/results/7.json"`,
		"-token-file=%d/token", `"-token-user=deploy"`, `"-shared-dir=` + e.home + `/.deploy/shared"`, `"-shared=.env"`, `"-shared=storage"`,
		"ExecStart=/bin/sh " + filepath.Join(e.m.ScriptDir, "blog", "build.sh") + "\n",
		"LoadCredential=token:" + filepath.Join(e.m.CredDir, "blog", "token") + "\n",
		"TimeoutStartSec=1500\n",
		"StandardOutput=truncate:" + filepath.Join(e.m.LogDir, "blog", "deploy-7.log") + "\n",
		"Environment=PATH=/opt/hostashi/node/22/bin:/usr/local/bin:/usr/bin:/bin\n",
		"HOSTASHI_RELEASE=" + rel + "\n",
	} {
		if !strings.Contains(d, want) {
			t.Errorf("drop-in lacks %q:\n%s", want, d)
		}
	}

	if strings.Contains(d, "s3cret") {
		t.Fatal("token in the drop-in")
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

	e.ctl.props = map[string]string{"ActiveState": "activating", "InvocationID": "abc"}
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

	e.ctl.props = map[string]string{"ActiveState": "inactive", "Result": "success", "InvocationID": "one"}

	req := agentapi.DeployStartRequest{
		Site: "blog", ID: 2, URL: "ssh://git@git.example.com/acme/site.git", Branch: "main",
		Auth: agentapi.GitAuthSSH, Key: "-----BEGIN OPENSSH PRIVATE KEY-----\n", HostKey: "ssh-ed25519 AAAAC3Nza",
		Python: "3.13", TimeoutSeconds: 60,
	}

	if _, err := e.m.Start(t.Context(), req); err != nil {
		t.Fatal(err)
	}

	d, _ := os.ReadFile(filepath.Join(e.m.UnitDir, "hostashi-deploy@blog.service.d", DropIn))
	for _, want := range []string{"-key-file=%d/key", `"-host-key=ssh-ed25519 AAAAC3Nza"`, "LoadCredential=key:", "PATH=" + e.home + "/.venv/bin:"} {
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

func TestStatus(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	e.ctl.props = map[string]string{"ActiveState": "inactive", "InvocationID": "old"}

	if _, err := e.m.Start(ctx, startReq(5)); err != nil {
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

	// Queued: the invocation is still the old one.
	if st := status(5); st.State != agentapi.DeployRunning {
		t.Fatalf("queued %+v", st)
	}

	e.ctl.props = map[string]string{"ActiveState": "activating", "InvocationID": "new"}

	if st := status(5); st.State != agentapi.DeployRunning {
		t.Fatalf("running %+v", st)
	}

	// Finished, but git-sync left no result.
	e.ctl.props = map[string]string{"ActiveState": "inactive", "Result": "success", "InvocationID": "new"}
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

	e.ctl.props = map[string]string{"ActiveState": "failed", "Result": "timeout", "InvocationID": "new"}

	if st := status(5); st.State != agentapi.DeployFailed || !strings.Contains(st.Error, "time limit") {
		t.Fatalf("timeout %+v", st)
	}

	// A queued start that never ran.
	e.ctl.props = map[string]string{"ActiveState": "inactive", "InvocationID": "new"}
	if _, err := e.m.Start(ctx, startReq(6)); err != nil {
		t.Fatal(err)
	}

	e.now = e.now.Add(10 * time.Minute)

	if st := status(6); st.State != agentapi.DeployFailed || st.Error != "the deploy did not start" {
		t.Fatalf("never started %+v", st)
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

	for _, p := range []string{filepath.Join(e.m.UnitDir, "hostashi-deploy@blog.service.d"), filepath.Join(e.m.ScriptDir, "blog"), filepath.Join(e.m.CredDir, "blog")} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left: %v", p, err)
		}
	}

	if !slices.Equal(e.ctl.stopped, []string{"hostashi-deploy@blog.service"}) {
		t.Fatalf("stopped %v", e.ctl.stopped)
	}

	st, _ := e.m.load()
	if _, ok := st["blog"]; ok {
		t.Fatal("state kept")
	}
}
