// SPDX-License-Identifier: AGPL-3.0-or-later

package appunit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/firewall"
)

type fakeCtl struct {
	calls  []string
	reload int
	fail   error
	// run does the work of a oneshot unit.
	run func(unit string) error
}

func (f *fakeCtl) DaemonReload(context.Context) error { f.reload++; return nil }

func (f *fakeCtl) do(verb, unit string) error {
	f.calls = append(f.calls, verb+" "+unit)

	return f.fail
}

func (f *fakeCtl) Start(_ context.Context, u string) error   { return f.do("start", u) }
func (f *fakeCtl) Stop(_ context.Context, u string) error    { return f.do("stop", u) }
func (f *fakeCtl) Restart(_ context.Context, u string) error { return f.do("restart", u) }
func (f *fakeCtl) ResetFailed(context.Context, string) error { return nil }

func (f *fakeCtl) Run(_ context.Context, u string) error {
	if err := f.do("run", u); err != nil {
		return err
	}

	if f.run != nil {
		return f.run(u)
	}

	return nil
}

func (f *fakeCtl) Show(context.Context, string, ...string) (map[string]string, error) {
	return map[string]string{"ActiveState": "active", "SubState": "running", "NRestarts": "2", "MemoryCurrent": "1048576"}, nil
}

type env struct {
	m     *Manager
	ctl   *fakeCtl
	fw    [][]firewall.AppOwner
	fwErr error
	root  string
}

func newEnv(t *testing.T) *env {
	t.Helper()

	root := t.TempDir()
	e := &env{ctl: &fakeCtl{}, root: root}
	e.m = &Manager{
		Dir: filepath.Join(root, "state"), UnitDir: filepath.Join(root, "units"), NodeDir: filepath.Join(root, "node"), LogDir: filepath.Join(root, "log"),
		Site: func(site string) (Site, error) {
			if site == "gone" {
				return Site{}, errors.New("no such user")
			}

			return Site{Home: "/home/" + site, UID: 1001, Slice: "hostashi-site-" + site + ".slice"}, nil
		},
		Hidden: func() []string { return []string{"/srv/pool2"} },
		Ctl:    e.ctl,
		Firewall: func(_ context.Context, o []firewall.AppOwner) error {
			e.fw = append(e.fw, o)

			return e.fwErr
		},
	}

	bin := filepath.Join(root, "node", "24", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(bin, "node"), nil, 0o755); err != nil {
		t.Fatal(err)
	}

	return e
}

func spec(f func(*agentapi.AppSpec)) agentapi.AppSpec {
	a := agentapi.AppSpec{Site: "shop", Node: "24", Workdir: "htdocs", StartKind: agentapi.AppStartScript, Start: "server.js", Port: 20001}
	if f != nil {
		f(&a)
	}

	return a
}

func TestApply(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	st, err := e.m.Apply(ctx, spec(nil))
	if err != nil {
		t.Fatal(err)
	}

	if st.State != "active" || st.Restarts != 2 || st.UsedBytes != 1<<20 {
		t.Errorf("status %+v", st)
	}

	if !slices.Equal(e.ctl.calls, []string{"restart hostashi-node@shop.service", "start hostashi-node@shop.service"}) || e.ctl.reload != 1 {
		t.Errorf("calls %v, %d reloads", e.ctl.calls, e.ctl.reload)
	}

	if len(e.fw) != 1 || !slices.Equal(e.fw[0], []firewall.AppOwner{{Port: 20001, UID: 1001}}) {
		t.Errorf("firewall %v", e.fw)
	}

	dir := filepath.Join(e.root, "units", "hostashi-node@shop.service.d")

	app, _ := os.ReadFile(filepath.Join(dir, AppDropIn))
	for _, want := range []string{
		"WorkingDirectory=/home/shop/htdocs\n", "ExecStart=\n", "ExecStart=" + e.root + "/node/24/bin/node server.js\n",
		"Environment=NODE_ENV=production HOST=127.0.0.1 PORT=20001\n", "Environment=PATH=" + e.root + "/node/24/bin:/usr/local/bin:/usr/bin:/bin\n",
	} {
		if !strings.Contains(string(app), want) {
			t.Errorf("app drop-in lacks %q:\n%s", want, app)
		}
	}

	slice, _ := os.ReadFile(filepath.Join(dir, SliceDropIn))
	for _, want := range []string{"Slice=hostashi-site-shop.slice\n", "BindPaths=/home/shop\n", "RequiresMountsFor=/home/shop\n", "TemporaryFileSystem=/srv/pool2:ro\n"} {
		if !strings.Contains(string(slice), want) {
			t.Errorf("slice drop-in lacks %q:\n%s", want, slice)
		}
	}

	if fi, err := os.Stat(filepath.Join(e.root, "log", "shop")); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("log dir: %v %v", fi, err)
	}

	// Unchanged: no restart, only start (enable).
	e.ctl.calls = nil
	if _, err := e.m.Apply(ctx, spec(nil)); err != nil || !slices.Equal(e.ctl.calls, []string{"start hostashi-node@shop.service"}) {
		t.Errorf("unchanged: %v %v", err, e.ctl.calls)
	}

	// npm start, then stopped.
	e.ctl.calls = nil
	if _, err := e.m.Apply(ctx, spec(func(a *agentapi.AppSpec) { a.StartKind, a.Start = agentapi.AppStartNPM, "start" })); err != nil {
		t.Fatal(err)
	}

	app, _ = os.ReadFile(filepath.Join(dir, AppDropIn))
	if !strings.Contains(string(app), "ExecStart="+e.root+"/node/24/bin/npm run start\n") || e.ctl.calls[0] != "restart hostashi-node@shop.service" {
		t.Errorf("npm: %v\n%s", e.ctl.calls, app)
	}

	e.ctl.calls = nil
	if _, err := e.m.Apply(ctx, spec(func(a *agentapi.AppSpec) { a.Stopped = true })); err != nil || !slices.Equal(e.ctl.calls, []string{"stop hostashi-node@shop.service"}) {
		t.Errorf("stopped: %v %v", err, e.ctl.calls)
	}

	if _, err := e.m.Action(ctx, "shop", agentapi.AppActionStart); !errors.Is(err, ErrStopped) {
		t.Errorf("start a kept-stopped app: %v", err)
	}

	// Starting it again restarts it.
	e.ctl.calls = nil
	if _, err := e.m.Apply(ctx, spec(nil)); err != nil || e.ctl.calls[0] != "restart hostashi-node@shop.service" {
		t.Errorf("start again: %v %v", err, e.ctl.calls)
	}
}

func TestApplyRefuses(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	for name, a := range map[string]agentapi.AppSpec{
		"node 22 missing": spec(func(a *agentapi.AppSpec) { a.Node = "22" }),
		"node 25":         spec(func(a *agentapi.AppSpec) { a.Node = "25" }),
		"workdir up":      spec(func(a *agentapi.AppSpec) { a.Workdir = "../other" }),
		"workdir abs":     spec(func(a *agentapi.AppSpec) { a.Workdir = "/etc" }),
		"script ext":      spec(func(a *agentapi.AppSpec) { a.Start = "server.sh" }),
		"script up":       spec(func(a *agentapi.AppSpec) { a.Start = "../x.js" }),
		"script space":    spec(func(a *agentapi.AppSpec) { a.Start = "a b.js" }),
		"script percent":  spec(func(a *agentapi.AppSpec) { a.Start = "%h.js" }),
		"npm shell":       spec(func(a *agentapi.AppSpec) { a.StartKind, a.Start = agentapi.AppStartNPM, "start; rm -rf /" }),
		"kind":            spec(func(a *agentapi.AppSpec) { a.StartKind = "sh" }),
		"no start":        spec(func(a *agentapi.AppSpec) { a.StartKind, a.Start = "", "" }),
		"port":            spec(func(a *agentapi.AppSpec) { a.Port = 8080 }),
		"site":            spec(func(a *agentapi.AppSpec) { a.Site = "root" }),
		"no account":      spec(func(a *agentapi.AppSpec) { a.Site = "gone" }),
	} {
		if _, err := e.m.Apply(ctx, a); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	if len(e.ctl.calls) != 0 {
		t.Errorf("units touched: %v", e.ctl.calls)
	}

	// No start command yet: set up, kept stopped.
	if _, err := e.m.Apply(ctx, spec(func(a *agentapi.AppSpec) { a.StartKind, a.Start, a.Stopped = "", "", true })); err != nil {
		t.Fatal(err)
	}

	if _, err := e.m.Apply(ctx, spec(func(a *agentapi.AppSpec) { a.Site = "other" })); !errors.Is(err, ErrPortTaken) {
		t.Errorf("port of another site: %v", err)
	}

	// A firewall that does not load refuses the app and keeps the old list.
	e.fwErr = errors.New("nft: error")
	if _, err := e.m.Apply(ctx, spec(func(a *agentapi.AppSpec) { a.Site, a.Port = "other", 20002 })); err == nil {
		t.Error("applied without the firewall")
	}

	st, _ := e.m.load()
	if _, ok := st["other"]; ok {
		t.Error("state kept the refused app")
	}

	// The firewall off is only a warning.
	e.fwErr = firewall.ErrDisabled
	if _, err := e.m.Apply(ctx, spec(func(a *agentapi.AppSpec) { a.Site, a.Port = "other", 20002 })); err != nil {
		t.Errorf("firewall off: %v", err)
	}
}

func TestRemoveAndRecover(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()

	if _, err := e.m.Apply(ctx, spec(nil)); err != nil {
		t.Fatal(err)
	}

	if _, err := e.m.Apply(ctx, spec(func(a *agentapi.AppSpec) { a.Site, a.Port, a.Stopped = "blog", 20002, true })); err != nil {
		t.Fatal(err)
	}

	if u, err := e.m.Users("24"); err != nil || !slices.Equal(u, []string{"blog", "shop"}) {
		t.Errorf("Users(24) = %v, %v", u, err)
	}

	if u, err := e.m.Users("22"); err != nil || len(u) != 0 {
		t.Errorf("Users(22) = %v, %v", u, err)
	}

	// A line installed again restarts its running apps only (blog is
	// stopped).
	e.ctl.calls = nil
	e.m.RestartLine(ctx, "24")

	if !slices.Equal(e.ctl.calls, []string{"restart hostashi-node@shop.service"}) {
		t.Errorf("RestartLine(24): %v", e.ctl.calls)
	}

	e.ctl.calls = nil
	e.m.RestartLine(ctx, "22")

	if len(e.ctl.calls) != 0 {
		t.Errorf("RestartLine(22): %v", e.ctl.calls)
	}

	// Lost drop-in: Recover writes it and starts only the running app.
	dir := filepath.Join(e.root, "units", "hostashi-node@shop.service.d")
	if err := os.Remove(filepath.Join(dir, AppDropIn)); err != nil {
		t.Fatal(err)
	}

	e.ctl.calls = nil
	if err := e.m.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(dir, AppDropIn)); err != nil {
		t.Error("drop-in not written again")
	}

	if !slices.Equal(e.ctl.calls, []string{"start hostashi-node@shop.service"}) {
		t.Errorf("recover calls %v", e.ctl.calls)
	}

	if err := e.m.Remove(ctx, "shop"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("drop-ins left: %v", err)
	}

	if last := e.fw[len(e.fw)-1]; !slices.Equal(last, []firewall.AppOwner{{Port: 20002, UID: 1001}}) {
		t.Errorf("firewall after remove %v", last)
	}

	if _, err := e.m.Action(ctx, "shop", agentapi.AppActionRestart); !errors.Is(err, ErrNoApp) {
		t.Errorf("action after remove: %v", err)
	}

	if err := e.m.Remove(ctx, "shop"); err != nil {
		t.Errorf("second remove: %v", err)
	}

	if st, err := e.m.Status(ctx, "shop"); err != nil || st.State != "" {
		t.Errorf("status after remove: %+v %v", st, err)
	}
}
