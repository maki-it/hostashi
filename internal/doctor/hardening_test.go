// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// tree writes files below a new root; a value of "->" makes a symlink to
// /dev/null, as systemctl enable does.
func tree(t *testing.T, files map[string]string) string {
	t.Helper()

	root := t.TempDir()

	for p, content := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}

		var err error
		if content == "->" {
			err = os.Symlink("/dev/null", full)
		} else {
			err = os.WriteFile(full, []byte(content), 0o600)
		}

		if err != nil {
			t.Fatal(err)
		}
	}

	return root
}

const rules = `table inet hostashi {
	chain input {
		tcp dport { 22, 80, 443, 2222, 8443 } accept
		udp dport { 443, 8443 } accept
	}
}
`

func active(on bool, err error) func(context.Context, string) (bool, error) {
	return func(_ context.Context, unit string) (bool, error) {
		if unit != FirewallUnit {
			return false, errors.New("unexpected unit " + unit)
		}

		return on, err
	}
}

func TestFirewall(t *testing.T) {
	ctx := context.Background()
	enabled := "/etc/systemd/system/sysinit.target.wants/" + FirewallUnit

	for _, tc := range []struct {
		name   string
		files  map[string]string
		active func(context.Context, string) (bool, error)
		status string
		want   string
	}{
		{"off", nil, active(false, nil), agentapi.DoctorWarn, "--no-firewall"},
		{"not enabled", map[string]string{FirewallFile: rules}, active(true, nil), agentapi.DoctorFail, "not enabled"},
		{"inactive", map[string]string{FirewallFile: rules, enabled: "->"}, active(false, nil), agentapi.DoctorFail, "not active"},
		{"systemd error", map[string]string{FirewallFile: rules, enabled: "->"}, active(false, errors.New("boom")), agentapi.DoctorWarn, "boom"},
		{"on", map[string]string{FirewallFile: rules, enabled: "->", SSHDConfig: "Port 22\nPort 2222\n"}, active(true, nil), agentapi.DoctorOK, "TCP 22, 80, 443, 2222, 8443; UDP 443, 8443"},
		{"ssh port closed", map[string]string{FirewallFile: rules, enabled: "->", SSHDConfig: "Include /etc/ssh/sshd_config.d/*.conf\n", "/etc/ssh/sshd_config.d/port.conf": "Port 2200\n"}, active(true, nil), agentapi.DoctorFail, "port 2200"},
		{"ssh default port", map[string]string{FirewallFile: rules, enabled: "->", SSHDConfig: "# Port 2200\n"}, active(true, nil), agentapi.DoctorOK, "TCP 22"},
		{"no tcp", map[string]string{FirewallFile: "table inet hostashi {}\n", enabled: "->"}, active(true, nil), agentapi.DoctorFail, "no TCP port"},
	} {
		c := Hardening{Root: tree(t, tc.files), Active: tc.active}.Firewall(ctx)
		if c.ID != "system.firewall" || c.Status != tc.status || !strings.Contains(c.Summary, tc.want) {
			t.Errorf("%s: %+v", tc.name, c)
		}
	}
}

func TestPortOpen(t *testing.T) {
	list := []string{"22", "80", "6000-6010"}
	for port, want := range map[int]bool{22: true, 23: false, 6000: true, 6005: true, 6010: true, 6011: false} {
		if got := portOpen(list, port); got != want {
			t.Errorf("port %d: %v", port, got)
		}
	}
}

func TestUpdates(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	base := map[string]string{
		UpdatesFile:                   "x",
		"/usr/bin/unattended-upgrade": "x",
		"/etc/systemd/system/timers.target.wants/apt-daily.timer":         "->",
		"/etc/systemd/system/timers.target.wants/apt-daily-upgrade.timer": "->",
	}

	with := func(drop string, stamp time.Duration) Hardening {
		files := map[string]string{}

		for k, v := range base {
			if k != drop {
				files[k] = v
			}
		}

		if stamp > 0 {
			files[UpdatesStamp] = ""
		}

		root := tree(t, files)

		if stamp > 0 {
			at := now.Add(-stamp)
			if err := os.Chtimes(filepath.Join(root, UpdatesStamp), at, at); err != nil {
				t.Fatal(err)
			}
		}

		return Hardening{Root: root, Now: func() time.Time { return now }}
	}

	for _, tc := range []struct {
		name   string
		h      Hardening
		status string
		want   string
	}{
		{"no run yet", with("", 0), agentapi.DoctorOK, "no run yet"},
		{"recent", with("", 5*time.Hour), agentapi.DoctorOK, "last run 2026-11-20 07:00 UTC"},
		{"stale", with("", 4*24*time.Hour), agentapi.DoctorWarn, "2026-11-16"},
		{"no settings", with(UpdatesFile, 0), agentapi.DoctorWarn, "not installed automatically"},
		{"no package", with("/usr/bin/unattended-upgrade", 0), agentapi.DoctorWarn, "not installed automatically"},
		{"timer off", with("/etc/systemd/system/timers.target.wants/apt-daily-upgrade.timer", 0), agentapi.DoctorWarn, "apt-daily-upgrade.timer"},
	} {
		c := tc.h.Updates()
		if c.ID != "system.updates" || c.Status != tc.status || !strings.Contains(c.Summary, tc.want) {
			t.Errorf("%s: %+v", tc.name, c)
		}
	}
}

func TestReboot(t *testing.T) {
	if c := (Hardening{Root: tree(t, nil)}).Reboot(); c.Status != agentapi.DoctorOK {
		t.Errorf("no file: %+v", c)
	}

	c := Hardening{Root: tree(t, map[string]string{RebootRequired: "", RebootRequired + ".pkgs": "linux-image-amd64\nlibc6\nlinux-image-amd64\n"})}.Reboot()
	if c.Status != agentapi.DoctorWarn || !strings.Contains(c.Summary, "(linux-image-amd64, libc6)") || c.Hint == "" {
		t.Errorf("reboot needed: %+v", c)
	}
}

func TestPHP(t *testing.T) {
	const (
		fresh = "55d0-55e0 r-xp 00000000 08:01 123 /usr/sbin/php-fpm8.4\n7f00-7f10 r--p 00000000 08:01 7 /usr/lib/x86_64-linux-gnu/libc.so.6\n"
		old   = "55d0-55e0 r-xp 00000000 08:01 123 /usr/sbin/php-fpm8.4 (deleted)\n"
		// A deleted file outside /usr (a worker's temp file) is not an update.
		tmp = fresh + "7f20-7f30 rw-s 00000000 00:01 9 /tmp/sess (deleted)\n"
	)

	root := tree(t, map[string]string{
		"/proc/10/cgroup": "0::/hostashi.slice/hostashi-site-shop.slice/hostashi-php-8.4@shop.service\n",
		"/proc/10/maps":   fresh,
		"/proc/11/cgroup": "0::/system.slice/hostashi-php-8.4@blog.service\n",
		"/proc/11/maps":   old,
		"/proc/12/cgroup": "0::/system.slice/hostashi-php-8.3@wiki.service\n",
		"/proc/12/maps":   tmp,
		"/proc/13/cgroup": "0::/system.slice/nginx.service\n",
		"/proc/13/maps":   old,
		"/proc/self/maps": old,
	})

	c := Hardening{Root: root}.PHP()
	if c.Status != agentapi.DoctorWarn || !strings.Contains(c.Summary, "PHP of blog ") || !strings.Contains(c.Hint, "hostashi-php-8.4@blog.service") {
		t.Errorf("stale PHP: %+v", c)
	}

	if err := os.WriteFile(filepath.Join(root, "proc/11/maps"), []byte(fresh), 0o600); err != nil {
		t.Fatal(err)
	}

	if c := (Hardening{Root: root}).PHP(); c.Status != agentapi.DoctorOK {
		t.Errorf("fresh PHP: %+v", c)
	}
}

func TestSSH(t *testing.T) {
	// Debian 13's sshd_config, shortened, plus Hostashi's drop-in for the
	// sites, whose Match block must not count for the admin.
	const debian = "Include /etc/ssh/sshd_config.d/*.conf\nKbdInteractiveAuthentication no\nUsePAM yes\nX11Forwarding yes\n"

	dropIn := "/etc/ssh/sshd_config.d/10-hostashi.conf"
	sites := "Match Group hostashi-sites\n    PasswordAuthentication no\n"

	for _, tc := range []struct {
		name   string
		files  map[string]string
		status string
		want   string
	}{
		{"debian default", map[string]string{SSHDConfig: debian, dropIn: sites}, agentapi.DoctorWarn, "passwords"},
		{"keys only", map[string]string{SSHDConfig: debian, dropIn: sites, "/etc/ssh/sshd_config.d/50-admin.conf": "PasswordAuthentication no\n"}, agentapi.DoctorOK, "need a key"},
		{"first value wins", map[string]string{SSHDConfig: "PasswordAuthentication=no\nPasswordAuthentication yes\nKbdInteractiveAuthentication no\n"}, agentapi.DoctorOK, "need a key"},
		{"pam asks", map[string]string{SSHDConfig: "PasswordAuthentication no\nUsePAM yes\n"}, agentapi.DoctorWarn, "passwords"},
		{"root with password", map[string]string{SSHDConfig: "PermitRootLogin yes\nPasswordAuthentication no\n"}, agentapi.DoctorWarn, "root"},
		{"match ends the file", map[string]string{SSHDConfig: "Match User admin\n  PasswordAuthentication no\n"}, agentapi.DoctorWarn, "passwords"},
		{"missing", nil, agentapi.DoctorWarn, "cannot read"},
	} {
		c := Hardening{Root: tree(t, tc.files)}.SSH()
		if c.ID != "system.ssh" || c.Status != tc.status || !strings.Contains(c.Summary, tc.want) {
			t.Errorf("%s: %+v", tc.name, c)
		}
	}
}

func TestSSHDPorts(t *testing.T) {
	h := Hardening{Root: tree(t, map[string]string{SSHDConfig: "Port 2222\nListenAddress 0.0.0.0:2200\nListenAddress [::]:2201\nListenAddress ::\nListenAddress 2001:db8::1\nPort 2222\n"})}

	c, found := h.sshd()
	if !found || len(c.ports) != 3 || c.ports[0] != 2200 || c.ports[1] != 2201 || c.ports[2] != 2222 {
		t.Errorf("ports = %v", c.ports)
	}
}

func TestSysctl(t *testing.T) {
	file := "# comment\nkernel.kptr_restrict = 2\nfs.suid_dumpable=0\nkernel.yama.ptrace_scope = 1\nnet.ipv4.conf.all.rp_filter = 2\n"

	// No Yama on this kernel: the key is skipped.
	root := tree(t, map[string]string{
		SysctlFile:                              file,
		"/proc/sys/kernel/kptr_restrict":        "2\n",
		"/proc/sys/fs/suid_dumpable":            "0\n",
		"/proc/sys/net/ipv4/conf/all/rp_filter": "2\n",
	})

	if c := (Hardening{Root: root}).Sysctl(); c.Status != agentapi.DoctorOK || c.Summary != "3 kernel settings match the baseline" {
		t.Errorf("match: %+v", c)
	}

	if err := os.WriteFile(filepath.Join(root, "proc/sys/fs/suid_dumpable"), []byte("2\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if c := (Hardening{Root: root}).Sysctl(); c.Status != agentapi.DoctorWarn || !strings.Contains(c.Summary, "fs.suid_dumpable = 2 (want 0)") {
		t.Errorf("drift: %+v", c)
	}

	if c := (Hardening{Root: tree(t, nil)}).Sysctl(); c.Status != agentapi.DoctorWarn || !strings.Contains(c.Hint, "step sysctl") {
		t.Errorf("no file: %+v", c)
	}
}

func TestFirewallPanel(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)

	if _, shown := FirewallPanel(agentapi.FirewallStatus{}, nil, now); shown {
		t.Error("a firewall that is off is reported by system.firewall, not here")
	}

	tests := []struct {
		name   string
		st     agentapi.FirewallStatus
		err    error
		status string
		want   string
	}{
		{"no rules", agentapi.FirewallStatus{Enabled: true}, nil, agentapi.DoctorOK, "open to everyone"},
		{"rules", agentapi.FirewallStatus{Enabled: true, State: agentapi.FirewallState{
			Open:     []agentapi.FirewallRule{{Proto: "tcp", From: 3000}, {Proto: "udp", From: 6000, To: 6010}},
			SSHAllow: []string{"192.0.2.1"}, PanelAllow: []string{"192.0.2.1", "2001:db8::/32"},
		}}, nil, agentapi.DoctorOK, "2 opened port rules; SSH only from 1 address; the panel only from 2 addresses"},
		{
			"pending",
			agentapi.FirewallStatus{Enabled: true, Pending: &agentapi.FirewallPending{Deadline: now.Unix() + 90}},
			nil,
			agentapi.DoctorWarn, "rolls back in 1m30s",
		},
		{"unreadable", agentapi.FirewallStatus{}, errors.New("boom"), agentapi.DoctorWarn, "boom"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, shown := FirewallPanel(tt.st, tt.err, now)
			if !shown || c.Status != tt.status || !strings.Contains(c.Summary, tt.want) {
				t.Errorf("got %v %+v, want %s containing %q", shown, c, tt.status, tt.want)
			}
		})
	}
}
