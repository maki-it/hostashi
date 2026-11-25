// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/hoststats"
	"github.com/maki-it/hostashi/internal/objcache"
	"github.com/maki-it/hostashi/internal/php"
	"github.com/maki-it/hostashi/internal/web"
)

// fakeServices records actions; units in missing are "not installed".
type fakeServices struct {
	mu      sync.Mutex
	calls   []string
	missing map[string]bool
	sshdErr error
	failOn  string
}

func (f *fakeServices) Show(_ context.Context, units []string) ([]agentapi.ServiceState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]agentapi.ServiceState, len(units))
	for i, u := range units {
		if f.missing[u] {
			out[i] = agentapi.ServiceState{Unit: u}

			continue
		}

		out[i] = agentapi.ServiceState{Unit: u, Installed: true, Load: "loaded", Active: "active", NRestarts: 1}
	}

	return out, nil
}

func (f *fakeServices) Action(_ context.Context, unit, action string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, action+" "+unit)

	if unit == f.failOn {
		return errors.New("systemctl exited 1: secret details")
	}

	return nil
}

func (f *fakeServices) TestSSHD(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "sshd -t")

	return f.sshdErr
}

func (f *fakeServices) take() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	c := f.calls
	f.calls = nil

	return c
}

type nginxCtl struct {
	mu    sync.Mutex
	calls []string
	bad   bool
}

func (c *nginxCtl) Test(context.Context, string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls = append(c.calls, "test")

	if c.bad {
		return errors.New("bad config")
	}

	return nil
}

func (c *nginxCtl) Reload(context.Context) error  { c.add("reload"); return nil }
func (c *nginxCtl) Restart(context.Context) error { c.add("restart"); return nil }

func (c *nginxCtl) add(s string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.calls = append(c.calls, s)
}

func TestServicesWithoutController(t *testing.T) {
	c, _ := startSiteAgent(t)

	_, err := agentapi.ServicesStatus.Call(context.Background(), c, agentapi.ServicesStatusRequest{})
	wantCode(t, err, agentrpc.CodePrecondition)

	_, err = agentapi.HostStatsOp.Call(context.Background(), c, agentapi.HostStatsRequest{})
	wantCode(t, err, agentrpc.CodePrecondition)
}

func TestServicesStatusAndActions(t *testing.T) {
	dir := t.TempDir()
	phpDir, cacheDir := filepath.Join(dir, "php"), filepath.Join(dir, "valkey")

	for d, state := range map[string]string{
		phpDir:   `{"shop":{"user":"shop","version":"8.4","home":"/x"},"blog":{"user":"blog","version":"8.3","home":"/y"}}`,
		cacheDir: `{"shop":{"memoryMb":64},"blog":{"memoryMb":64,"paused":true}}`,
	} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(d, "state.json"), []byte(state), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	svc := &fakeServices{missing: map[string]bool{"hostashi-adminer.service": true}}
	ngx := &nginxCtl{}
	w := &web.Reconciler{Base: filepath.Join(dir, "web"), Driver: &web.Nginx{Ctl: ngx, ControlSocket: filepath.Join(dir, "run", "c.sock")}}

	c, _ := startSiteAgentCfg(t, Config{
		Services: svc, Web: w,
		PHP:         &php.Manager{Dir: phpDir, SocketDir: "/run/hostashi-php", WebGroup: "www-data"},
		ObjectCache: &objcache.Manager{Dir: cacheDir, UnitDir: filepath.Join(dir, "units")},
	})
	ctx := context.Background()

	res, err := agentapi.ServicesStatus.Call(ctx, c, agentapi.ServicesStatusRequest{})
	if err != nil {
		t.Fatal(err)
	}

	var got []string
	for _, s := range res.Services {
		got = append(got, s.ID+"/"+s.Site+"="+s.Unit)
	}

	want := []string{
		"nginx/=nginx.service", "mariadb/=mariadb.service", "ssh/=ssh.service", "fail2ban/=fail2ban.service",
		"firewall/=hostashi-firewall.service", "adminer/=hostashi-adminer.service",
		"freshclam/=clamav-freshclam.service", "clamd/=clamav-daemon.service", "clamonacc/=clamav-clamonacc.service", "panel/=hostashi.service", "agent/=hostashi-agent.service",
		"php/blog=hostashi-php-8.3@blog.service", "php/shop=hostashi-php-8.4@shop.service",
		"valkey/blog=hostashi-valkey@blog.service", "valkey/shop=hostashi-valkey@shop.service",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("status\n got %q\nwant %q", got, want)
	}

	if res.Services[5].Installed || !res.Services[0].Installed {
		t.Fatalf("installed flags: %+v", res.Services)
	}

	one, err := agentapi.ServicesStatus.Call(ctx, c, agentapi.ServicesStatusRequest{ID: "php", Site: "shop"})
	if err != nil || len(one.Services) != 1 || one.Services[0].Unit != "hostashi-php-8.4@shop.service" {
		t.Fatalf("one: %+v %v", one, err)
	}

	// Plain actions go to systemd with the unit from the catalog or the
	// site's state.
	for _, tc := range []struct {
		req  agentapi.ServiceActionRequest
		call []string
	}{
		{agentapi.ServiceActionRequest{ID: "mariadb", Action: "stop"}, []string{"stop mariadb.service"}},
		{agentapi.ServiceActionRequest{ID: "fail2ban", Action: "reload"}, []string{"reload fail2ban.service"}},
		{agentapi.ServiceActionRequest{ID: "php", Site: "shop", Action: "reload"}, []string{"reload hostashi-php-8.4@shop.service"}},
		{agentapi.ServiceActionRequest{ID: "valkey", Site: "shop", Action: "restart"}, []string{"restart hostashi-valkey@shop.service"}},
		{agentapi.ServiceActionRequest{ID: "ssh", Action: "reload"}, []string{"sshd -t", "reload ssh.service"}},
	} {
		st, err := agentapi.ServiceAction.Call(ctx, c, tc.req)
		if err != nil || st.ID != tc.req.ID || st.Site != tc.req.Site || !st.Installed {
			t.Fatalf("%+v: %+v %v", tc.req, st, err)
		}

		if calls := svc.take(); !slices.Equal(calls, tc.call) {
			t.Fatalf("%+v: calls %q, want %q", tc.req, calls, tc.call)
		}
	}

	// Refused before anything runs.
	for _, tc := range []struct {
		req  agentapi.ServiceActionRequest
		code string
	}{
		{agentapi.ServiceActionRequest{ID: "nginx", Action: "stop"}, agentrpc.CodeInvalidArgument},
		{agentapi.ServiceActionRequest{ID: "ssh", Action: "stop"}, agentrpc.CodeInvalidArgument},
		{agentapi.ServiceActionRequest{ID: "ssh", Action: "restart"}, agentrpc.CodeInvalidArgument},
		{agentapi.ServiceActionRequest{ID: "firewall", Action: "stop"}, agentrpc.CodeInvalidArgument},
		{agentapi.ServiceActionRequest{ID: "agent", Action: "restart"}, agentrpc.CodeInvalidArgument},
		{agentapi.ServiceActionRequest{ID: "panel", Action: "restart"}, agentrpc.CodeInvalidArgument},
		{agentapi.ServiceActionRequest{ID: "php", Site: "shop", Action: "stop"}, agentrpc.CodeInvalidArgument},
		{agentapi.ServiceActionRequest{ID: "php", Action: "reload"}, agentrpc.CodeInvalidArgument},
		{agentapi.ServiceActionRequest{ID: "nginx", Site: "shop", Action: "reload"}, agentrpc.CodeInvalidArgument},
		{agentapi.ServiceActionRequest{ID: "cron", Action: "start"}, agentrpc.CodeInvalidArgument},
		{agentapi.ServiceActionRequest{ID: "php", Site: "../x", Action: "reload"}, agentrpc.CodeInvalidArgument},
		{agentapi.ServiceActionRequest{ID: "php", Site: "nosuch", Action: "reload"}, agentrpc.CodeNotFound},
		{agentapi.ServiceActionRequest{ID: "valkey", Site: "nosuch", Action: "start"}, agentrpc.CodeNotFound},
		{agentapi.ServiceActionRequest{ID: "valkey", Site: "blog", Action: "start"}, agentrpc.CodePrecondition},
		{agentapi.ServiceActionRequest{ID: "adminer", Action: "start"}, agentrpc.CodePrecondition},
	} {
		_, err := agentapi.ServiceAction.Call(ctx, c, tc.req)
		wantCode(t, err, tc.code)

		if calls := svc.take(); len(calls) != 0 {
			t.Fatalf("%+v ran %q", tc.req, calls)
		}
	}

	// sshd -t failing: nothing is reloaded.
	svc.sshdErr = errors.New("bad key")
	_, err = agentapi.ServiceAction.Call(ctx, c, agentapi.ServiceActionRequest{ID: "ssh", Action: "reload"})
	wantCode(t, err, agentrpc.CodePrecondition)

	if calls := svc.take(); !slices.Equal(calls, []string{"sshd -t"}) {
		t.Fatalf("calls %q", calls)
	}

	// A failing systemctl does not leak its output.
	svc.failOn = "mariadb.service"
	_, err = agentapi.ServiceAction.Call(ctx, c, agentapi.ServiceActionRequest{ID: "mariadb", Action: "restart"})
	wantCode(t, err, agentrpc.CodeInternal)

	if containsSecret(err) {
		t.Fatalf("leaked: %v", err)
	}

	svc.take()

	// nginx goes through the reconciler: no generation yet, then test and
	// reload or restart, a broken config touches nothing.
	_, err = agentapi.ServiceAction.Call(ctx, c, agentapi.ServiceActionRequest{ID: "nginx", Action: "reload"})
	wantCode(t, err, agentrpc.CodePrecondition)

	if err := os.MkdirAll(filepath.Join(dir, "web", "generations", "3"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("generations/3", filepath.Join(dir, "web", "current")); err != nil {
		t.Fatal(err)
	}

	for _, a := range []string{"reload", "restart", "start"} {
		if _, err := agentapi.ServiceAction.Call(ctx, c, agentapi.ServiceActionRequest{ID: "nginx", Action: a}); err != nil {
			t.Fatalf("nginx %s: %v", a, err)
		}
	}

	ngx.bad = true
	_, err = agentapi.ServiceAction.Call(ctx, c, agentapi.ServiceActionRequest{ID: "nginx", Action: "restart"})
	wantCode(t, err, agentrpc.CodePrecondition)

	if want := []string{"test", "reload", "test", "restart", "test", "restart", "test"}; !slices.Equal(ngx.calls, want) {
		t.Fatalf("nginx calls %q, want %q", ngx.calls, want)
	}

	if calls := svc.take(); len(calls) != 0 {
		t.Fatalf("nginx went past the reconciler: %q", calls)
	}
}

func containsSecret(err error) bool {
	return err != nil && (strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "exited"))
}

func TestHostStatsThroughServer(t *testing.T) {
	root := t.TempDir()

	for p, content := range map[string]string{
		"proc/stat": "cpu 1 2 3 4 5 6 7 8\ncpu0 1 2 3 4 5 6 7 8\n", "proc/meminfo": "MemTotal: 100 kB\nMemAvailable: 50 kB\n",
		"proc/loadavg": "0.1 0.2 0.3 1/1 1\n", "proc/uptime": "10 5\n",
		"proc/net/dev": "head 1\nhead 2\n  eth0: 1 2 0 0 0 0 0 0 3 4 0 0 0 0 0 0\n",
	} {
		f := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(f), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	c, _ := startSiteAgentCfg(t, Config{Host: hoststats.Reader{Root: root}})

	st, err := agentapi.HostStatsOp.Call(context.Background(), c, agentapi.HostStatsRequest{})
	if err != nil {
		t.Fatal(err)
	}

	// "/" and the test's pool are measured; both may be one file system.
	if st.CPUs != 1 || st.Memory.TotalBytes != 102400 || len(st.Net) != 1 || len(st.Disks) == 0 || st.Disks[0].Name != "root" {
		t.Fatalf("%+v", st)
	}
}
