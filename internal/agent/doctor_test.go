// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/doctor"
	"github.com/maki-it/hostashi/internal/siteslice"
	"github.com/maki-it/hostashi/internal/web"
)

func doctorEnv(t *testing.T) DoctorEnv {
	t.Helper()

	dir := t.TempDir()
	osr, mi := filepath.Join(dir, "os-release"), filepath.Join(dir, "mountinfo")

	if err := os.WriteFile(osr, []byte("ID=debian\nVERSION_ID=\"13\"\nPRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(mi, []byte("23 1 0:21 / /proc rw - proc none rw,hidepid=invisible\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sockDir, err := os.MkdirTemp("", "doc")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })

	f2b := filepath.Join(sockDir, "f2b")

	l, err := net.Listen("unix", f2b)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = l.Close() })

	// An empty root for the hardening checks: no firewall, no updates, no
	// PHP processes, no sshd config.
	root := filepath.Join(dir, "root")
	if err := os.MkdirAll(filepath.Join(root, "proc"), 0o700); err != nil {
		t.Fatal(err)
	}

	return DoctorEnv{
		System:         doctor.System{OSRelease: osr, MountInfo: mi},
		PanelSocket:    filepath.Join(sockDir, "panel-missing"),
		Fail2banSocket: f2b,
		Disk:           []string{dir},
		Hardening: doctor.Hardening{Root: root, Active: func(context.Context, string) (bool, error) {
			return false, errors.New("not asked: there is no rules file")
		}},
		ValkeyBinary:  osr, // any file: Valkey counts as installed
		ValkeyDefault: func(context.Context) (string, string, error) { return "inactive", "masked", nil },
	}
}

func byID(r agentapi.DoctorReport) map[string][]agentapi.DoctorCheck {
	m := map[string][]agentapi.DoctorCheck{}

	for _, c := range r.Checks {
		k := c.ID
		if c.Site != "" {
			k = c.Site + "/" + c.ID
		}

		m[k] = append(m[k], c)
	}

	return m
}

func TestDoctor(t *testing.T) {
	ctx := context.Background()
	fq := newFakeQuota(true)
	slices := &siteslice.Manager{Dir: t.TempDir(), Ctl: nopReload{}}
	c, _ := startSiteAgentCfg(t, Config{Quota: fq, Slices: slices, Doctor: doctorEnv(t)})

	// One site only: the test accounts have a single uid when run as root.
	site, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop"})
	if err != nil {
		t.Fatal(err)
	}

	r, err := agentapi.Doctor.Call(ctx, c, agentapi.DoctorRequest{})
	if err != nil {
		t.Fatal(err)
	}

	m := byID(r)
	if l := m["shop/site.limits"]; len(l) != 1 || l[0].Status != agentapi.DoctorWarn || l[0].Hint == "" {
		t.Errorf("site without a slice: %+v", l)
	}

	if d := m["shop/site.disk"]; len(d) != 2 || d[0].Status != agentapi.DoctorOK {
		t.Errorf("unlimited disk: %+v", d)
	}

	// 4096 bytes and 3 files used (fakeQuota) of 4300 bytes, files
	// unlimited; and limits on the slice.
	_ = fq.SetLimits("", site.UID, 4300, 0)

	if _, err := agentapi.SiteLimitsSet.Call(ctx, c, agentapi.SiteLimitsSetRequest{User: "shop", Limits: agentapi.SiteLimits{TasksMax: 128}}); err != nil {
		t.Fatal(err)
	}

	if r, err = agentapi.Doctor.Call(ctx, c, agentapi.DoctorRequest{}); err != nil {
		t.Fatal(err)
	}

	m = byID(r)
	want := map[string]string{
		"system.os":          agentapi.DoctorOK,
		"system.quota":       agentapi.DoctorOK,
		"system.proc":        agentapi.DoctorOK,
		"system.pool":        agentapi.DoctorWarn, // the test pool is 0700, not root 0711
		"system.pool.volume": agentapi.DoctorOK,
		"service.panel":      agentapi.DoctorFail, // no panel socket
		"service.nginx":      agentapi.DoctorFail, // no web reconciler
		"service.mariadb":    agentapi.DoctorWarn, // no database manager
		"service.fail2ban":   agentapi.DoctorOK,
		"system.firewall":    agentapi.DoctorWarn, // no rules file
		"system.updates":     agentapi.DoctorWarn,
		"system.reboot":      agentapi.DoctorOK,
		"system.php":         agentapi.DoctorOK,
		"system.ssh":         agentapi.DoctorWarn, // no sshd_config
		"system.sysctl":      agentapi.DoctorWarn, // no sysctl file
		"shop/site.limits":   agentapi.DoctorOK,
	}

	if len(m["system.disk"]) == 0 { // its status depends on the test machine's disk
		t.Error("no disk check")
	}

	for k, status := range want {
		if len(m[k]) == 0 || m[k][0].Status != status {
			t.Errorf("%s = %+v, want %s", k, m[k], status)
		}
	}

	shopDisk := m["shop/site.disk"]
	if len(shopDisk) != 2 || shopDisk[0].Status != agentapi.DoctorFail || shopDisk[1].Status != agentapi.DoctorOK {
		t.Errorf("shop disk = %+v", shopDisk)
	}

	if r.Worst() != agentapi.DoctorFail {
		t.Errorf("worst = %s", r.Worst())
	}

	// System first, then the sites in order.
	last := ""
	for _, ch := range r.Checks {
		if ch.Site < last {
			t.Fatalf("checks out of order at %+v", ch)
		}

		last = ch.Site
	}
}

// TestDoctorMissingPool: a pool whose volume is gone fails, and its path
// gets no disk, quota or mode check that would describe the root disk
// (spike S9 finding 4).
func TestDoctorMissingPool(t *testing.T) {
	c, pool := startSiteAgentCfg(t, Config{Quota: newFakeQuota(true), Doctor: doctorEnv(t)})

	if err := os.Remove(pool); err != nil {
		t.Fatal(err)
	}

	r, err := agentapi.Doctor.Call(context.Background(), c, agentapi.DoctorRequest{})
	if err != nil {
		t.Fatal(err)
	}

	m := byID(r)
	if v := m["system.pool.volume"]; len(v) != 1 || v[0].Status != agentapi.DoctorFail || !strings.Contains(v[0].Hint, "mount "+pool) {
		t.Errorf("missing pool: %+v", v)
	}

	if len(m["system.quota"]) != 0 || len(m["system.pool"]) != 0 {
		t.Errorf("checks of a missing pool: quota %+v, mode %+v", m["system.quota"], m["system.pool"])
	}

	for _, d := range m["system.disk"] {
		if strings.HasPrefix(d.Summary, pool) {
			t.Errorf("disk check of a missing pool: %+v", d)
		}
	}
}

func TestDoctorWithoutSites(t *testing.T) {
	ctx := context.Background()
	s := NewServer(Config{Version: "t", Doctor: doctorEnv(t)}, uint32(os.Getuid()))

	dir, err := os.MkdirTemp("", "doc")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "a.sock")
	if err := s.Listen(ctx, sock, -1); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(ctx)
	t.Cleanup(cancel)

	go func() { _ = s.Serve(ctx) }()

	r, err := agentapi.Doctor.Call(ctx, agentrpc.NewClient(sock), agentapi.DoctorRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if m := byID(r); m["system.os"][0].Status != agentapi.DoctorOK || m["service.nginx"][0].Status != agentapi.DoctorFail {
		t.Errorf("report = %+v", r)
	}

	if m := byID(r); len(m["service.dbtool"]) != 0 {
		t.Errorf("database tool checked without its port: %+v", m["service.dbtool"])
	}
}

// With the database tool installed (ADR-0038), its PHP-FPM socket is
// checked.
func TestDoctorDBTool(t *testing.T) {
	env := doctorEnv(t)

	for _, tc := range []struct {
		name, socket, want string
	}{
		{"running", env.Fail2banSocket, agentapi.DoctorOK},
		{"down", env.Fail2banSocket + "-missing", agentapi.DoctorFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := env
			e.DBToolSocket = tc.socket
			d := &doctorOps{env: e, dbTool: true}

			c := byID(d.run(context.Background()))["service.dbtool"]
			if len(c) != 1 || c[0].Status != tc.want || (tc.want != agentapi.DoctorOK && c[0].Hint == "") {
				t.Errorf("service.dbtool = %+v, want %s", c, tc.want)
			}
		})
	}
}

func TestNginxCheck(t *testing.T) {
	for _, tc := range []struct {
		h      web.Health
		status string
	}{
		{web.Health{Current: 4, Confirmed: true, Live: 4}, agentapi.DoctorOK},
		{web.Health{}, agentapi.DoctorWarn},
		{web.Health{Current: 4, Confirmed: true, LiveErr: errors.New("connection refused")}, agentapi.DoctorFail},
		{web.Health{Current: 4, Confirmed: true, Live: 3}, agentapi.DoctorFail},
		{web.Health{Current: 4, Confirmed: true, Live: 4, Drift: []string{"sites/shop.conf (modified)"}}, agentapi.DoctorWarn},
		{web.Health{Current: 4, Live: 4}, agentapi.DoctorWarn},
	} {
		if c := nginxCheck(tc.h); c.Status != tc.status || c.ID != "service.nginx" {
			t.Errorf("%+v: %+v", tc.h, c)
		}
	}
}
