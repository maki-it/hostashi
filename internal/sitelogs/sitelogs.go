// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sitelogs reads the logs of a site for the panel (ADR-0053). The
// path comes from the log kind and the site name, never from a request.
// Files are opened without following any symbolic link and must be regular
// files of the expected owner, so a site cannot point the root agent at
// another file.
package sitelogs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// WordPressLog is the file name of a site's WordPress install log below
// AppLogDir, wpunit.LogName.
const WordPressLog = "wordpress.log"

// ErrUnsafe is returned for a log that is not a regular file of the owner.
var ErrUnsafe = errors.New("sitelogs: log file is not safe to read")

// Reader reads the logs below two directories.
type Reader struct {
	// SiteLogDir holds <site>/access.log and <site>/error.log of nginx.
	SiteLogDir string
	// PHPLogDir holds <site>/error.log of PHP-FPM.
	PHPLogDir string
	// AppLogDir holds <site>/app.log of the site's app (ADR-0054) and
	// <site>/wordpress.log of its last WordPress quick install (ADR-0069,
	// package wpunit).
	AppLogDir string
	// Owner is the uid every log file must have: 0 in production.
	Owner uint32
}

// Path returns the file of a kind of log of a site.
func (r *Reader) Path(site string, kind agentapi.LogKind) (string, error) {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return "", err
	}

	switch kind {
	case agentapi.LogAccess:
		return filepath.Join(r.SiteLogDir, site, "access.log"), nil
	case agentapi.LogError:
		return filepath.Join(r.SiteLogDir, site, "error.log"), nil
	case agentapi.LogPHP:
		return filepath.Join(r.PHPLogDir, site, "error.log"), nil
	case agentapi.LogApp:
		if r.AppLogDir == "" {
			return "", fmt.Errorf("unknown log %q", kind)
		}

		return filepath.Join(r.AppLogDir, site, "app.log"), nil
	case agentapi.LogWordPress:
		if r.AppLogDir == "" {
			return "", fmt.Errorf("unknown log %q", kind)
		}

		return filepath.Join(r.AppLogDir, site, WordPressLog), nil
	default:
		return "", fmt.Errorf("unknown log %q", kind)
	}
}

// Read answers one site.logs.read request. A missing file is an empty
// answer: a new site has no log yet.
func (r *Reader) Read(req agentapi.SiteLogsReadRequest) (agentapi.SiteLogsReadResponse, error) {
	resp := agentapi.SiteLogsReadResponse{Lines: []string{}}

	if err := req.Validate(); err != nil {
		return resp, err
	}

	p, err := r.Path(req.Site, req.Kind)
	if err != nil {
		return resp, err
	}

	f, st, err := r.open(p)
	if errors.Is(err, os.ErrNotExist) {
		if req.After != nil {
			resp.Rotated = req.After.Offset > 0
		}

		return resp, nil
	}

	if err != nil {
		return resp, err
	}

	defer f.Close()

	ino := st.Ino
	size := st.Size

	if req.After == nil {
		lines, end, err := tail(f, size, req.Lines)
		if err != nil {
			return resp, err
		}

		resp.Lines = lines
		resp.Cursor = agentapi.LogCursor{Inode: ino, Offset: end}

		return resp, nil
	}

	start := req.After.Offset
	if req.After.Inode != ino || start > size {
		resp.Rotated = true
		start = 0
	}

	lines, next, more, err := forward(f, start, size)
	if err != nil {
		return resp, err
	}

	resp.Lines = lines
	resp.Cursor = agentapi.LogCursor{Inode: ino, Offset: next}
	resp.More = more

	return resp, nil
}

// OpenFile opens p like the logs: read-only, no symbolic links anywhere in
// the path, a regular file of the owner. The traffic counter of ADR-0063
// reads its log through it.
func (r *Reader) OpenFile(p string) (*os.File, *unix.Stat_t, error) { return r.open(p) }

// open opens p read-only without following symbolic links anywhere in the
// path and checks that it is a regular file of the owner.
func (r *Reader) open(p string) (*os.File, *unix.Stat_t, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, p, &unix.OpenHow{
		// O_NONBLOCK: a FIFO in place of the log must not hang the agent.
		Flags:   unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOCTTY | unix.O_NONBLOCK,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if errors.Is(err, unix.ENOENT) {
		return nil, nil, os.ErrNotExist
	}

	if err != nil {
		return nil, nil, fmt.Errorf("%w: open: %w", ErrUnsafe, err)
	}

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)

		return nil, nil, fmt.Errorf("sitelogs: stat: %w", err)
	}

	if st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != r.Owner {
		_ = unix.Close(fd)

		return nil, nil, ErrUnsafe
	}

	return os.NewFile(uintptr(fd), p), &st, nil
}

// chunk is how much tail reads backwards at a time.
const chunk = 16 << 10

// tail returns up to n last whole lines before size, within the answer
// budget, and the offset after the last of them. An unfinished last line
// is left for the next read with a cursor.
func tail(f io.ReaderAt, size int64, n int) ([]string, int64, error) {
	var (
		buf   []byte
		pos   = size
		limit = int64(agentapi.MaxLogAnswer) * 2 // raw bytes worth looking at
	)

	for pos > 0 && int64(len(buf)) < limit && bytes.Count(buf, []byte{'\n'}) <= n {
		step := min(int64(chunk), pos)
		pos -= step

		b := make([]byte, step)
		if _, err := f.ReadAt(b, pos); err != nil && !errors.Is(err, io.EOF) {
			return nil, size, fmt.Errorf("sitelogs: read: %w", err)
		}

		buf = append(b, buf...) //nolint:makezero // b is the earlier part of the file
	}

	end := size

	if i := bytes.LastIndexByte(buf, '\n'); i < len(buf)-1 {
		end -= int64(len(buf) - i - 1)
		buf = buf[:i+1]
	}

	// The first line is cut unless the read started at the beginning.
	if pos > 0 && len(buf) > 0 {
		if i := bytes.IndexByte(buf, '\n'); i >= 0 {
			buf = buf[i+1:]
		} else {
			buf = nil
		}
	}

	raw := splitLines(buf)
	if len(raw) > n {
		raw = raw[len(raw)-n:]
	}

	// Keep the newest lines that fit into the budget.
	out := make([]string, 0, len(raw))
	used := 0

	for i := len(raw) - 1; i >= 0; i-- {
		l, cost := clean(raw[i])
		if used+cost > agentapi.MaxLogAnswer {
			break
		}

		out = append(out, l)
		used += cost
	}

	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}

	return out, end, nil
}

// forward returns the whole lines from start on, the offset after the last
// returned line and whether more lines wait. A line longer than what one
// answer reads is cut and skipped to its end.
func forward(f io.ReaderAt, start, size int64) ([]string, int64, bool, error) {
	out := []string{}
	used := 0
	pos := start

	for pos < size {
		want := min(int64(agentapi.MaxLogAnswer), size-pos)
		b := make([]byte, want)

		got, err := f.ReadAt(b, pos)
		if err != nil && !errors.Is(err, io.EOF) {
			return nil, start, false, fmt.Errorf("sitelogs: read: %w", err)
		}

		b = b[:got]

		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			if int64(got) < int64(agentapi.MaxLogAnswer) {
				// An unfinished last line: it comes with the next read.
				return out, pos, false, nil
			}

			// One huge line without a break: show its start, skip ahead.
			l, cost := clean(b)
			if used+cost > agentapi.MaxLogAnswer {
				return out, pos, true, nil
			}

			out = append(out, l)
			used += cost
			pos += int64(got)

			continue
		}

		for i >= 0 {
			l, cost := clean(b[:i])
			if used+cost > agentapi.MaxLogAnswer {
				return out, pos, true, nil
			}

			out = append(out, l)
			used += cost
			pos += int64(i) + 1
			b = b[i+1:]
			i = bytes.IndexByte(b, '\n')
		}
	}

	return out, pos, false, nil
}

func splitLines(b []byte) [][]byte {
	if len(b) == 0 {
		return nil
	}

	b = bytes.TrimSuffix(b, []byte{'\n'})

	return bytes.Split(b, []byte{'\n'})
}

// clean makes a line safe to show: valid UTF-8, no control characters but
// tab, at most MaxLogLine bytes after JSON escaping. It returns the line
// and its escaped size.
func clean(b []byte) (string, int) {
	b = bytes.TrimSuffix(b, []byte{'\r'})

	var (
		out  []byte
		cost int
	)

	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == utf8.RuneError && n <= 1 {
			r = utf8.RuneError
		} else if r != '\t' && (r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0)) {
			r = utf8.RuneError
		}

		c := escapedLen(r)
		if cost+c > agentapi.MaxLogLine-5 { // quotes and "…"
			out = append(out, "…"...)
			cost += 3

			break
		}

		out = utf8.AppendRune(out, r)
		cost += c
		b = b[n:]
	}

	return string(out), cost + 3 // quotes and comma
}

// escapedLen is the size of r in encoding/json's output with HTML escaping.
func escapedLen(r rune) int {
	switch r {
	case '<', '>', '&', ' ', ' ':
		return 6
	case '"', '\\', '\t':
		return 2
	default:
		return utf8.RuneLen(r)
	}
}
