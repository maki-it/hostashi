// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/sysconf"
)

type fakeSystem struct {
	mu      sync.Mutex
	st      sysconf.State
	zones   []string
	calls   []string
	failSet error
	reboots int
}

func (f *fakeSystem) State(context.Context) (sysconf.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.st, nil
}

func (f *fakeSystem) Timezones(context.Context) ([]string, error) { return f.zones, nil }

func (f *fakeSystem) SetTimezone(_ context.Context, zone string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "tz "+zone)
	if f.failSet != nil {
		return f.failSet
	}

	f.st.Timezone = zone

	return nil
}

func (f *fakeSystem) SetHostname(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "host "+name)
	if f.failSet != nil {
		return f.failSet
	}

	f.st.Hostname, f.st.StaticHostname = name, name

	return nil
}

func (f *fakeSystem) Reboot(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.reboots++

	return nil
}

func newSystemOps(t *testing.T, f *fakeSystem) (*systemOps, string) {
	t.Helper()

	root := t.TempDir()
	for _, d := range []string{"etc", "proc", "run"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	return &systemOps{
		ctl:   f,
		root:  root,
		log:   slog.New(slog.DiscardHandler),
		now:   func() time.Time { return time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC) },
		after: func(_ time.Duration, fn func()) { fn() },
	}, root
}

func writeTestFile(t *testing.T, name, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSystemInfo(t *testing.T) {
	f := &fakeSystem{st: sysconf.State{Hostname: "web2", StaticHostname: "web2.example.org", Timezone: "Europe/Berlin", NTPSynchronized: true}}
	o, root := newSystemOps(t, f)

	writeTestFile(t, filepath.Join(root, procUptime), "12345.67 9999.00\n")
	writeTestFile(t, filepath.Join(root, rebootRequired), "*** System restart required ***\n")
	writeTestFile(t, filepath.Join(root, rebootRequiredPkgs), "linux-image-6.12\nlibc6\nlinux-image-6.12\n\n")

	info, err := o.info(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if info.Hostname != "web2.example.org" || info.Transient != "web2" || info.Timezone != "Europe/Berlin" || !info.NTPSynchronized {
		t.Fatalf("got %+v", info)
	}

	if info.Time != "2026-11-22T13:00:00+01:00" {
		t.Fatalf("time %q", info.Time)
	}

	if info.UptimeSeconds != 12345.67 {
		t.Fatalf("uptime %v", info.UptimeSeconds)
	}

	if !info.RebootRequired || !slices.Equal(info.RebootPackages, []string{"linux-image-6.12", "libc6"}) {
		t.Fatalf("reboot %v %v", info.RebootRequired, info.RebootPackages)
	}

	if info.CloudInit {
		t.Fatal("cloud-init without /etc/cloud")
	}

	if info.Updates != nil {
		t.Fatalf("updates without the package: %+v", info.Updates)
	}
}

func TestSystemInfoWithoutFiles(t *testing.T) {
	f := &fakeSystem{st: sysconf.State{Hostname: "web2", Timezone: "Nowhere/Land"}}
	o, _ := newSystemOps(t, f)

	info, err := o.info(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	// No static hostname: the transient one; an unknown zone: UTC.
	if info.Hostname != "web2" || info.Time != "2026-11-22T12:00:00Z" || info.UptimeSeconds != 0 || info.RebootRequired {
		t.Fatalf("got %+v", info)
	}
}

func TestSetTimezone(t *testing.T) {
	f := &fakeSystem{st: sysconf.State{Timezone: "UTC"}, zones: []string{"Europe/Berlin", "UTC"}}
	o, _ := newSystemOps(t, f)

	info, err := o.setTimezone(t.Context(), "Europe/Berlin")
	if err != nil {
		t.Fatal(err)
	}

	if info.Timezone != "Europe/Berlin" {
		t.Fatalf("got %+v", info)
	}

	if _, err := o.setTimezone(t.Context(), "Europe/Atlantis"); !errors.Is(err, errZoneUnknown) {
		t.Fatalf("unknown zone: %v", err)
	}

	if !slices.Equal(f.calls, []string{"tz Europe/Berlin"}) {
		t.Fatalf("calls %v", f.calls)
	}
}

func TestSetTimezoneNoBus(t *testing.T) {
	f := &fakeSystem{zones: []string{"UTC"}, failSet: sysconf.ErrNoBus}
	o, _ := newSystemOps(t, f)

	if _, err := o.setTimezone(t.Context(), "UTC"); !errors.Is(err, errNoBus) {
		t.Fatalf("got %v", err)
	}
}

func TestSetHostnameUpdatesFiles(t *testing.T) {
	f := &fakeSystem{}
	o, root := newSystemOps(t, f)

	hosts := "127.0.0.1\tlocalhost\n127.0.1.1\tweb2.old.example web2\n\n# IPv6\n::1 localhost ip6-localhost\n"
	writeTestFile(t, filepath.Join(root, etcHosts), hosts)

	if err := os.MkdirAll(filepath.Join(root, cloudDir), 0o755); err != nil {
		t.Fatal(err)
	}

	info, err := o.setHostname(t.Context(), "web3.example.org")
	if err != nil {
		t.Fatal(err)
	}

	if info.Hostname != "web3.example.org" || !info.CloudInit {
		t.Fatalf("got %+v", info)
	}

	got, _ := os.ReadFile(filepath.Join(root, etcHosts))
	want := "127.0.0.1\tlocalhost\n127.0.1.1\tweb3.example.org web3\n\n# IPv6\n::1 localhost ip6-localhost\n"

	if string(got) != want {
		t.Fatalf("hosts:\n%s", got)
	}

	cfg, err := os.ReadFile(filepath.Join(root, cloudHostnameCfg))
	if err != nil || string(cfg) != cloudHostnameContent {
		t.Fatalf("cloud-init: %q %v", cfg, err)
	}
}

func TestSetHostnameFailureLeavesFiles(t *testing.T) {
	f := &fakeSystem{failSet: errors.New("access denied")}
	o, root := newSystemOps(t, f)

	hosts := "127.0.1.1 web2\n"
	writeTestFile(t, filepath.Join(root, etcHosts), hosts)

	if _, err := o.setHostname(t.Context(), "web3"); !errors.Is(err, errSystemCall) {
		t.Fatalf("got %v", err)
	}

	got, _ := os.ReadFile(filepath.Join(root, etcHosts))
	if string(got) != hosts {
		t.Fatalf("hosts changed: %q", got)
	}
}

func TestUpdateHosts(t *testing.T) {
	cases := []struct{ name, in, host, want string }{
		{"replace", "127.0.1.1 old\n", "web3", "127.0.1.1\tweb3\n"},
		{"fqdn", "127.0.1.1 old\n", "web3.example.org", "127.0.1.1\tweb3.example.org web3\n"},
		{"after loopback", "127.0.0.1 localhost\n::1 localhost\n", "web3", "127.0.0.1 localhost\n127.0.1.1\tweb3\n::1 localhost\n"},
		{"empty", "", "web3", "127.0.1.1\tweb3\n"},
		{"no newline", "10.0.0.1 db", "web3", "10.0.0.1 db\n127.0.1.1\tweb3"},
		{"comment kept", "# 127.0.1.1 old\n127.0.1.1 old\n", "web3", "# 127.0.1.1 old\n127.0.1.1\tweb3\n"},
		{"all lines", "127.0.1.1 a\n127.0.1.1 b\n", "web3", "127.0.1.1\tweb3\n127.0.1.1\tweb3\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(updateHosts([]byte(tc.in), tc.host)); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestReboot(t *testing.T) {
	f := &fakeSystem{}
	o, _ := newSystemOps(t, f)

	var delay time.Duration

	var pending func()

	o.after = func(d time.Duration, fn func()) { delay, pending = d, fn }

	resp, err := o.reboot(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if resp.DelaySeconds != 2 || delay != RebootDelay {
		t.Fatalf("delay %v %v", resp.DelaySeconds, delay)
	}

	if f.reboots != 0 {
		t.Fatal("rebooted before the delay")
	}

	if _, err := o.reboot(t.Context()); !errors.Is(err, errRebooting) {
		t.Fatalf("second reboot: %v", err)
	}

	pending()

	if f.reboots != 1 {
		t.Fatalf("reboots %d", f.reboots)
	}
}

func TestSystemOpsOverRPC(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	start := func(cfg Config) *agentrpc.Client {
		s := NewServer(cfg, uint32(os.Getuid()))

		dir, err := os.MkdirTemp("", "sys")
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = os.RemoveAll(dir) })

		sock := filepath.Join(dir, "a.sock")
		if err := s.Listen(ctx, sock, -1); err != nil {
			t.Fatal(err)
		}

		go func() { _ = s.Serve(ctx) }()

		return agentrpc.NewClient(sock)
	}

	off := start(Config{Version: "t"})

	_, err := agentapi.SystemInfoOp.Call(ctx, off, agentapi.SystemInfoRequest{})
	if !strings.Contains(err.Error(), "not available") {
		t.Fatalf("without System: %v", err)
	}

	f := &fakeSystem{st: sysconf.State{Hostname: "web2", Timezone: "UTC"}, zones: []string{"UTC"}}
	on := start(Config{Version: "t", System: f})

	info, err := agentapi.SystemInfoOp.Call(ctx, on, agentapi.SystemInfoRequest{})
	if err != nil || info.Hostname != "web2" {
		t.Fatalf("info %+v %v", info, err)
	}

	for _, bad := range []string{"Web2", "web2;reboot", "-x"} {
		if _, err := agentapi.SystemHostnameSet.Call(ctx, on, agentapi.SystemHostnameRequest{Name: bad}); err == nil {
			t.Fatalf("hostname %q accepted", bad)
		}
	}

	if _, err := agentapi.SystemTimezoneSet.Call(ctx, on, agentapi.SystemTimezoneRequest{Zone: "../../etc/passwd"}); err == nil {
		t.Fatal("path accepted as zone")
	}

	if len(f.calls) != 0 {
		t.Fatalf("invalid requests reached systemd: %v", f.calls)
	}
}

func TestWriteHostsOnBindMount(t *testing.T) {
	f := &fakeSystem{}
	o, root := newSystemOps(t, f)

	writeTestFile(t, filepath.Join(root, etcHosts), "127.0.1.1 old\n")

	renameFile = func(string, string) error { return &os.LinkError{Op: "rename", Err: syscall.EBUSY} }

	t.Cleanup(func() { renameFile = os.Rename })

	if _, err := o.setHostname(t.Context(), "web3"); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(filepath.Join(root, etcHosts))
	if string(got) != "127.0.1.1\tweb3\n" {
		t.Fatalf("hosts %q", got)
	}

	// No temporary file is left behind.
	entries, _ := os.ReadDir(filepath.Join(root, "etc"))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".hosts.") {
			t.Fatalf("left %s", e.Name())
		}
	}
}

func TestSystemInfoUpdates(t *testing.T) {
	o, root := newSystemOps(t, &fakeSystem{st: sysconf.State{Hostname: "web2"}})

	writeTestFile(t, filepath.Join(root, "var/lib/dpkg/status"), "Package: hostashi\nStatus: install ok installed\nArchitecture: amd64\nVersion: 0.12.0\n")
	writeTestFile(t, filepath.Join(root, "etc/apt/sources.list.d/hostashi.sources"), "Types: deb\nURIs: https://get.hostashi.com/apt\nSuites: stable\nComponents: main\n")

	list := filepath.Join(root, "var/lib/apt/lists/get.hostashi.com_apt_dists_stable_main_binary-amd64_Packages")
	writeTestFile(t, list, "Package: hostashi\nVersion: 0.13.0\n")

	when := time.Date(2026, 11, 26, 6, 0, 0, 0, time.UTC)
	if err := os.Chtimes(list, when, when); err != nil {
		t.Fatal(err)
	}

	info, err := o.info(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := agentapi.SystemUpdates{Installed: "0.12.0", Available: "0.13.0", Update: true, ListedAt: "2026-11-26T06:00:00Z"}
	if info.Updates == nil || *info.Updates != want {
		t.Fatalf("updates %+v, want %+v", info.Updates, want)
	}

	writeTestFile(t, filepath.Join(root, "etc/apt/apt.conf.d/52hostashi-unattended-upgrades"), "\t\"origin=Hostashi,codename=stable\";\n")

	if info, err = o.info(t.Context()); err != nil || !info.Updates.Auto {
		t.Fatalf("auto: %+v %v", info.Updates, err)
	}
}
