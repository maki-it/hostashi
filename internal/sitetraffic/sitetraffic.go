// SPDX-License-Identifier: AGPL-3.0-or-later

// Package sitetraffic counts the web traffic of each site from the small
// log nginx writes next to its access log (ADR-0063):
// <SiteLogDir>/<site>/traffic.log in the format LogFormat. It runs in the
// agent, reads only what was appended since its last read and returns sums,
// never lines.
package sitetraffic

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// FileName is the log of a site below its log directory; logrotate keeps
// the previous day as FileName + ".1" (delaycompress).
const FileName = "traffic.log"

// LogFormat is the nginx log_format of the file: time, status, bytes the
// client sent (request line, headers and body), bytes nginx sent. It holds
// no address, path or user agent.
const LogFormat = "$msec $status $request_length $bytes_sent"

// MaxRead bounds what one Read takes from one file; the rest is read the
// next time [E: about a million requests].
const MaxRead = 32 << 20

// Totals are the sums since the agent started.
type Totals struct {
	Requests uint64
	BytesIn  uint64
	BytesOut uint64
}

// Opener opens a log file safely (sitelogs.Reader.OpenFile).
type Opener func(p string) (*os.File, *unix.Stat_t, error)

// Counter keeps one read position per site.
type Counter struct {
	Dir  string // the sites' log directories, as sitelogs.Reader.SiteLogDir
	Open Opener

	mu    sync.Mutex
	sites map[string]*state
}

type state struct {
	ino   uint64
	off   int64
	tot   Totals
	known bool // a file was seen once; before that the counter starts at its end
}

// Read adds what was appended to the site's log since the last call and
// returns the totals. The first call starts at the end of the file, so the
// totals count from the agent's start. A rotated file is finished from its
// ".1" copy; a truncated one is read from the start.
func (c *Counter) Read(site string) (Totals, error) {
	if err := agentapi.ValidateSiteUser(site); err != nil {
		return Totals{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.sites == nil {
		c.sites = map[string]*state{}
	}

	s := c.sites[site]
	if s == nil {
		s = &state{}
		c.sites[site] = s
	}

	p := filepath.Join(c.Dir, site, FileName)

	f, st, err := c.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return s.tot, nil
	}

	if err != nil {
		return s.tot, err
	}

	defer f.Close()

	if !s.known {
		s.ino, s.off, s.known = st.Ino, st.Size, true

		return s.tot, nil
	}

	if st.Ino != s.ino {
		c.finishRotated(p+".1", s)
		s.ino, s.off = st.Ino, 0
	} else if st.Size < s.off {
		s.off = 0
	}

	n, err := add(f, s.off, st.Size, &s.tot)
	s.off += n

	return s.tot, err
}

// finishRotated reads the rest of the file that was rotated away, when the
// ".1" copy is still the same file. Errors only lose that rest.
func (c *Counter) finishRotated(p string, s *state) {
	f, st, err := c.Open(p)
	if err != nil {
		return
	}
	defer f.Close()

	if st.Ino != s.ino || st.Size < s.off {
		return
	}

	_, _ = add(f, s.off, st.Size, &s.tot)
}

// Forget drops the state of sites that are gone.
func (c *Counter) Forget(keep map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for site := range c.sites {
		if !keep[site] {
			delete(c.sites, site)
		}
	}
}

// add parses the whole lines between off and size, at most MaxRead bytes,
// and returns how many bytes it consumed. An unfinished last line stays for
// the next read.
func add(f io.ReaderAt, off, size int64, tot *Totals) (int64, error) {
	if size <= off {
		return 0, nil
	}

	b := make([]byte, min(size-off, MaxRead))

	n, err := f.ReadAt(b, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}

	b = b[:n]

	last := bytes.LastIndexByte(b, '\n')
	if last < 0 {
		if len(b) == MaxRead {
			return int64(len(b)), nil // one line longer than MaxRead: skip it
		}

		return 0, nil
	}

	for line := range bytes.Lines(b[:last+1]) {
		addLine(line, tot)
	}

	return int64(last + 1), nil
}

// addLine adds one "msec status request_length bytes_sent" line; a line
// that does not parse is skipped.
func addLine(line []byte, tot *Totals) {
	fields := bytes.Fields(line)
	if len(fields) != 4 {
		return
	}

	in, err := strconv.ParseUint(string(fields[2]), 10, 64)
	if err != nil {
		return
	}

	out, err := strconv.ParseUint(string(fields[3]), 10, 64)
	if err != nil {
		return
	}

	tot.Requests++
	tot.BytesIn += in
	tot.BytesOut += out
}
