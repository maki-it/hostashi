// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The file manager's helper (ADR-0040) runs per site as the site user and
// stops after a few minutes without requests.
const (
	FilesService = "/etc/systemd/system/hostashi-files@.service"
	FilesSocket  = "/etc/systemd/system/hostashi-files@.socket"
	// FilesMaxAge is how long a helper may run before the check warns: the
	// idle stop ends it 5 minutes after the last request, so a day means a
	// connection that never ends or an idle timer that does not work.
	FilesMaxAge = 24 * time.Hour
	// userHZ is the unit of the start time in /proc/<pid>/stat. The kernel
	// reports it as USER_HZ, which is 100 on every architecture Hostashi
	// supports (amd64, arm64).
	userHZ = 100
)

var filesUnitRe = regexp.MustCompile(`/hostashi-files@([a-z][a-z0-9_-]{1,31})\.service$`)

// Files checks the file manager: its unit templates are installed, no
// helper runs as root, and none has run for longer than FilesMaxAge.
func (h Hardening) Files() Check {
	const id = "system.files"

	for _, p := range []string{FilesService, FilesSocket} {
		if !h.exists(p) {
			return fail(id, "the file manager's units are missing ("+p+")", "The Files tab cannot start. Run the installer again (step units).")
		}
	}

	dirs, err := os.ReadDir(h.path("/proc"))
	if err != nil {
		return warn(id, "cannot read /proc: "+describe(err), "")
	}

	boot, bootOK := h.bootTime()
	now := h.now()

	var root, old []string

	for _, d := range dirs {
		if _, err := strconv.Atoi(d.Name()); err != nil {
			continue
		}

		site, ok := h.filesSite(d.Name())
		if !ok {
			continue
		}

		if uid, ok := h.procUID(d.Name()); ok && uid == 0 && !slices.Contains(root, site) {
			root = append(root, site)
		}

		if start, ok := h.procStart(d.Name(), boot); ok && bootOK && now.Sub(start) > FilesMaxAge && !slices.Contains(old, site) {
			old = append(old, site)
		}
	}

	slices.Sort(root)
	slices.Sort(old)

	switch {
	case len(root) > 0:
		return fail(id, "the file manager of "+strings.Join(root, ", ")+" runs as root",
			"It must run as the site user. Stop it (systemctl stop hostashi-files@"+root[0]+".service) and check the unit: systemctl cat hostashi-files@"+root[0]+".service.")
	case len(old) > 0:
		return warn(id, "the file manager of "+strings.Join(old, ", ")+" has run for more than a day",
			fmt.Sprintf("It should stop 5 minutes after the last request. Check: systemctl status hostashi-files@%s.service; stop it with systemctl stop hostashi-files@%s.service.", old[0], old[0]))
	default:
		return ok(id, "the file manager's units are installed; its helpers run as the site users")
	}
}

// filesSite returns the site whose file manager helper a process is, from
// its cgroup.
func (h Hardening) filesSite(pid string) (string, bool) {
	b, err := os.ReadFile(h.path("/proc/" + pid + "/cgroup"))
	if err != nil {
		return "", false
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		_, p, found := strings.Cut(line, "::")
		if !found {
			continue
		}

		if m := filesUnitRe.FindStringSubmatch(p); m != nil {
			return m[1], true
		}
	}

	return "", false
}

// procUID is the real user ID of a process, or its effective one when that
// is root: either way root is what the check looks for.
func (h Hardening) procUID(pid string) (int, bool) {
	b, err := os.ReadFile(h.path("/proc/" + pid + "/status"))
	if err != nil {
		return 0, false
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		rest, found := strings.CutPrefix(line, "Uid:")
		if !found {
			continue
		}

		f := strings.Fields(rest)
		if len(f) < 2 {
			return 0, false
		}

		ruid, err1 := strconv.Atoi(f[0])
		eff, err2 := strconv.Atoi(f[1])

		if err1 != nil || err2 != nil {
			return 0, false
		}

		if eff == 0 {
			return 0, true
		}

		return ruid, true
	}

	return 0, false
}

// bootTime reads btime from /proc/stat.
func (h Hardening) bootTime() (time.Time, bool) {
	b, err := os.ReadFile(h.path("/proc/stat"))
	if err != nil {
		return time.Time{}, false
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		if v, found := strings.CutPrefix(line, "btime "); found {
			s, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return time.Time{}, false
			}

			return time.Unix(s, 0), true
		}
	}

	return time.Time{}, false
}

// procStart is when a process started: field 22 of /proc/<pid>/stat, in
// ticks since boot. The command name (field 2) may hold spaces and
// parentheses, so the fields are counted after its last ")".
func (h Hardening) procStart(pid string, boot time.Time) (time.Time, bool) {
	b, err := os.ReadFile(h.path("/proc/" + pid + "/stat"))
	if err != nil {
		return time.Time{}, false
	}

	s := string(b)

	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return time.Time{}, false
	}

	f := strings.Fields(s[i+1:])
	// After the name: field 3 (state) is f[0], so field 22 is f[19].
	if len(f) < 20 {
		return time.Time{}, false
	}

	ticks, err := strconv.ParseInt(f[19], 10, 64)
	if err != nil {
		return time.Time{}, false
	}

	return boot.Add(time.Duration(ticks) * time.Second / userHZ), true
}
