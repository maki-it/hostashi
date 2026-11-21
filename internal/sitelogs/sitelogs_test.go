// SPDX-License-Identifier: AGPL-3.0-or-later

package sitelogs

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

func newReader(t *testing.T) *Reader {
	t.Helper()

	dir := t.TempDir()
	r := &Reader{SiteLogDir: filepath.Join(dir, "nginx"), PHPLogDir: filepath.Join(dir, "php"), Owner: uint32(os.Getuid())}

	for _, d := range []string{filepath.Join(r.SiteLogDir, "blog"), filepath.Join(r.PHPLogDir, "blog")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}

	return r
}

func write(t *testing.T, r *Reader, kind agentapi.LogKind, content string) string {
	t.Helper()

	p, err := r.Path("blog", kind)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}

	return p
}

func appendTo(t *testing.T, p, s string) {
	t.Helper()

	f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, r *Reader, req agentapi.SiteLogsReadRequest) agentapi.SiteLogsReadResponse {
	t.Helper()

	resp, err := r.Read(req)
	if err != nil {
		t.Fatal(err)
	}

	return resp
}

func TestPaths(t *testing.T) {
	r := &Reader{SiteLogDir: "/var/log/nginx/sites", PHPLogDir: "/var/log/hostashi/php"}

	for kind, want := range map[agentapi.LogKind]string{
		agentapi.LogAccess: "/var/log/nginx/sites/blog/access.log",
		agentapi.LogError:  "/var/log/nginx/sites/blog/error.log",
		agentapi.LogPHP:    "/var/log/hostashi/php/blog/error.log",
	} {
		if p, err := r.Path("blog", kind); err != nil || p != want {
			t.Errorf("%s: %q %v", kind, p, err)
		}
	}

	for _, bad := range []string{"../etc", "blog/x", "", "Blog", "a b"} {
		if _, err := r.Path(bad, agentapi.LogAccess); err == nil {
			t.Errorf("site %q accepted", bad)
		}
	}

	if _, err := r.Path("blog", "passwd"); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestValidate(t *testing.T) {
	for _, req := range []agentapi.SiteLogsReadRequest{
		{Site: "blog", Kind: "access"},
		{Site: "blog", Kind: "access", Lines: 1001},
		{Site: "blog", Kind: "access", Lines: -1},
		{Site: "blog", Kind: "access", Lines: 10, After: &agentapi.LogCursor{}},
		{Site: "blog", Kind: "access", After: &agentapi.LogCursor{Offset: -1}},
		{Site: "blog", Kind: "../x", Lines: 10},
		{Site: "../blog", Kind: "access", Lines: 10},
	} {
		if err := req.Validate(); err == nil {
			t.Errorf("accepted %+v", req)
		}
	}
}

func TestTailAndFollow(t *testing.T) {
	r := newReader(t)

	// Missing file: empty, not an error.
	resp := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogAccess, Lines: 10})
	if len(resp.Lines) != 0 || resp.Cursor.Offset != 0 {
		t.Fatalf("missing file: %+v", resp)
	}

	var b strings.Builder
	for i := 1; i <= 50; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}

	p := write(t, r, agentapi.LogAccess, b.String()+"unfinished")

	resp = read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogAccess, Lines: 3})
	if strings.Join(resp.Lines, "|") != "line 48|line 49|line 50" {
		t.Fatalf("tail: %q", resp.Lines)
	}

	// The unfinished line is not shown and comes with the next read.
	if resp.Cursor.Offset != int64(b.Len()) {
		t.Fatalf("cursor %d, want %d", resp.Cursor.Offset, b.Len())
	}

	appendTo(t, p, " now\nline 52\n")

	resp = read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogAccess, After: &resp.Cursor})
	if strings.Join(resp.Lines, "|") != "unfinished now|line 52" || resp.Rotated || resp.More {
		t.Fatalf("follow: %+v", resp)
	}

	// Nothing new: nothing, same cursor.
	again := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogAccess, After: &resp.Cursor})
	if len(again.Lines) != 0 || again.Cursor != resp.Cursor {
		t.Fatalf("no news: %+v", again)
	}

	// More lines than exist: all of them.
	all := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogAccess, Lines: 1000})
	if len(all.Lines) != 52 || all.Lines[0] != "line 1" {
		t.Fatalf("all: %d %q", len(all.Lines), all.Lines[:2])
	}

	// Rotation: a new file under the same name starts from its beginning.
	if err := os.Rename(p, p+".1"); err != nil {
		t.Fatal(err)
	}

	write(t, r, agentapi.LogAccess, "fresh 1\n")

	rot := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogAccess, After: &resp.Cursor})
	if !rot.Rotated || strings.Join(rot.Lines, "|") != "fresh 1" {
		t.Fatalf("rotated: %+v", rot)
	}

	// Truncation in place (copytruncate) counts as rotation too.
	big := agentapi.LogCursor{Inode: rot.Cursor.Inode, Offset: 1 << 20}

	tr := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogAccess, After: &big})
	if !tr.Rotated || strings.Join(tr.Lines, "|") != "fresh 1" {
		t.Fatalf("truncated: %+v", tr)
	}
}

func TestLargeLogsStayInOneFrame(t *testing.T) {
	r := newReader(t)

	// Lines full of characters that JSON escapes to six bytes.
	line := strings.Repeat("<&>", 600)

	var b strings.Builder
	for range 400 {
		b.WriteString(line + "\n")
	}

	// And one line far longer than a whole answer, without a break.
	b.WriteString(strings.Repeat("x", 100<<10) + "\nend\n")
	p := write(t, r, agentapi.LogError, b.String())

	check := func(resp agentapi.SiteLogsReadResponse) {
		t.Helper()

		out, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}

		if len(out) > agentapi.MaxLogAnswer+1024 || len(out) > agentrpc.MaxFrame-1024 {
			t.Fatalf("answer of %d bytes", len(out))
		}

		for _, l := range resp.Lines {
			if b, _ := json.Marshal(l); len(b) > agentapi.MaxLogLine {
				t.Fatalf("line of %d escaped bytes", len(b))
			}
		}
	}

	resp := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogError, Lines: 1000})
	check(resp)

	if len(resp.Lines) == 0 || resp.Lines[len(resp.Lines)-1] != "end" {
		t.Fatalf("tail ends with %q", resp.Lines[len(resp.Lines)-1])
	}

	// Following from the start walks the whole file in frames that fit.
	cur := agentapi.LogCursor{Inode: resp.Cursor.Inode}
	total := 0

	for range 1000 {
		resp = read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogError, After: &cur})
		check(resp)

		total += len(resp.Lines)
		cur = resp.Cursor

		if !resp.More && cur.Offset == int64(b.Len()) {
			break
		}
	}

	if cur.Offset != int64(b.Len()) {
		t.Fatalf("follow stopped at %d of %d", cur.Offset, b.Len())
	}

	if total < 402 {
		t.Fatalf("%d lines read, want at least 402", total)
	}

	if strings.Contains(p, "..") {
		t.Fatal(p)
	}
}

func TestCleanLines(t *testing.T) {
	r := newReader(t)
	write(t, r, agentapi.LogPHP, "a\x1b[31mred\x00\tok\r\n\xff\xfebad utf8\n sep\n")

	resp := read(t, r, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogPHP, Lines: 10})

	want := []string{"a�[31mred�\tok", "��bad utf8", " sep"}
	if strings.Join(resp.Lines, "|") != strings.Join(want, "|") {
		t.Fatalf("%q", resp.Lines)
	}
}

func TestUnsafeFiles(t *testing.T) {
	r := newReader(t)

	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("root:x:0:0\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A symlink as the log file.
	p, _ := r.Path("blog", agentapi.LogAccess)
	if err := os.Symlink(secret, p); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Read(agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogAccess, Lines: 5}); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlinked file: %v", err)
	}

	// A symlink as the site's directory.
	if err := os.Symlink(filepath.Dir(secret), filepath.Join(r.SiteLogDir, "shop")); err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(secret, filepath.Join(filepath.Dir(secret), "access.log")); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Read(agentapi.SiteLogsReadRequest{Site: "shop", Kind: agentapi.LogAccess, Lines: 5}); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("symlinked directory: %v", err)
	}

	// A FIFO must not block.
	ep, _ := r.Path("blog", agentapi.LogError)
	if err := syscall.Mkfifo(ep, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Read(agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogError, Lines: 5}); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("fifo: %v", err)
	}

	// A directory in place of the file.
	pp, _ := r.Path("blog", agentapi.LogPHP)
	if err := os.Mkdir(pp, 0o700); err != nil {
		t.Fatal(err)
	}

	if _, err := r.Read(agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogPHP, Lines: 5}); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("directory: %v", err)
	}

	// A file of another owner.
	r2 := newReader(t)
	write(t, r2, agentapi.LogAccess, "x\n")

	r2.Owner++
	if _, err := r2.Read(agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogAccess, Lines: 5}); !errors.Is(err, ErrUnsafe) {
		t.Fatalf("foreign owner: %v", err)
	}
}
