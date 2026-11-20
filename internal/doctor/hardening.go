// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The checks of the hardening baseline (ADR-0033): the firewall, the
// security updates, a pending reboot, PHP masters on a replaced binary and
// the admin's SSH logins, and the kernel settings. They read files only; the one exception is the
// firewall unit's state, which the caller asks systemd for (Hardening.Active).

// Paths of the baseline, below Hardening.Root.
const (
	FirewallFile   = "/etc/hostashi/firewall.nft"
	FirewallUnit   = "hostashi-firewall.service"
	UpdatesFile    = "/etc/apt/apt.conf.d/52hostashi-unattended-upgrades"
	UpdatesStamp   = "/var/lib/apt/periodic/unattended-upgrades-stamp"
	RebootRequired = "/run/reboot-required"
	SSHDConfig     = "/etc/ssh/sshd_config"
	SysctlFile     = "/etc/sysctl.d/90-hostashi.conf"
	systemdWants   = "/etc/systemd/system"
)

// UpdatesStaleAfter is how old the last run of unattended-upgrades may be.
// It runs every day, so three days without one means the timers stopped.
const UpdatesStaleAfter = 72 * time.Hour

// UpdateTimers are the apt timers the installer enables (step updates).
var UpdateTimers = []string{"apt-daily.timer", "apt-daily-upgrade.timer"}

// Hardening reads the files of the baseline. The zero value reads the
// server, except Active, which must be set for the firewall check.
type Hardening struct {
	Root string // prefixed to every path; empty means /
	// Active reports whether a systemd unit is active.
	Active func(ctx context.Context, unit string) (bool, error)
	Now    func() time.Time
}

func (h Hardening) path(p string) string {
	if h.Root == "" {
		return p
	}

	return filepath.Join(h.Root, p)
}

func (h Hardening) now() time.Time {
	if h.Now == nil {
		return time.Now()
	}

	return h.Now()
}

func (h Hardening) exists(p string) bool {
	_, err := os.Lstat(h.path(p))

	return err == nil
}

var dportRe = regexp.MustCompile(`^\s*(tcp|udp) dport \{ ([0-9, -]+) \} accept\s*$`)

// firewallPorts reads the port lists of the installer's rules file.
func firewallPorts(b []byte) (tcp, udp []string) {
	for line := range strings.SplitSeq(string(b), "\n") {
		m := dportRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		var ports []string

		for p := range strings.SplitSeq(m[2], ",") {
			if p = strings.TrimSpace(p); p != "" {
				ports = append(ports, p)
			}
		}

		if m[1] == "tcp" {
			tcp = append(tcp, ports...)
		} else {
			udp = append(udp, ports...)
		}
	}

	return tcp, udp
}

// portOpen reports whether port is in a list of ports and ranges.
func portOpen(list []string, port int) bool {
	for _, p := range list {
		lo, hi, isRange := strings.Cut(p, "-")
		if !isRange {
			hi = lo
		}

		a, err1 := strconv.Atoi(lo)
		b, err2 := strconv.Atoi(hi)

		if err1 == nil && err2 == nil && a <= port && port <= b {
			return true
		}
	}

	return false
}

// Firewall checks Hostashi's own nftables table (ADR-0033): that the unit
// is enabled and active, and that every port sshd is configured for is
// open, so nobody is locked out by the next reload.
func (h Hardening) Firewall(ctx context.Context) Check {
	const id = "system.firewall"

	b, err := os.ReadFile(h.path(FirewallFile))
	if errors.Is(err, os.ErrNotExist) {
		return warn(id, "Hostashi's firewall is off (--no-firewall)",
			"Make sure a firewall in front of the server lets through only SSH, 80, 443 and the panel port.")
	}

	if err != nil {
		return fail(id, "cannot read "+FirewallFile+": "+describe(err), "")
	}

	hint := "Check: systemctl status " + FirewallUnit + ", journalctl -u " + FirewallUnit + ". Run the installer again (step firewall)."

	if !h.exists(systemdWants + "/sysinit.target.wants/" + FirewallUnit) {
		return fail(id, "the firewall is not enabled and is gone after a reboot", hint)
	}

	if h.Active == nil {
		return warn(id, "cannot ask systemd for the firewall's state", "")
	}

	active, err := h.Active(ctx, FirewallUnit)
	if err != nil {
		return warn(id, "cannot ask systemd for the firewall's state: "+err.Error(), "")
	}

	if !active {
		return fail(id, "the firewall unit is not active, so its rules may not be loaded", hint)
	}

	tcp, udp := firewallPorts(b)
	if len(tcp) == 0 {
		return fail(id, FirewallFile+" opens no TCP port", "Run the installer again (step firewall).")
	}

	ssh, _ := h.sshd()

	var closed []string

	for _, p := range ssh.ports {
		if !portOpen(tcp, p) {
			closed = append(closed, strconv.Itoa(p))
		}
	}

	if len(closed) > 0 {
		return fail(id, "sshd is configured for port "+strings.Join(closed, ", ")+", which the firewall does not open",
			"Run the installer again (step firewall) before you log out, or the next reload locks you out.")
	}

	summary := "the firewall is on, open: TCP " + strings.Join(tcp, ", ")
	if len(udp) > 0 {
		summary += "; UDP " + strings.Join(udp, ", ")
	}

	return ok(id, summary)
}

// Updates checks the automatic security updates (ADR-0033).
func (h Hardening) Updates() Check {
	const id = "system.updates"

	hint := "Run the installer again (step updates)."

	if !h.exists(UpdatesFile) || !h.exists("/usr/bin/unattended-upgrade") {
		return warn(id, "security updates are not installed automatically", hint)
	}

	for _, t := range UpdateTimers {
		if !h.exists(systemdWants + "/timers.target.wants/" + t) {
			return warn(id, t+" is not enabled, so no updates run", hint)
		}
	}

	st, err := os.Stat(h.path(UpdatesStamp))
	if errors.Is(err, os.ErrNotExist) {
		return ok(id, "security updates are installed every day (no run yet)")
	}

	if err != nil {
		return warn(id, "cannot read "+UpdatesStamp+": "+describe(err), "")
	}

	last := st.ModTime()
	if h.now().Sub(last) > UpdatesStaleAfter {
		return warn(id, "the last automatic update ran on "+last.UTC().Format("2006-01-02"),
			"Check: systemctl status apt-daily-upgrade.timer, /var/log/unattended-upgrades/.")
	}

	return ok(id, "security updates are installed every day, last run "+last.UTC().Format("2006-01-02 15:04")+" UTC")
}

// Reboot reports a reboot that updated packages wait for. The server never
// reboots by itself (ADR-0033).
func (h Hardening) Reboot() Check {
	const id = "system.reboot"

	if !h.exists(RebootRequired) {
		return ok(id, "no reboot needed")
	}

	summary := "a reboot is needed to finish updates"

	if b, err := os.ReadFile(h.path(RebootRequired + ".pkgs")); err == nil {
		var pkgs []string
		for p := range strings.FieldsSeq(string(b)) {
			if !slices.Contains(pkgs, p) {
				pkgs = append(pkgs, p)
			}
		}

		if len(pkgs) > 0 {
			summary += " (" + strings.Join(pkgs, ", ") + ")"
		}
	}

	return warn(id, summary, "Reboot when it suits you: the server never reboots by itself.")
}

var phpUnitRe = regexp.MustCompile(`/(hostashi-php-[0-9]\.[0-9]@([a-z][a-z0-9_-]{1,31})\.service)$`)

// PHP reports the sites whose PHP master or workers still run a binary or
// library that an update replaced (ADR-0033). The update hook restarts
// them, so this shows only when the hook failed.
func (h Hardening) PHP() Check {
	const id = "system.php"

	dirs, err := os.ReadDir(h.path("/proc"))
	if err != nil {
		return warn(id, "cannot read /proc: "+describe(err), "")
	}

	stale := map[string]string{} // site -> unit

	for _, d := range dirs {
		if _, err := strconv.Atoi(d.Name()); err != nil {
			continue
		}

		unit, site, ok := h.phpUnit(d.Name())
		if !ok || stale[site] != "" {
			continue
		}

		if h.replaced(d.Name()) {
			stale[site] = unit
		}
	}

	if len(stale) == 0 {
		return ok(id, "every site's PHP runs the installed version")
	}

	sites := make([]string, 0, len(stale))
	for s := range stale {
		sites = append(sites, s)
	}

	slices.Sort(sites)

	return warn(id, "PHP of "+strings.Join(sites, ", ")+" still runs a replaced binary after an update",
		fmt.Sprintf("It misses the update's fixes. Restart it: systemctl restart %s.", stale[sites[0]]))
}

// phpUnit returns the per-site PHP unit a process belongs to, from its
// cgroup.
func (h Hardening) phpUnit(pid string) (unit, site string, ok bool) {
	b, err := os.ReadFile(h.path("/proc/" + pid + "/cgroup"))
	if err != nil {
		return "", "", false
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		// cgroup v2: "0::/path"; v1 lines name the controllers.
		_, p, found := strings.Cut(line, "::")
		if !found {
			continue
		}

		if m := phpUnitRe.FindStringSubmatch(p); m != nil {
			return m[1], m[2], true
		}
	}

	return "", "", false
}

// replaced reports whether a process maps a deleted file under /usr, which
// is what a package update leaves behind.
func (h Hardening) replaced(pid string) bool {
	f, err := os.Open(h.path("/proc/" + pid + "/maps"))
	if err != nil {
		return false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := sc.Text()
		if strings.HasSuffix(l, " (deleted)") && strings.Contains(l, " /usr/") {
			return true
		}
	}

	return false
}

// sshdConfig is what SSH reads from sshd's config files.
type sshdConfig struct {
	ports    []int
	password bool   // PasswordAuthentication, or keyboard-interactive (PAM asks for the password)
	root     string // PermitRootLogin
}

// sshd reads sshd's global settings from its config files: the first value
// of a keyword wins, Include is followed, Match blocks are skipped (they
// hold the sites' settings, ADR-0032). It is a reading of the files, not
// sshd -T: a setting sshd takes from elsewhere is not seen.
func (h Hardening) sshd() (sshdConfig, bool) {
	vals := map[string]string{}

	var ports []int

	var read func(file string, depth int) bool

	read = func(file string, depth int) bool {
		f, err := os.Open(h.path(file))
		if err != nil {
			return false
		}
		defer f.Close()

		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fields := strings.Fields(strings.ReplaceAll(sc.Text(), "=", " "))
			if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
				continue
			}

			key := strings.ToLower(fields[0])

			switch key {
			case "match":
				return true // a Match block lasts to the end of its file
			case "include":
				if depth > 4 {
					continue
				}

				for _, pat := range fields[1:] {
					if !filepath.IsAbs(pat) {
						pat = "/etc/ssh/" + pat
					}

					matches, _ := filepath.Glob(h.path(pat))
					for _, m := range matches {
						rel := m
						if h.Root != "" {
							rel = strings.TrimPrefix(m, filepath.Clean(h.Root))
						}

						read(rel, depth+1)
					}
				}
			case "port":
				if p, err := strconv.Atoi(fields[1]); err == nil {
					ports = append(ports, p)
				}
			case "listenaddress":
				if p, found := listenPort(fields[1]); found {
					ports = append(ports, p)
				}
			default:
				if _, seen := vals[key]; !seen {
					vals[key] = strings.ToLower(fields[1])
				}
			}
		}

		return true
	}

	if !read(SSHDConfig, 0) {
		return sshdConfig{}, false
	}

	if len(ports) == 0 {
		ports = []int{22}
	}

	slices.Sort(ports)

	def := func(k, d string) string {
		if v, ok := vals[k]; ok {
			return v
		}

		return d
	}

	pam := def("usepam", "no") == "yes"
	kbd := def("kbdinteractiveauthentication", def("challengeresponseauthentication", "yes")) == "yes"

	return sshdConfig{
		ports:    slices.Compact(ports),
		password: def("passwordauthentication", "yes") == "yes" || (pam && kbd),
		root:     def("permitrootlogin", "prohibit-password"),
	}, true
}

// listenPort returns the port of a ListenAddress value: host:port, or
// [v6]:port for IPv6. A bare IPv6 address has no port.
func listenPort(v string) (int, bool) {
	var p string

	switch {
	case strings.HasPrefix(v, "["):
		_, after, found := strings.Cut(v, "]:")
		if !found {
			return 0, false
		}

		p = after
	case strings.Count(v, ":") == 1:
		_, p, _ = strings.Cut(v, ":")
	default:
		return 0, false
	}

	n, err := strconv.Atoi(p)

	return n, err == nil
}

// SSH warns when the admin's or root's SSH login accepts passwords. The
// installer never changes these settings (ADR-0033, question 4); fail2ban
// slows guessing down but does not stop it.
func (h Hardening) SSH() Check {
	const id = "system.ssh"

	c, found := h.sshd()
	if !found {
		return warn(id, "cannot read "+SSHDConfig, "")
	}

	hint := "Log in with a key first, then set PasswordAuthentication no and KbdInteractiveAuthentication no in a file in /etc/ssh/sshd_config.d/ and run systemctl reload ssh."

	switch {
	case c.root == "yes":
		return warn(id, "root may log in over SSH with a password", "Set PermitRootLogin prohibit-password. "+hint)
	case c.password:
		return warn(id, "SSH accepts passwords for the admin's login", hint)
	}

	return ok(id, "SSH logins outside the sites need a key")
}

// Sysctl compares the live kernel settings with the installer's file
// (ADR-0033). A key this kernel does not have is skipped, as the installer
// does. A file that sorts later may set other values on purpose, so a
// difference is a warning.
func (h Hardening) Sysctl() Check {
	const id = "system.sysctl"

	f, err := os.Open(h.path(SysctlFile))
	if errors.Is(err, os.ErrNotExist) {
		return warn(id, "the kernel settings of the baseline are missing", "Run the installer again (step sysctl).")
	}

	if err != nil {
		return warn(id, "cannot read "+SysctlFile+": "+describe(err), "")
	}

	defer f.Close()

	var drift []string

	n := 0

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		key, want, found := strings.Cut(sc.Text(), "=")
		key = strings.TrimSpace(key)

		if !found || key == "" || strings.HasPrefix(key, "#") {
			continue
		}

		b, err := os.ReadFile(h.path("/proc/sys/" + strings.ReplaceAll(key, ".", "/")))
		if err != nil {
			continue
		}

		n++

		live := strings.Join(strings.Fields(string(b)), " ")
		if want = strings.Join(strings.Fields(want), " "); live != want {
			drift = append(drift, key+" = "+live+" (want "+want+")")
		}
	}

	if len(drift) > 0 {
		return warn(id, "kernel settings differ from the baseline: "+strings.Join(drift, ", "),
			"A file in /etc/sysctl.d/ that sorts later may set them. Otherwise run the installer again (step sysctl).")
	}

	return ok(id, fmt.Sprintf("%d kernel settings match the baseline", n))
}
