// SPDX-License-Identifier: AGPL-3.0-or-later

package sitetraffic

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/sitelogs"
)

func newCounter(t *testing.T) (*Counter, string) {
	t.Helper()

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "shop"), 0o750); err != nil {
		t.Fatal(err)
	}

	r := &sitelogs.Reader{SiteLogDir: dir, Owner: uint32(os.Getuid())}

	return &Counter{Dir: dir, Open: r.OpenFile}, filepath.Join(dir, "shop", FileName)
}

func appendTo(t *testing.T, p, s string) {
	t.Helper()

	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, c *Counter) Totals {
	t.Helper()

	tot, err := c.Read("shop")
	if err != nil {
		t.Fatal(err)
	}

	return tot
}

func TestCounter(t *testing.T) {
	c, p := newCounter(t)

	// No file yet.
	if got := read(t, c); got != (Totals{}) {
		t.Fatalf("no file %+v", got)
	}

	// The first sight starts at the end: old lines do not count.
	appendTo(t, p, "1732451234.123 200 100 1000\n")

	if got := read(t, c); got != (Totals{}) {
		t.Fatalf("first %+v", got)
	}

	appendTo(t, p, "1732451235.000 200 10 20\n1732451235.100 404 1 2\ngarbage line\n1732451235.2 200 x 5\n1732451236.0 200 3")

	if got := read(t, c); got != (Totals{Requests: 2, BytesIn: 11, BytesOut: 22}) {
		t.Fatalf("appended %+v", got)
	}

	// The unfinished line is counted once it ends.
	appendTo(t, p, " 4\n")

	if got := read(t, c); got != (Totals{Requests: 3, BytesIn: 14, BytesOut: 26}) {
		t.Fatalf("finished line %+v", got)
	}
}

func TestCounterRotation(t *testing.T) {
	c, p := newCounter(t)

	appendTo(t, p, "")
	read(t, c)
	appendTo(t, p, "1 200 1 1\n")
	read(t, c)

	// More lines, then logrotate moves the file before the next read.
	appendTo(t, p, "2 200 10 10\n")

	if err := os.Rename(p, p+".1"); err != nil {
		t.Fatal(err)
	}

	appendTo(t, p, "3 200 100 100\n")

	if got := read(t, c); got != (Totals{Requests: 3, BytesIn: 111, BytesOut: 111}) {
		t.Fatalf("rotated %+v", got)
	}

	// Truncated in place (copytruncate): start again at 0.
	if err := os.Truncate(p, 0); err != nil {
		t.Fatal(err)
	}

	appendTo(t, p, "4 200 5 5\n")

	if got := read(t, c); got != (Totals{Requests: 4, BytesIn: 116, BytesOut: 116}) {
		t.Fatalf("truncated %+v", got)
	}
}

func TestCounterRefusesSymlink(t *testing.T) {
	c, p := newCounter(t)

	other := filepath.Join(t.TempDir(), "secret")
	appendTo(t, other, "1 200 1 1\n")

	if err := os.Symlink(other, p); err != nil {
		t.Fatal(err)
	}

	if _, err := c.Read("shop"); err == nil {
		t.Fatal("want an error for a symlink")
	}
}

func TestCounterBadSite(t *testing.T) {
	c, _ := newCounter(t)

	for _, site := range []string{"", "../etc", "a/b"} {
		if _, err := c.Read(site); err == nil {
			t.Fatalf("%q: want error", site)
		}
	}
}

func TestCounterForget(t *testing.T) {
	c, p := newCounter(t)

	appendTo(t, p, "")
	read(t, c)
	appendTo(t, p, "1 200 1 1\n")
	read(t, c)

	c.Forget(map[string]bool{})

	if got := read(t, c); got != (Totals{}) {
		t.Fatalf("after forget %+v", got)
	}
}

func TestAddLongLine(t *testing.T) {
	c, p := newCounter(t)

	appendTo(t, p, "")
	read(t, c)
	appendTo(t, p, strings.Repeat("x", 100)+"\n1 200 2 3\n")

	if got := read(t, c); got != (Totals{Requests: 1, BytesIn: 2, BytesOut: 3}) {
		t.Fatalf("%+v", got)
	}
}
