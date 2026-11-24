// SPDX-License-Identifier: AGPL-3.0-or-later

package sitestats

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// SliceCgroup is the cgroup directory of a site's slice (ADR-0015).
func SliceCgroup(root, user string) string {
	return filepath.Join(root, "hostashi.slice", "hostashi-site.slice", "hostashi-site-"+escapeUnitPrefix(user)+".slice")
}

// UserSliceCgroup is the cgroup directory systemd-logind puts the SSH and
// SFTP sessions of a uid in (ADR-0032).
func UserSliceCgroup(root string, uid uint32) string {
	return filepath.Join(root, "user.slice", "user-"+strconv.FormatUint(uint64(uid), 10)+".slice")
}

// Usage are the raw counters of one or more cgroups (ADR-0063). The panel
// turns two readings into rates.
type Usage struct {
	CPUUsec uint64 // cpu.stat usage_usec
	// MemoryBytes is the working set: memory.current minus inactive_file.
	MemoryBytes  uint64
	CacheBytes   uint64 // file cache in memory.stat (file)
	MemoryKnown  bool
	Tasks        uint64 // pids.current
	IOReadBytes  uint64 // io.stat rbytes, all devices
	IOWriteBytes uint64 // io.stat wbytes, all devices
	IOKnown      bool
}

// Add sums two readings; a field is known when either side knows it.
func (u Usage) Add(o Usage) Usage {
	return Usage{
		CPUUsec:      u.CPUUsec + o.CPUUsec,
		MemoryBytes:  u.MemoryBytes + o.MemoryBytes,
		CacheBytes:   u.CacheBytes + o.CacheBytes,
		MemoryKnown:  u.MemoryKnown || o.MemoryKnown,
		Tasks:        u.Tasks + o.Tasks,
		IOReadBytes:  u.IOReadBytes + o.IOReadBytes,
		IOWriteBytes: u.IOWriteBytes + o.IOWriteBytes,
		IOKnown:      u.IOKnown || o.IOKnown,
	}
}

// maxStatFile bounds what is read of one cgroup file; the files are a few
// hundred bytes.
const maxStatFile = 64 << 10

// ReadUsage reads the counters of a cgroup directory. A missing directory
// means nothing of the site runs there, which is not an error; a missing
// file means its accounting is off.
func ReadUsage(dir string) (Usage, error) {
	var u Usage

	if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
		return u, nil
	} else if err != nil {
		return u, err
	}

	if kv, ok, err := readKV(filepath.Join(dir, "cpu.stat")); err != nil {
		return u, err
	} else if ok {
		u.CPUUsec = kv["usage_usec"]
	}

	cur, ok, err := readUint(filepath.Join(dir, "memory.current"))
	if err != nil {
		return u, err
	}

	if ok {
		kv, _, err := readKV(filepath.Join(dir, "memory.stat"))
		if err != nil {
			return u, err
		}

		inactive := kv["inactive_file"]
		if inactive > cur {
			inactive = cur
		}

		u.MemoryBytes, u.CacheBytes, u.MemoryKnown = cur-inactive, kv["file"], true
	}

	if n, ok, err := readUint(filepath.Join(dir, "pids.current")); err != nil {
		return u, err
	} else if ok {
		u.Tasks = n
	}

	r, w, ok, err := readIO(filepath.Join(dir, "io.stat"))
	if err != nil {
		return u, err
	}

	u.IOReadBytes, u.IOWriteBytes, u.IOKnown = r, w, ok

	return u, nil
}

// readFile reads a small cgroup file; ok is false when it does not exist.
func readFile(path string) ([]byte, bool, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}

	if err != nil {
		return nil, false, err
	}

	defer f.Close()

	b, err := io.ReadAll(io.LimitReader(f, maxStatFile+1))
	if err != nil {
		return nil, false, err
	}

	if len(b) > maxStatFile {
		return nil, false, fmt.Errorf("sitestats: %s is too large", path)
	}

	return b, true, nil
}

func readUint(path string) (uint64, bool, error) {
	b, ok, err := readFile(path)
	if err != nil || !ok {
		return 0, ok, err
	}

	s := strings.TrimSpace(string(b))
	if s == "max" {
		return 0, false, nil
	}

	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("sitestats: %s: %w", path, err)
	}

	return n, true, nil
}

// readKV parses "key value" lines; lines that do not parse are skipped.
func readKV(path string) (map[string]uint64, bool, error) {
	b, ok, err := readFile(path)
	if err != nil || !ok {
		return nil, ok, err
	}

	out := map[string]uint64{}

	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, found := strings.Cut(sc.Text(), " ")
		if !found {
			continue
		}

		if n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64); err == nil {
			out[k] = n
		}
	}

	return out, true, nil
}

// readIO sums rbytes and wbytes over the device lines of io.stat:
// "8:0 rbytes=1 wbytes=2 rios=3 wios=4 dbytes=0 dios=0".
func readIO(path string) (r, w uint64, ok bool, err error) {
	b, ok, err := readFile(path)
	if err != nil || !ok {
		return 0, 0, ok, err
	}

	for line := range strings.Lines(string(b)) {
		for f := range strings.FieldsSeq(line) {
			k, v, found := strings.Cut(f, "=")
			if !found {
				continue
			}

			n, err := strconv.ParseUint(v, 10, 64)
			if err != nil {
				continue
			}

			switch k {
			case "rbytes":
				r += n
			case "wbytes":
				w += n
			}
		}
	}

	return r, w, true, nil
}
