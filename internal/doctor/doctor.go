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
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/quota"
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
	MountInfo string                                     // default /proc/1/mountinfo
	Statfs    func(path string, st *unix.Statfs_t) error // default unix.Statfs
	Fstab     string                                     // default /etc/fstab
	DevDir    string                                     // default /dev
}

func (s System) osRelease() string {
	if s.OSRelease == "" {
		return "/etc/os-release"
	}

	return s.OSRelease
}

// mountInfo is the mount table of PID 1, the one users log in to. The
// agent's own table is not: ProtectKernelTunables= and
// ProtectControlGroups= give the agent a fresh /proc without hidepid
// (systemd 257, Debian 13), so /proc/self/mountinfo would report a /proc
// that no site user ever sees.
func (s System) mountInfo() string {
	if s.MountInfo == "" {
		return "/proc/1/mountinfo"
	}

	return s.MountInfo
}

func (s System) statfs(path string, st *unix.Statfs_t) error {
	if s.Statfs == nil {
		return unix.Statfs(path, st)
	}

	return s.Statfs(path, st)
}

// ParseOSRelease reads the KEY=value lines of an os-release file, with the
// quotes around a value removed.
func ParseOSRelease(b []byte) map[string]string {
	vals := map[string]string{}

	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, found := strings.Cut(sc.Text(), "=")
		if found {
			vals[k] = strings.Trim(v, `"'`)
		}
	}

	return vals
}

// OS checks the distribution against the support matrix.
func (s System) OS() Check {
	const id = "system.os"

	b, err := os.ReadFile(s.osRelease())
	if err != nil {
		return warnT(id, "read.failed", "os.supported.hint", P{"path": s.osRelease()})
	}

	vals := ParseOSRelease(b)

	name := vals["PRETTY_NAME"]
	if name == "" {
		name = vals["ID"] + " " + vals["VERSION_ID"]
	}

	if want, known := Supported[vals["ID"]]; known && want == vals["VERSION_ID"] {
		return okT(id, "os.supported", P{"name": name})
	}

	return warnT(id, "os.unsupported", "os.unsupported.hint", P{"name": name})
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
			out = append(out, failT(id, "path.error", "disk.error.hint", P{"path": p, "error": describe(err)}))

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
		params := P{"path": p, "free": Bytes(free), "total": Bytes(total), "percent": strconv.FormatUint(pct, 10)}

		switch {
		case pct < DiskFailPercent || free < DiskFailBytes:
			out = append(out, failT(id, "disk.free", "disk.fail.hint", params))
		case pct < DiskWarnPercent:
			out = append(out, warnT(id, "disk.free", "disk.warn.hint", params))
		default:
			out = append(out, okT(id, "disk.free", params))
		}
	}

	return out
}

// Quota reports whether the file system of dir enforces project quota.
func Quota(dir string, st quota.State, err error) Check {
	const id = "system.quota"

	switch {
	case err != nil:
		return failT(id, "quota.error", "", P{"path": dir, "error": describe(err)})
	case st == quota.Enforced:
		return okT(id, "quota.enforced", P{"path": dir})
	case st == quota.Counting:
		return failT(id, "quota.counting", "quota.counting.hint", P{"path": dir})
	default:
		return warnT(id, "quota.off", "quota.off.hint", P{"path": dir})
	}
}

// Proc checks that /proc hides other users' processes (ADR-0032).
func (s System) Proc() Check {
	const id = "system.proc"

	b, err := os.ReadFile(s.mountInfo())
	if err != nil {
		return warnT(id, "read.failed", "", P{"path": s.mountInfo()})
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
				return okT(id, "proc.hidden", nil)
			}
		}

		return warnT(id, "proc.visible", "installer.step.hint", P{"step": "harden"})
	}

	return warnT(id, "proc.unmounted", "", nil)
}

func lastField(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}

	return f[len(f)-1]
}

// PoolState is what the agent knows about a storage pool (ADR-0016).
type PoolState struct {
	Name string
	Path string
	// Unavailable is why the agent refuses the pool: the volume is not
	// mounted, or the marker is missing or belongs to another pool.
	Unavailable error
	// Volume: the pool was added with "hostashi pool add" and must be its
	// own mounted volume. The default pool on the system disk is not.
	Volume bool
	// InFstab: /etc/fstab has an entry for the mount point.
	InFstab bool
}

// PoolVolume checks that a pool is usable and, for a pool on its own
// volume, mounted the way "pool add" demands (spike S9 finding 4). An
// unavailable pool fails: its sites do not start, and the free space
// reported for its path would be the root disk's.
func (s System) PoolVolume(p PoolState) Check {
	const id = "system.pool.volume"

	params := P{"name": p.Name, "path": p.Path}

	if p.Unavailable != nil {
		return failT(id, "pool.unavailable", "pool.unavailable.hint", params)
	}

	if !p.Volume {
		return okT(id, "pool.available", params)
	}

	var st unix.Statfs_t
	if err := s.statfs(p.Path, &st); err != nil {
		params["error"] = describe(err)

		return failT(id, "pool.error", "", params)
	}

	nosuid := st.Flags&unix.ST_NOSUID == 0 || st.Flags&unix.ST_NODEV == 0

	switch {
	case nosuid && !p.InFstab:
		return warnT(id, "pool.nosuid.nofstab", "pool.mount.hint", params)
	case nosuid:
		return warnT(id, "pool.nosuid", "pool.mount.hint", params)
	case !p.InFstab:
		return warnT(id, "pool.nofstab", "pool.mount.hint", params)
	}

	return okT(id, "pool.mounted", params)
}

// PoolMode checks that a pool of homes is root 0711, so site users cannot
// list each other (ADR-0032).
func PoolMode(dir string) Check {
	const id = "system.pool"

	fi, err := os.Stat(dir)
	if err != nil {
		return failT(id, "path.error", "", P{"path": dir, "error": describe(err)})
	}

	st, isUnix := fi.Sys().(*syscall.Stat_t)
	if fi.Mode().Perm() == 0o711 && isUnix && st.Uid == 0 {
		return okT(id, "pool.mode.ok", P{"path": dir})
	}

	return warnT(id, "pool.mode", "pool.mode.hint", P{"path": dir, "mode": fmt.Sprintf("%04o", fi.Mode().Perm()), "step": "harden"})
}

// Socket checks that a service accepts connections on its Unix socket.
// name is a value of germanValues or a product name; hintKey is a text.
func Socket(ctx context.Context, id, name, path, hintKey string) Check {
	d := net.Dialer{Timeout: 2 * time.Second}

	c, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return failT(id, "socket.down", hintKey, P{"name": name, "path": path, "error": describe(err)})
	}

	_ = c.Close()

	return okT(id, "service.running", P{"name": name})
}

// Panel asks the panel for its health over its Unix socket.
func Panel(ctx context.Context, socket string) Check {
	const id = "service.panel"

	const hint = "panel.hint"

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
		return failT(id, "panel.error", hint, P{"error": err.Error()})
	}

	// The socket trusts only nginx, which always names the client. This
	// request comes from the server itself.
	req.Header.Set("X-Real-IP", "127.0.0.1")

	resp, err := client.Do(req)
	if err != nil {
		return failT(id, "panel.down", hint, P{"path": socket, "error": describe(err)})
	}
	defer resp.Body.Close()

	var h struct {
		Status  string `json:"status"`
		Agent   string `json:"agent"`
		DB      string `json:"db"`
		Version string `json:"version"`
	}

	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<12)).Decode(&h); err != nil || resp.StatusCode != http.StatusOK || h.Status != "ok" {
		return failT(id, "panel.problem", hint, P{"code": strconv.Itoa(resp.StatusCode), "agent": strconv.Quote(h.Agent), "db": strconv.Quote(h.DB)})
	}

	return okT(id, "panel.running", P{"version": h.Version})
}

// Usage checks a site's disk use against its limit. A limit of 0 means
// unlimited, which is a choice and not a problem.
func Usage(site, what string, used, limit uint64, format func(uint64) string) Check {
	const id = "site.disk"

	if limit == 0 {
		return NewSite(agentapi.DoctorOK, id, site, "usage.unlimited", "", P{"what": what, "used": format(used)})
	}

	pct := used * 100 / limit
	params := P{"what": what, "used": format(used), "limit": format(limit), "percent": strconv.FormatUint(pct, 10)}

	switch {
	case pct >= UsageFailPercent:
		return NewSite(agentapi.DoctorFail, id, site, "usage", "usage.fail.hint", params)
	case pct >= UsageWarnPercent:
		return NewSite(agentapi.DoctorWarn, id, site, "usage", "usage.warn.hint", params)
	}

	return NewSite(agentapi.DoctorOK, id, site, "usage", "", params)
}

// DatabaseUsage checks the size of a site's local databases against its
// warning threshold (ADR-0057). Nothing stops a database at the
// threshold, so a full one is a warning to act on, not a failed write.
func DatabaseUsage(site string, used, limit uint64) Check {
	const id = "site.database"

	if limit == 0 {
		return NewSite(agentapi.DoctorOK, id, site, "db.unlimited", "", P{"used": Bytes(used)})
	}

	pct := used * 100 / limit
	params := P{"used": Bytes(used), "limit": Bytes(limit), "percent": strconv.FormatUint(pct, 10)}

	switch {
	case pct >= UsageFailPercent:
		return NewSite(agentapi.DoctorFail, id, site, "db.usage", "db.usage.hint", params)
	case pct >= UsageWarnPercent:
		return NewSite(agentapi.DoctorWarn, id, site, "db.usage", "db.usage.hint", params)
	}

	return NewSite(agentapi.DoctorOK, id, site, "db.usage", "", params)
}

// Cert checks when a site's certificate expires.
func Cert(site string, notAfter, now time.Time) Check {
	const id = "site.tls"

	left := notAfter.Sub(now)
	days := int(left.Hours() / 24)
	params := P{"date": notAfter.UTC().Format("2006-01-02"), "days": strconv.Itoa(days)}

	switch {
	case left <= 0:
		return NewSite(agentapi.DoctorFail, id, site, "cert.expired", "cert.hint", params)
	case days < CertFailDays:
		return NewSite(agentapi.DoctorFail, id, site, "cert.valid", "cert.hint", params)
	case days < CertWarnDays:
		return NewSite(agentapi.DoctorWarn, id, site, "cert.valid", "cert.hint", params)
	}

	return NewSite(agentapi.DoctorOK, id, site, "cert.valid", "", params)
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

// PHPSoonDays is how many days before a version's end of security fixes
// its sites get a warning (ADR-0036).
const PHPSoonDays = 60

// PHPSupport checks the PHP version of a site against the end of its
// security fixes (agentapi.PHPSecurityUntil). Hostashi never switches the
// version by itself; the check only tells.
func PHPSupport(site, version string, now time.Time) Check {
	const id = "site.php"

	until, ok := agentapi.PHPSecurityUntil[version]
	end, err := time.Parse(time.DateOnly, until)

	if !ok || err != nil {
		return NewSite(agentapi.DoctorWarn, id, site, "php.unknown", "php.unknown.hint", P{"version": version})
	}

	left := end.Add(24 * time.Hour).Sub(now)
	days := int(left.Hours() / 24)

	switch {
	case left <= 0:
		return NewSite(agentapi.DoctorWarn, id, site, "php.ended", "php.ended.hint", P{"version": version, "until": until})
	case days <= PHPSoonDays:
		return NewSite(agentapi.DoctorWarn, id, site, "php.soon", "php.soon.hint", P{"version": version, "until": until, "days": strconv.Itoa(days)})
	}

	return NewSite(agentapi.DoctorOK, id, site, "php.supported", "", P{"version": version, "until": until})
}
