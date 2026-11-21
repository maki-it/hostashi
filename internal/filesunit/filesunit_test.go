// SPDX-License-Identifier: AGPL-3.0-or-later

package filesunit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeCtl struct {
	calls []string
	fail  string
}

func (f *fakeCtl) do(call string) error {
	f.calls = append(f.calls, call)
	if call == f.fail {
		return errors.New("failed")
	}

	return nil
}

func (f *fakeCtl) DaemonReload(context.Context) error        { return f.do("daemon-reload") }
func (f *fakeCtl) Start(_ context.Context, u string) error   { return f.do("start " + u) }
func (f *fakeCtl) Restart(_ context.Context, u string) error { return f.do("try-restart " + u) }
func (f *fakeCtl) Stop(_ context.Context, u string) error    { return f.do("stop " + u) }

var blog = Site{Name: "blog", Home: "/home/blog", Slice: `hostashi-site-blog.slice`}

func TestOpen(t *testing.T) {
	ctl := &fakeCtl{}
	m := &Manager{UnitDir: t.TempDir(), Ctl: ctl}
	ctx := context.Background()

	sock, err := m.Open(ctx, blog)
	if err != nil {
		t.Fatal(err)
	}

	if sock != "/run/hostashi-files/blog.sock" {
		t.Errorf("socket %q", sock)
	}

	want := []string{"daemon-reload", "try-restart hostashi-files@blog.service", "start hostashi-files@blog.socket"}
	if !reflect.DeepEqual(ctl.calls, want) {
		t.Fatalf("calls %v, want %v", ctl.calls, want)
	}

	b, err := os.ReadFile(filepath.Join(m.UnitDir, "hostashi-files@blog.service.d", DropInName))
	if err != nil {
		t.Fatal(err)
	}

	for _, l := range []string{"[Unit]\nRequiresMountsFor=/home/blog", "Slice=hostashi-site-blog.slice", "BindPaths=/home/blog", "ReadWritePaths=/home/blog", "Environment=HOSTASHI_FILES_HOME=/home/blog"} {
		if !strings.Contains(string(b), l+"\n") {
			t.Errorf("drop-in lacks %q:\n%s", l, b)
		}
	}

	// Unchanged: only the socket start.
	ctl.calls = nil

	if _, err := m.Open(ctx, blog); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(ctl.calls, []string{"start hostashi-files@blog.socket"}) {
		t.Fatalf("second open: %v", ctl.calls)
	}

	// A moved home rewrites the drop-in and restarts a running helper.
	ctl.calls = nil
	moved := blog
	moved.Home = "/srv/pool/blog"

	if _, err := m.Open(ctx, moved); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(ctl.calls, want) {
		t.Fatalf("moved: %v", ctl.calls)
	}
}

func TestOpenRefusesOddSites(t *testing.T) {
	m := &Manager{UnitDir: t.TempDir(), Ctl: &fakeCtl{}}

	for _, s := range []Site{
		{Name: "Blog", Home: "/home/blog", Slice: blog.Slice},
		{Name: "blog", Home: "/home/blog\nExecStart=/bin/sh", Slice: blog.Slice},
		{Name: "blog", Home: "/home/../etc", Slice: blog.Slice},
		{Name: "blog", Home: "/", Slice: blog.Slice},
		{Name: "blog", Home: "home/blog", Slice: blog.Slice},
		{Name: "blog", Home: "/home/my blog", Slice: blog.Slice},
		{Name: "blog", Home: "/home/blog", Slice: "system.slice"},
		{Name: "blog", Home: "/home/blog", Slice: "hostashi-site-blog.slice\nUser=root"},
		{Name: "blog", Home: "/home/blog", Slice: blog.Slice, Hidden: []string{"/srv/vol 1"}},
		{Name: "blog", Home: "/home/blog", Slice: blog.Slice, Hidden: []string{"/srv/../etc"}},
		{Name: "blog", Home: "/home/blog", Slice: blog.Slice, Hidden: []string{"/"}},
	} {
		if _, err := m.Open(context.Background(), s); !errors.Is(err, ErrInvalid) {
			t.Errorf("Open(%+v) = %v, want ErrInvalid", s, err)
		}
	}
}

func TestOpenFailure(t *testing.T) {
	ctl := &fakeCtl{fail: "start hostashi-files@blog.socket"}
	m := &Manager{UnitDir: t.TempDir(), Ctl: ctl}

	if _, err := m.Open(context.Background(), blog); err == nil {
		t.Fatal("socket start failure not reported")
	}
}

func TestRemove(t *testing.T) {
	ctl := &fakeCtl{}
	m := &Manager{UnitDir: t.TempDir(), Ctl: ctl}
	ctx := context.Background()

	// Never opened: nothing to do.
	if err := m.Remove(ctx, "blog"); err != nil || len(ctl.calls) != 0 {
		t.Fatalf("remove unopened: %v %v", err, ctl.calls)
	}

	if _, err := m.Open(ctx, blog); err != nil {
		t.Fatal(err)
	}

	ctl.calls = nil

	if err := m.Remove(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	want := []string{"stop hostashi-files@blog.socket", "stop hostashi-files@blog.service", "daemon-reload"}
	if !reflect.DeepEqual(ctl.calls, want) {
		t.Fatalf("calls %v, want %v", ctl.calls, want)
	}

	if _, err := os.Stat(filepath.Join(m.UnitDir, "hostashi-files@blog.service.d")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("drop-in directory left")
	}

	if err := m.Remove(ctx, "../x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("odd name: %v", err)
	}
}

func TestOpenHidesOtherPools(t *testing.T) {
	m := &Manager{UnitDir: t.TempDir(), Ctl: &fakeCtl{}}
	s := blog
	s.Hidden = []string{"/srv/vol1"}

	if _, err := m.Open(context.Background(), s); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(filepath.Join(m.UnitDir, "hostashi-files@blog.service.d", DropInName))
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasSuffix(string(b), "\nTemporaryFileSystem=/srv/vol1:ro\n") {
		t.Errorf("other pool not hidden:\n%s", b)
	}
}
