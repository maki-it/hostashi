// SPDX-License-Identifier: AGPL-3.0-or-later

// Package doctor holds the read-only checks of hostashi doctor: each one
// looks at one part of the server and says what it found and, if something
// is wrong, what to do. None of them changes anything. The agent runs them
// (agent/doctor.go) because only root can see everything they read.
package doctor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Check is one result.
type Check = agentapi.DoctorCheck

// Supported is the support matrix (ADR-0020): distribution ID and version.
var Supported = map[string]string{"debian": "13", "ubuntu": "26.04"}

// Thresholds of the free space on a file system.
const (
	DiskWarnPercent = 15
	DiskFailPercent = 5
	DiskFailBytes   = 1 << 30 // below 1 GiB free is a failure on any size
)

// Thresholds of a site's disk use against its limit, as in the panel.
const (
	UsageWarnPercent = 80
	UsageFailPercent = 95
)

// Days before a certificate expires. lego renews 30 days before, so a
// certificate this close to the end was not renewed.
const (
	CertWarnDays = 21
	CertFailDays = 7
)

// System reads the server. The zero value reads the real paths.
type System struct {
	OSRelease string                                     // default /etc/os-release
	MountInfo string                                     // default /proc/self/mountinfo
	Statfs    func(path string, st *unix.Statfs_t) error // default unix.Statfs
}

func (s System) osRelease() string {
	if s.OSRelease == "" {
		return "/etc/os-release"
	}

	return s.OSRelease
}

func (s System) mountInfo() string {
	if s.MountInfo == "" {
		return "/proc/self/mountinfo"
	}

	return s.MountInfo
}

func (s System) statfs(path string, st *unix.Statfs_t) error {
	if s.Statfs == nil {
		return unix.Statfs(path, st)
	}

	return s.Statfs(path, st)
}

func ok(id, summary string) Check { return Check{ID: id, Status: agentapi.DoctorOK, Summary: summary} }

func warn(id, summary, hint string) Check {
	return Check{ID: id, Status: agentapi.DoctorWarn, Summary: summary, Hint: hint}
}

func fail(id, summary, hint string) Check {
	return Check{ID: id, Status: agentapi.DoctorFail, Summary: summary, Hint: hint}
}

// OS checks the distribution against the support matrix.
func (s System) OS() Check {
	const id = "system.os"

	b, err := os.ReadFile(s.osRelease())
	if err != nil {
		return warn(id, "cannot read "+s.osRelease(), "Hostashi supports Debian 13 and Ubuntu 26.04.")
	}

	vals := map[string]string{}

	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, found := strings.Cut(sc.Text(), "=")
		if found {
			vals[k] = strings.Trim(v, `"'`)
		}
	}

	name := vals["PRETTY_NAME"]
	if name == "" {
		name = vals["ID"] + " " + vals["VERSION_ID"]
	}

	if want, known := Supported[vals["ID"]]; known && want == vals["VERSION_ID"] {
		return ok(id, name+" is supported")
	}

	return warn(id, name+" is not in the support matrix", "Hostashi supports Debian 13 and Ubuntu 26.04. Other systems are untested.")
}

// Disk checks the free space of each file system that holds one of paths.
// Paths on the same file system are reported once, under the first path.
func (s System) Disk(paths []string) []Check {
	const id = "system.disk"

	var (
		out  []Check
		seen = map[unix.Fsid]bool{}
	)

	for _, p := range paths {
		var st unix.Statfs_t
		if err := s.statfs(p, &st); err != nil {
			out = append(out, fail(id, p+": "+describe(err), "Check that the directory exists and its file system is mounted."))

			continue
		}

		if seen[st.Fsid] {
			continue
		}

		seen[st.Fsid] = true

		if st.Blocks == 0 {
			continue // a pseudo file system has no size
		}

		bsize := uint64(st.Bsize) //nolint:gosec // the block size is positive
		total, free := st.Blocks*bsize, st.Bavail*bsize
		pct := free * 100 / total
		summary := fmt.Sprintf("%s: %s free of %s (%d %%)", p, Bytes(free), Bytes(total), pct)

		switch {
		case pct < DiskFailPercent || free < DiskFailBytes:
			out = append(out, fail(id, summary, "Free space now: sites, databases and backups fail when the disk is full."))
		case pct < DiskWarnPercent:
			out = append(out, warn(id, summary, "The disk is filling up. Check the sites' disk use and old backups."))
		default:
			out = append(out, ok(id, summary))
		}
	}

	return out
}

// Quota reports whether the file system of dir enforces project quota.
func Quota(dir string, available bool, err error) Check {
	const id = "system.quota"

	switch {
	case err != nil:
		return fail(id, dir+": cannot read the quota state: "+describe(err), "")
	case available:
		return ok(id, dir+": disk limits are enforced (project quota)")
	default:
		return warn(id, dir+": no project quota, sites get no disk limit",
			"Run the installer again: it enables quota with one reboot, or says why it cannot.")
	}
}

// Proc checks that /proc hides other users' processes (ADR-0032).
func (s System) Proc() Check {
	const id = "system.proc"

	b, err := os.ReadFile(s.mountInfo())
	if err != nil {
		return warn(id, "cannot read "+s.mountInfo(), "")
	}

	for line := range strings.SplitSeq(string(b), "\n") {
		// mount ID, parent, dev, root, mount point, options, ... " - " type source super options
		pre, post, found := strings.Cut(line, " - ")
		if !found {
			continue
		}

		f := strings.Fields(pre)
		if len(f) < 5 || f[4] != "/proc" {
			continue
		}

		for o := range strings.SplitSeq(lastField(post), ",") {
			if o == "hidepid=invisible" || o == "hidepid=2" {
				return ok(id, "/proc hides other users' processes")
			}
		}

		return warn(id, "/proc shows every process to every user", "Run the installer again (step harden).")
	}

	return warn(id, "/proc is not mounted", "")
}

func lastField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}

	return f[len(f)-1]
}

// PoolMode checks that a pool of homes is root 0711, so site users cannot
// list each other (ADR-0032).
func PoolMode(dir string) Check {
	const id = "system.pool"

	fi, err := os.Stat(dir)
	if err != nil {
		return fail(id, dir+": "+describe(err), "")
	}

	st, isUnix := fi.Sys().(*syscall.Stat_t)
	if fi.Mode().Perm() == 0o711 && isUnix && st.Uid == 0 {
		return ok(id, dir+" is root 0711")
	}

	return warn(id, fmt.Sprintf("%s has mode %04o", dir, fi.Mode().Perm()),
		"Site users can list the other sites' homes. Run the installer again (step harden).")
}

// Socket checks that a service accepts connections on its Unix socket.
func Socket(ctx context.Context, id, name, path, hint string) Check {
	d := net.Dialer{Timeout: 2 * time.Second}

	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return fail(id, name+" does not answer on "+path+": "+describe(err), hint)
	}

	_ = c.Close()

	return ok(id, name+" is running")
}

// Panel asks the panel for its health over its Unix socket.
func Panel(ctx context.Context, socket string) Check {
	const id = "service.panel"

	hint := "Check the panel: systemctl status hostashi-panel, journalctl -u hostashi-panel."
	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var d net.Dialer

				return d.DialContext(ctx, "unix", socket)
			},
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://panel/api/v1/health", nil)
	if err != nil {
		return fail(id, "panel: "+err.Error(), hint)
	}

	// The socket trusts only nginx, which always names the client. This
	// request comes from the server itself.
	req.Header.Set("X-Real-IP", "127.0.0.1")

	resp, err := client.Do(req)
	if err != nil {
		return fail(id, "the panel does not answer on "+socket+": "+describe(err), hint)
	}
	defer resp.Body.Close()

	var h struct {
		Status  string `json:"status"`
		Agent   string `json:"agent"`
		DB      string `json:"db"`
		Version string `json:"version"`
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<12)).Decode(&h); err != nil || resp.StatusCode != http.StatusOK || h.Status != "ok" {
		return fail(id, fmt.Sprintf("the panel reports a problem (HTTP %d, agent %q, database %q)", resp.StatusCode, h.Agent, h.DB), hint)
	}

	return ok(id, "the panel is running, version "+h.Version)
}

// Usage checks a site's disk use against its limit. A limit of 0 means
// unlimited, which is a choice and not a problem.
func Usage(site, what string, used, limit uint64, format func(uint64) string) Check {
	const id = "site.disk"

	if limit == 0 {
		return Check{ID: id, Site: site, Status: agentapi.DoctorOK, Summary: what + ": " + format(used) + ", unlimited"}
	}

	pct := used * 100 / limit
	summary := fmt.Sprintf("%s: %s of %s (%d %%)", what, format(used), format(limit), pct)
	c := Check{ID: id, Site: site, Status: agentapi.DoctorOK, Summary: summary}

	switch {
	case pct >= UsageFailPercent:
		c.Status, c.Hint = agentapi.DoctorFail, "Almost full: writes fail at the limit. Raise the limit or free space."
	case pct >= UsageWarnPercent:
		c.Status, c.Hint = agentapi.DoctorWarn, "Filling up. Raise the limit or free space."
	}

	return c
}

// Cert checks when a site's certificate expires.
func Cert(site string, notAfter, now time.Time) Check {
	const id = "site.tls"

	left := notAfter.Sub(now)
	days := int(left.Hours() / 24)
	c := Check{ID: id, Site: site, Status: agentapi.DoctorOK, Summary: fmt.Sprintf("certificate valid until %s (%d days)", notAfter.UTC().Format("2006-01-02"), days)}
	hint := "The renewal did not work. Check the site's domains in the panel and the panel's log (journalctl -u hostashi-panel)."

	switch {
	case left <= 0:
		c.Status, c.Summary, c.Hint = agentapi.DoctorFail, "certificate expired on "+notAfter.UTC().Format("2006-01-02"), hint
	case days < CertFailDays:
		c.Status, c.Hint = agentapi.DoctorFail, hint
	case days < CertWarnDays:
		c.Status, c.Hint = agentapi.DoctorWarn, hint
	}

	return c
}

// Bytes formats a size in binary units.
func Bytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// describe shortens common errors.
func describe(err error) string {
	switch {
	case errors.Is(err, os.ErrNotExist):
		return "not found"
	case errors.Is(err, os.ErrPermission):
		return "permission denied"
	case errors.Is(err, unix.ECONNREFUSED):
		return "connection refused"
	}

	return err.Error()
}
