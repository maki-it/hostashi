// SPDX-License-Identifier: AGPL-3.0-or-later

// Package hoststats reads the server's counters for the dashboard
// (ADR-0052): CPU times, memory, load and uptime from /proc, network
// counters from /proc/net/dev and file system sizes through statfs. It only
// reads, keeps nothing and never starts a process.
package hoststats

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// maxFile bounds every /proc file read. /proc/stat grows with the number
// of CPUs; 1 MiB holds thousands [E].
const maxFile = 1 << 20

// Mount is one file system to measure; Name ends up in the answer, Path
// never does.
type Mount struct {
	Name string
	Path string
}

// Reader reads from Root (empty: the real /), so tests can use fixtures.
type Reader struct {
	Root string
	// Statfs defaults to unix.Statfs.
	Statfs func(path string, st *unix.Statfs_t) error
}

func (r Reader) path(p string) string {
	if r.Root == "" {
		return p
	}

	return filepath.Join(r.Root, p)
}

func (r Reader) read(p string) ([]byte, error) {
	f, err := os.Open(r.path(p))
	if err != nil {
		return nil, err
	}
	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil {
		return nil, err
	}

	if len(b) > maxFile {
		return nil, fmt.Errorf("hoststats: %s is larger than %d bytes", p, maxFile)
	}

	return b, nil
}

// Read collects one sample. A mount that cannot be measured is left out;
// the /proc files are required.
func (r Reader) Read(mounts []Mount) (agentapi.HostStats, error) {
	var out agentapi.HostStats

	b, err := r.read("/proc/stat")
	if err != nil {
		return out, err
	}

	if out.CPU, out.CPUs, err = ParseStat(b); err != nil {
		return out, err
	}

	if b, err = r.read("/proc/meminfo"); err != nil {
		return out, err
	}

	if out.Memory, err = ParseMeminfo(b); err != nil {
		return out, err
	}

	if b, err = r.read("/proc/loadavg"); err != nil {
		return out, err
	}

	if out.Load1, out.Load5, out.Load15, err = ParseLoadavg(b); err != nil {
		return out, err
	}

	if b, err = r.read("/proc/uptime"); err != nil {
		return out, err
	}

	if out.UptimeSeconds, err = ParseUptime(b); err != nil {
		return out, err
	}

	if b, err = r.read("/proc/net/dev"); err != nil {
		return out, err
	}

	if out.Net, err = ParseNetDev(b); err != nil {
		return out, err
	}

	out.Disks = r.disks(mounts)

	return out, nil
}

func (r Reader) disks(mounts []Mount) []agentapi.HostDisk {
	statfs := r.Statfs
	if statfs == nil {
		statfs = unix.Statfs
	}

	out := []agentapi.HostDisk{}
	seen := map[uint64]bool{} // one row per file system, by its ID

	for _, m := range mounts {
		if len(out) == agentapi.MaxHostDisks {
			break
		}

		var st unix.Statfs_t
		if err := statfs(m.Path, &st); err != nil {
			continue
		}

		id := uint64(uint32(st.Fsid.Val[0]))<<32 | uint64(uint32(st.Fsid.Val[1])) //nolint:gosec // bit pattern of the ID, not a number
		if id != 0 && seen[id] {
			continue
		}

		seen[id] = true

		bs := uint64(st.Bsize) //nolint:gosec // block size is positive
		out = append(out, agentapi.HostDisk{
			Name:        m.Name,
			TotalBytes:  st.Blocks * bs,
			FreeBytes:   st.Bavail * bs,
			TotalInodes: st.Files,
			FreeInodes:  st.Ffree,
		})
	}

	return out
}

var errFormat = errors.New("hoststats: unexpected format")

// ParseStat reads the "cpu" line and counts the "cpuN" lines of
// /proc/stat. Old kernels with fewer than eight fields leave the rest 0.
func ParseStat(b []byte) (agentapi.CPUTimes, int, error) {
	var (
		t     agentapi.CPUTimes
		found bool
		cpus  int
	)

	for line := range bytes.SplitSeq(b, []byte("\n")) {
		f := strings.Fields(string(line))
		if len(f) == 0 {
			continue
		}

		switch {
		case f[0] == "cpu":
			if len(f) < 5 {
				return t, 0, fmt.Errorf("%w: cpu line", errFormat)
			}

			v := make([]uint64, 8)

			for i := 0; i < 8 && i+1 < len(f); i++ {
				n, err := strconv.ParseUint(f[i+1], 10, 64)
				if err != nil {
					return t, 0, fmt.Errorf("%w: cpu field %d", errFormat, i+1)
				}

				v[i] = n
			}

			t = agentapi.CPUTimes{User: v[0], Nice: v[1], System: v[2], Idle: v[3], IOWait: v[4], IRQ: v[5], SoftIRQ: v[6], Steal: v[7]}
			found = true
		case strings.HasPrefix(f[0], "cpu"):
			if _, err := strconv.Atoi(f[0][3:]); err == nil {
				cpus++
			}
		}
	}

	if !found {
		return t, 0, fmt.Errorf("%w: no cpu line", errFormat)
	}

	return t, max(cpus, 1), nil
}

// ParseMeminfo reads MemTotal, MemAvailable, SwapTotal and SwapFree.
func ParseMeminfo(b []byte) (agentapi.HostMemory, error) {
	var (
		m    agentapi.HostMemory
		seen int
	)

	fields := map[string]*uint64{
		"MemTotal": &m.TotalBytes, "MemAvailable": &m.AvailableBytes,
		"SwapTotal": &m.SwapTotalBytes, "SwapFree": &m.SwapFreeBytes,
	}

	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, rest, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}

		p, want := fields[k]
		if !want {
			continue
		}

		f := strings.Fields(rest)
		if len(f) == 0 || len(f) > 2 || (len(f) == 2 && f[1] != "kB") {
			return m, fmt.Errorf("%w: %s", errFormat, k)
		}

		n, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil || n > 1<<50 {
			return m, fmt.Errorf("%w: %s", errFormat, k)
		}

		*p = n * 1024
		seen++
	}

	if m.TotalBytes == 0 || seen < 2 {
		return m, fmt.Errorf("%w: meminfo without MemTotal and MemAvailable", errFormat)
	}

	return m, nil
}

func parseFloat(s string) (float64, error) {
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || !(v >= 0 && v <= 1e12) { // also refuses NaN
		return 0, errFormat
	}

	return v, nil
}

// ParseLoadavg reads the first three fields of /proc/loadavg.
func ParseLoadavg(b []byte) (l1, l5, l15 float64, err error) {
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return 0, 0, 0, fmt.Errorf("%w: loadavg", errFormat)
	}

	if l1, err = parseFloat(f[0]); err != nil {
		return 0, 0, 0, err
	}

	if l5, err = parseFloat(f[1]); err != nil {
		return 0, 0, 0, err
	}

	if l15, err = parseFloat(f[2]); err != nil {
		return 0, 0, 0, err
	}

	return l1, l5, l15, nil
}

// ParseUptime reads the first field of /proc/uptime.
func ParseUptime(b []byte) (float64, error) {
	f := strings.Fields(string(b))
	if len(f) < 1 {
		return 0, fmt.Errorf("%w: uptime", errFormat)
	}

	return parseFloat(f[0])
}

// ifaceRe is what the panel may see of an interface name (Linux allows 15
// bytes of nearly anything; odd names are left out).
var ifaceRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,15}$`)

// ParseNetDev reads /proc/net/dev: every interface except lo, at most
// agentapi.MaxHostNets, in file order.
func ParseNetDev(b []byte) ([]agentapi.HostNet, error) {
	out := []agentapi.HostNet{}

	for line := range bytes.SplitSeq(b, []byte("\n")) {
		name, rest, ok := strings.Cut(string(line), ":")
		if !ok {
			continue
		}

		name = strings.TrimSpace(name)
		if name == "lo" || !ifaceRe.MatchString(name) {
			continue
		}

		f := strings.Fields(rest)
		if len(f) < 16 {
			return nil, fmt.Errorf("%w: net/dev line for %s", errFormat, name)
		}

		var v [4]uint64

		for i, idx := range []int{0, 8, 1, 9} {
			n, err := strconv.ParseUint(f[idx], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("%w: net/dev field for %s", errFormat, name)
			}

			v[i] = n
		}

		out = append(out, agentapi.HostNet{Name: name, RxBytes: v[0], TxBytes: v[1], RxPackets: v[2], TxPackets: v[3]})

		if len(out) == agentapi.MaxHostNets {
			break
		}
	}

	return out, nil
}
