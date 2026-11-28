// SPDX-License-Identifier: AGPL-3.0-or-later

package scanunit

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// The progress of a scan (ADR-0064, progress addendum). Before the unit starts,
// the agent walks the home and writes the files to scan into a list, one
// absolute path per line; the scanner reads it with --file-list= and
// prints one line per file, without --infected. The progress is the
// number of those lines in the log over the number of lines in the list.
// clamdscan prints nothing for clean files of a folder it walks itself,
// only for files it is named, so the list is what gives both scanners a
// count.

const (
	// DefaultListDir holds <site>.list, the files of a site's running
	// scan (root 0711; files root:<site> 0640: only the site user reads
	// its own file names).
	DefaultListDir = "/var/lib/hostashi-scan"
	// tailWindow is how much of the end of a log a failed run looks at.
	tailWindow = 64 << 10
)

// maxListEntries bounds a list. A home with more files is scanned by
// folder, and its scan shows no total. A variable for the tests.
var maxListEntries int64 = 5_000_000

// errTooMany: the home has more than maxListEntries files.
var errTooMany = errors.New("scanunit: too many files for a list")

// progress is what the agent knows about the running scan of a site. It
// lives in memory only: after a restart of the agent, Status rebuilds it
// from the list and the log.
type progress struct {
	id int64
	// listing: the list is being written; the unit has not started yet.
	listing bool
	// err is why the scan could not start, once listing has ended.
	err string
	// total is the number of lines of the list; 0 while unknown.
	total int64
	// scanned counts the log's lines that report on a file, up to off.
	scanned int64
	off     int64
	atStart bool
	slash   bool
	cancel  context.CancelFunc
}

// ListFile is the list of files of a site's scan.
func (m *Manager) ListFile(site string) string {
	return filepath.Join(or(m.ListDir, DefaultListDir), site+".list")
}

// listable reports whether a path can stand on one line of a list.
func listable(p string) bool { return !strings.ContainsAny(p, "\n\r") }

// walkList calls emit for each file a scan of home must read: the regular
// files below each root (home, or the targets inside it), without
// following symlinks and without the Excluded folders of the home. It
// reads directory entries only: their type comes with the entry, so no
// file is opened or stat'ed. A name that cannot stand on a line of the list
// (a newline in it) must not hide a file from the scan: its folder is
// listed instead, which the scanner then walks itself, and nothing else
// below that folder. A folder that cannot be read is listed too, so the
// scanner reports the error.
func walkList(ctx context.Context, home string, targets []string, emit func(string) error) error {
	roots := []string{home}
	if len(targets) > 0 {
		roots = roots[:0]
		for _, t := range targets {
			roots = append(roots, home+"/"+t)
		}
	}

	excluded := make(map[string]bool, len(Excluded))
	for _, d := range Excluded {
		excluded[home+"/"+d] = true
	}

	covered := ""

	for _, root := range roots {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			if covered != "" && (p == covered || strings.HasPrefix(p, covered+"/")) {
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}

				return nil
			}

			if err != nil {
				if d == nil || !d.IsDir() || !listable(p) {
					return nil
				}

				covered = p

				if err := emit(p); err != nil {
					return err
				}

				return fs.SkipDir
			}

			if d.IsDir() && excluded[p] {
				return fs.SkipDir
			}

			if !listable(d.Name()) {
				parent := filepath.Dir(p)
				if p == root || !listable(parent) {
					return nil
				}

				covered = parent

				if err := emit(parent); err != nil {
					return err
				}

				if d.IsDir() {
					return fs.SkipDir
				}

				return nil
			}

			if d.Type().IsRegular() && listable(p) {
				return emit(p)
			}

			return nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// writeList writes the list of a scan to name, root:gid 0640, and returns
// its number of lines. With more than maxListEntries files it writes the
// roots instead, for the scanner to walk, and returns 0.
func writeList(ctx context.Context, name, home string, targets []string, gid uint32) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(name), 0o711); err != nil {
		return 0, fmt.Errorf("scanunit: %w", err)
	}

	// Explicit: the agent runs with UMask=0077, and the site user must
	// reach the file.
	if err := os.Chmod(filepath.Dir(name), 0o711); err != nil {
		return 0, fmt.Errorf("scanunit: %w", err)
	}

	tmp := name + ".tmp"
	_ = os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("scanunit: %w", err)
	}

	defer func() { _ = os.Remove(tmp) }()

	w := bufio.NewWriterSize(f, 256<<10)

	var n int64

	err = walkList(ctx, home, targets, func(p string) error {
		if n >= maxListEntries {
			return errTooMany
		}

		n++
		_, err := w.WriteString(p + "\n")

		return err
	})

	if errors.Is(err, errTooMany) {
		n, err = 0, rootsOnly(f, w, home, targets)
	}

	err = errors.Join(err, w.Flush(), f.Chown(-1, int(gid)), f.Chmod(0o640), f.Sync(), f.Close())
	if err == nil {
		err = os.Rename(tmp, name)
	}

	if err != nil {
		return 0, fmt.Errorf("scanunit: list: %w", err)
	}

	return n, nil
}

// rootsOnly replaces what f holds with the roots of a scan.
func rootsOnly(f *os.File, w *bufio.Writer, home string, targets []string) error {
	w.Reset(f)

	if err := f.Truncate(0); err != nil {
		return err
	}

	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	if len(targets) == 0 {
		_, err := w.WriteString(home + "\n")

		return err
	}

	for _, t := range targets {
		if _, err := w.WriteString(home + "/" + t + "\n"); err != nil {
			return err
		}
	}

	return nil
}

// countLines counts the lines of a list, 0 if it cannot be read.
func countLines(name string) int64 {
	f, err := os.Open(name)
	if err != nil {
		return 0
	}
	defer f.Close()

	var n int64

	buf := make([]byte, 64<<10)

	for {
		k, err := f.Read(buf)
		for _, c := range buf[:k] {
			if c == '\n' {
				n++
			}
		}

		if err != nil {
			return n
		}
	}
}

// advance reads what the log gained since the last call and counts the
// lines that report on a file: they start with the file's absolute path.
// Warnings ("WARNING: ...", "LibClamAV ...") do not. Only the new part of
// the log is read, so a poll costs what the scan wrote since the last one.
// A log shorter than what was read is a new one.
func (p *progress) advance(log string) {
	f, err := os.Open(log)
	if err != nil {
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		return
	}

	if fi.Size() < p.off {
		p.off, p.scanned, p.atStart = 0, 0, true
	}

	if _, err := f.Seek(p.off, io.SeekStart); err != nil {
		return
	}

	buf := make([]byte, 64<<10)

	for {
		k, err := f.Read(buf)
		for _, c := range buf[:k] {
			if p.atStart {
				p.slash, p.atStart = c == '/', false
			}

			if c == '\n' {
				if p.slash {
					p.scanned++
				}

				p.atStart = true
			}
		}

		p.off += int64(k)

		if err != nil {
			return
		}
	}
}

// report is the progress as the panel gets it.
func (p *progress) report() *agentapi.ScanProgress {
	return &agentapi.ScanProgress{Scanned: p.scanned, Total: p.total}
}
