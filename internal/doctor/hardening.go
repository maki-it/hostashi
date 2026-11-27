// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"bufio"
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
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
		return warnT(id, "fw.off", "fw.off.hint", nil)
	}

	if err != nil {
		return failT(id, "read.error", "", P{"path": FirewallFile, "error": describe(err)})
	}

	unit := P{"unit": FirewallUnit, "step": "firewall"}

	if !h.exists(systemdWants + "/sysinit.target.wants/" + FirewallUnit) {
		return failT(id, "fw.disabled", "fw.unit.hint", unit)
	}

	if h.Active == nil {
		return warnT(id, "fw.state", "", nil)
	}

	active, err := h.Active(ctx, FirewallUnit)
	if err != nil {
		return warnT(id, "fw.state.error", "", P{"error": err.Error()})
	}

	if !active {
		return failT(id, "fw.inactive", "fw.unit.hint", unit)
	}

	tcp, udp := firewallPorts(b)
	if len(tcp) == 0 {
		return failT(id, "fw.noport", "installer.step.hint", P{"path": FirewallFile, "step": "firewall"})
	}

	ssh, _ := h.sshd()

	var closed []string

	for _, p := range ssh.ports {
		if !portOpen(tcp, p) {
			closed = append(closed, strconv.Itoa(p))
		}
	}

	if len(closed) > 0 {
		return failT(id, "fw.ssh.closed", "fw.ssh.closed.hint", P{"ports": strings.Join(closed, ", ")})
	}

	if len(udp) > 0 {
		return okT(id, "fw.on.udp", P{"tcp": strings.Join(tcp, ", "), "udp": strings.Join(udp, ", ")})
	}

	return okT(id, "fw.on", P{"tcp": strings.Join(tcp, ", ")})
}

// Updates checks the automatic security updates (ADR-0033).
func (h Hardening) Updates() Check {
	const id = "system.updates"

	step := P{"step": "updates"}

	if !h.exists(UpdatesFile) || !h.exists("/usr/bin/unattended-upgrade") {
		return warnT(id, "updates.off", "installer.step.hint", step)
	}

	for _, t := range UpdateTimers {
		if !h.exists(systemdWants + "/timers.target.wants/" + t) {
			return warnT(id, "updates.timer", "installer.step.hint", P{"timer": t, "step": "updates"})
		}
	}

	st, err := os.Stat(h.path(UpdatesStamp))
	if errors.Is(err, os.ErrNotExist) {
		return okT(id, "updates.norun", nil)
	}

	if err != nil {
		return warnT(id, "read.error", "", P{"path": UpdatesStamp, "error": describe(err)})
	}

	last := st.ModTime()
	if h.now().Sub(last) > UpdatesStaleAfter {
		return warnT(id, "updates.stale", "updates.stale.hint", P{"date": last.UTC().Format("2006-01-02")})
	}

	return okT(id, "updates.ok", P{"time": last.UTC().Format("2006-01-02 15:04")})
}

// Reboot reports a reboot that updated packages wait for. The server never
// reboots by itself (ADR-0033).
func (h Hardening) Reboot() Check {
	const id = "system.reboot"

	if !h.exists(RebootRequired) {
		return okT(id, "reboot.no", nil)
	}

	key, params := "reboot.needed", P{}

	if b, err := os.ReadFile(h.path(RebootRequired + ".pkgs")); err == nil {
		var pkgs []string
		for p := range strings.FieldsSeq(string(b)) {
			if !slices.Contains(pkgs, p) {
				pkgs = append(pkgs, p)
			}
		}

		if len(pkgs) > 0 {
			key, params["packages"] = "reboot.needed.packages", strings.Join(pkgs, ", ")
		}
	}

	return warnT(id, key, "reboot.hint", params)
}

var phpUnitRe = regexp.MustCompile(`/(hostashi-php-[0-9]\.[0-9]@([a-z][a-z0-9_-]{1,31})\.service)$`)

// PHP reports the sites whose PHP master or workers still run a binary or
// library that an update replaced (ADR-0033). The update hook restarts
// them, so this shows only when the hook failed.
func (h Hardening) PHP() Check {
	const id = "system.php"

	dirs, err := os.ReadDir(h.path("/proc"))
	if err != nil {
		return warnT(id, "read.error", "", P{"path": "/proc", "error": describe(err)})
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
		return okT(id, "php.current", nil)
	}

	sites := make([]string, 0, len(stale))
	for s := range stale {
		sites = append(sites, s)
	}

	slices.Sort(sites)

	return warnT(id, "php.stale", "php.stale.hint", P{"sites": strings.Join(sites, ", "), "unit": stale[sites[0]]})
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
		return warnT(id, "read.failed", "", P{"path": SSHDConfig})
	}

	switch {
	case c.root == "yes":
		return warnT(id, "ssh.root", "ssh.root.hint", nil)
	case c.password:
		return warnT(id, "ssh.password", "ssh.hint", nil)
	}

	return okT(id, "ssh.keys", nil)
}

// Sysctl compares the live kernel settings with the installer's file
// (ADR-0033). A key this kernel does not have is skipped, as the installer
// does. A file that sorts later may set other values on purpose, so a
// difference is a warning.
func (h Hardening) Sysctl() Check {
	const id = "system.sysctl"

	f, err := os.Open(h.path(SysctlFile))
	if errors.Is(err, os.ErrNotExist) {
		return warnT(id, "sysctl.missing", "installer.step.hint", P{"step": "sysctl"})
	}

	if err != nil {
		return warnT(id, "read.error", "", P{"path": SysctlFile, "error": describe(err)})
	}

	defer f.Close()

	var drift, abweichung []string

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
			abweichung = append(abweichung, key+" = "+live+" (soll "+want+")")
		}
	}

	if len(drift) > 0 {
		return warnT(id, "sysctl.drift", "sysctl.drift.hint", P{"drift": strings.Join(drift, ", "), "drift@de": strings.Join(abweichung, ", "), "step": "sysctl"})
	}

	return okT(id, "sysctl.ok", P{"count": strconv.Itoa(n)})
}

// FirewallPanel reports the panel's firewall rules (ADR-0034): what they
// open and allow, and a change that waits for its confirmation. ok is false
// when the firewall is off; the check is then left out, system.firewall
// already says so.
func FirewallPanel(st agentapi.FirewallStatus, err error, now time.Time) (Check, bool) {
	const id = "system.firewall.panel"

	if err != nil {
		return warnT(id, "fwpanel.error", "", P{"error": err.Error()}), true
	}

	if !st.Enabled {
		return Check{}, false
	}

	if st.Pending != nil {
		left := time.Unix(st.Pending.Deadline, 0).Sub(now).Round(time.Second)

		return warnT(id, "fwpanel.pending", "fwpanel.pending.hint", P{"left": max(left, 0).String()}), true
	}

	var parts, teile []string

	if n := len(st.State.Open); n > 0 {
		parts = append(parts, plural(n, "opened port rule", "opened port rules"))
		teile = append(teile, plural(n, "Regel für einen offenen Port", "Regeln für offene Ports"))
	}

	if n := len(st.State.SSHAllow); n > 0 {
		parts = append(parts, "SSH only from "+plural(n, "address", "addresses"))
		teile = append(teile, "SSH nur von "+plural(n, "Adresse", "Adressen"))
	}

	if n := len(st.State.PanelAllow); n > 0 {
		parts = append(parts, "the panel only from "+plural(n, "address", "addresses"))
		teile = append(teile, "das Panel nur von "+plural(n, "Adresse", "Adressen"))
	}

	if len(parts) == 0 {
		return okT(id, "fwpanel.none", nil), true
	}

	return okT(id, "fwpanel.rules", P{"rules": strings.Join(parts, "; "), "rules@de": strings.Join(teile, "; ")}), true
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}

	return strconv.Itoa(n) + " " + many
}
