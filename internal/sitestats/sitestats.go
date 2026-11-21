// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sitestats reads per-site numbers for the metrics.snapshot
// operation (ADR-0022): whether a site's PHP-FPM master runs and how much
// memory its cgroup uses, and how much disk its home takes. It runs in the
// agent as root and only reads.
package sitestats

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultCgroupRoot is where the unified cgroup hierarchy is mounted.
const DefaultCgroupRoot = "/sys/fs/cgroup"

// unitPrefix must match php.Unit: hostashi-php-<version>@<site>.service.
const unitPrefix = "hostashi-php-"

// escapeUnitPrefix escapes a unit prefix for its slice name the way systemd
// does for the characters a PHP version and the fixed prefix can contain:
// a dash becomes \x2d.
func escapeUnitPrefix(s string) string { return strings.ReplaceAll(s, "-", `\x2d`) }

// PHPCgroup is the cgroup directory of a site's FPM unit. systemd puts the
// instances of a template unit into the slice system-<prefix>.slice.
func PHPCgroup(root, version, user string) string {
	prefix := unitPrefix + version

	return filepath.Join(root, "system.slice", "system-"+escapeUnitPrefix(prefix)+".slice", prefix+"@"+user+".service")
}

// PHPSliceCgroup is the cgroup directory of a site's FPM unit inside the
// site's slice (ADR-0015): hostashi.slice/hostashi-site.slice/<slice>/<unit>.
func PHPSliceCgroup(root, version, user string) string {
	slice := "hostashi-site-" + escapeUnitPrefix(user) + ".slice"

	return filepath.Join(root, "hostashi.slice", "hostashi-site.slice", slice, unitPrefix+version+"@"+user+".service")
}

// PHP is the state of one site's FPM unit.
type PHP struct {
	Up          bool
	MemoryBytes uint64
	MemoryKnown bool
}

// ReadPHP reads the unit's cgroup. A missing cgroup means the unit does not
// run, which is not an error.
// The unit is looked up in the site's slice first, then in systemd's
// default place for a unit that has not moved yet.
func ReadPHP(root, version, user string) (PHP, error) {
	return readUnit(PHPSliceCgroup(root, version, user), PHPCgroup(root, version, user))
}

// CacheCgroup is the cgroup directory of a site's object cache unit
// hostashi-valkey@<site>.service in the site's slice (ADR-0045).
func CacheCgroup(root, user string) string {
	slice := "hostashi-site-" + escapeUnitPrefix(user) + ".slice"

	return filepath.Join(root, "hostashi.slice", "hostashi-site.slice", slice, "hostashi-valkey@"+user+".service")
}

// ReadCache reads the cgroup of a site's object cache like ReadPHP.
func ReadCache(root, user string) (PHP, error) {
	return readUnit(CacheCgroup(root, user))
}

// readUnit reads the first of the cgroup directories that exists.
func readUnit(dirs ...string) (PHP, error) {
	var (
		dir   string
		procs []byte
		err   error
	)

	for _, dir = range dirs {
		procs, err = os.ReadFile(filepath.Join(dir, "cgroup.procs"))
		if !errors.Is(err, fs.ErrNotExist) {
			break
		}
	}

	if errors.Is(err, fs.ErrNotExist) {
		return PHP{}, nil
	}

	if err != nil {
		return PHP{}, err
	}

	out := PHP{Up: strings.TrimSpace(string(procs)) != ""}

	b, err := os.ReadFile(filepath.Join(dir, "memory.current"))
	if errors.Is(err, fs.ErrNotExist) {
		return out, nil // memory accounting off
	}

	if err != nil {
		return out, err
	}

	n, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	if err != nil {
		return out, err
	}

	out.MemoryBytes, out.MemoryKnown = n, true

	return out, nil
}

// DiskUsage adds up the allocated blocks of everything below root without
// following symbolic links or leaving the file system. A file with several
// hard links counts once. Entries that vanish during the walk are skipped.
func DiskUsage(ctx context.Context, root string) (uint64, error) {
	var (
		total uint64
		dev   uint64
		seen  = map[uint64]bool{}
	)

	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && p != root {
				return nil
			}

			return err
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		info, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}

		if err != nil {
			return err
		}

		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return nil
		}

		if p == root {
			dev = uint64(st.Dev) //nolint:unconvert // Dev is uint64 on amd64, not everywhere
		} else if uint64(st.Dev) != dev { //nolint:unconvert // see above
			if d.IsDir() {
				return fs.SkipDir
			}

			return nil
		}

		if st.Nlink > 1 && !d.IsDir() {
			if seen[st.Ino] {
				return nil
			}

			seen[st.Ino] = true
		}

		total += uint64(st.Blocks) * 512 //nolint:gosec // Blocks is never negative

		return nil
	})

	return total, err
}

// Disk is the cached disk usage of one site.
type Disk struct {
	UsedBytes  uint64
	MeasuredAt time.Time // zero: not measured yet
}

// DiskCache measures every site's home now and then and answers from the
// last result, so a scrape never walks a file tree (ADR-0022).
type DiskCache struct {
	mu   sync.Mutex
	last map[string]Disk
}

// Get returns the last measurement of a site.
func (c *DiskCache) Get(user string) Disk {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.last[user]
}

// Refresh measures the given homes (site name to path) one after the other
// and replaces the cache. A site whose walk fails keeps its old value.
func (c *DiskCache) Refresh(ctx context.Context, homes map[string]string, now func() time.Time) error {
	next := make(map[string]Disk, len(homes))

	var errs []error

	for user, home := range homes {
		n, err := DiskUsage(ctx, home)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			errs = append(errs, err)
			next[user] = c.Get(user)

			continue
		}

		next[user] = Disk{UsedBytes: n, MeasuredAt: now()}
	}

	c.mu.Lock()
	c.last = next
	c.mu.Unlock()

	return errors.Join(errs...)
}
