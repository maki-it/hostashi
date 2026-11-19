// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"errors"
	"io"
	"os"

	"golang.org/x/sys/unix"
)

// Usage is what a site's home occupies on the old server: the base for its
// disk limits on the new one (ADR-0015: measured use plus 50 %).
type Usage struct {
	// Bytes counts allocated blocks (st_blocks × 512), like disk quota.
	Bytes int64 `json:"bytes"`
	// Inodes counts the home and every entry below it, hard links once.
	Inodes int64 `json:"inodes"`
	// Partial is set when the walk stopped early; the numbers are then a
	// lower bound.
	Partial bool `json:"partial,omitempty"`
}

const (
	maxUsageDepth   = 256
	maxUsageEntries = 20_000_000
)

var errUsageLimit = errors.New("usage walk limit reached")

// Usage walks the tree below abs without following symlinks and without
// leaving its file system, as quota on the new server would count it.
func (r *reader) Usage(abs string) (Usage, error) {
	f, err := r.open(abs, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW)
	if err != nil {
		return Usage{}, err
	}

	var st unix.Stat_t
	if err := unix.Fstat(int(f.Fd()), &st); err != nil {
		_ = f.Close()

		return Usage{}, err
	}

	w := usageWalk{dev: st.Dev, seen: map[[2]uint64]bool{}}
	w.add(&st)

	err = w.dir(f, 0)

	u := Usage{Bytes: w.bytes, Inodes: w.inodes}
	if errors.Is(err, errUsageLimit) {
		u.Partial = true
		err = nil
	}

	return u, err
}

type usageWalk struct {
	dev           uint64
	bytes, inodes int64
	seen          map[[2]uint64]bool
}

func (w *usageWalk) add(st *unix.Stat_t) {
	if st.Nlink > 1 && st.Mode&unix.S_IFMT != unix.S_IFDIR {
		k := [2]uint64{st.Dev, st.Ino}
		if w.seen[k] {
			return
		}

		w.seen[k] = true
	}

	w.inodes++
	w.bytes += st.Blocks * 512
}

// dir counts the entries of the open directory f and closes it.
func (w *usageWalk) dir(f *os.File, depth int) error {
	defer f.Close()

	if depth >= maxUsageDepth {
		return errUsageLimit
	}

	fd := int(f.Fd())

	for {
		ents, err := f.ReadDir(1024)
		for _, e := range ents {
			if w.inodes >= maxUsageEntries {
				return errUsageLimit
			}

			var st unix.Stat_t
			if err := unix.Fstatat(fd, e.Name(), &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
				continue // removed while we walk
			}

			if st.Dev != w.dev {
				continue // another file system, not in this quota
			}

			w.add(&st)

			if st.Mode&unix.S_IFMT != unix.S_IFDIR {
				continue
			}

			cfd, err := unix.Openat(fd, e.Name(), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
			if err != nil {
				continue
			}

			if err := w.dir(os.NewFile(uintptr(cfd), e.Name()), depth+1); err != nil {
				return err
			}
		}

		if errors.Is(err, io.EOF) || (err == nil && len(ents) == 0) {
			return nil
		}

		if err != nil {
			return err
		}
	}
}
