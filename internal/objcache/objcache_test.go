// SPDX-License-Identifier: AGPL-3.0-or-later

package objcache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type fakeCtl struct {
	calls   []string
	failOn  string // "verb unit" that fails
	running map[string]bool
}

func (f *fakeCtl) do(verb, unit string) error {
	f.calls = append(f.calls, verb+" "+unit)
	if f.failOn == verb+" "+unit {
		return errors.New("boom")
	}

	if f.running == nil {
		f.running = map[string]bool{}
	}

	switch verb {
	case "start", "restart":
		f.running[unit] = true
	case "stop":
		delete(f.running, unit)
	}

	return nil
}

func (f *fakeCtl) DaemonReload(context.Context) error {
	f.calls = append(f.calls, "daemon-reload")

	return nil
}
func (f *fakeCtl) Start(_ context.Context, u string) error   { return f.do("start", u) }
func (f *fakeCtl) Stop(_ context.Context, u string) error    { return f.do("stop", u) }
func (f *fakeCtl) Restart(_ context.Context, u string) error { return f.do("restart", u) }
func (f *fakeCtl) Show(_ context.Context, u string, _ ...string) (map[string]string, error) {
	if f.running[u] {
		return map[string]string{"ActiveState": "active", "MemoryCurrent": "4194304"}, nil
	}

	return map[string]string{"ActiveState": "inactive", "MemoryCurrent": "[not set]"}, nil
}

func newManager(t *testing.T) (*Manager, *fakeCtl) {
	t.Helper()

	dir := t.TempDir()
	ctl := &fakeCtl{}

	return &Manager{
		Dir: filepath.Join(dir, "valkey"), UnitDir: filepath.Join(dir, "units"), SocketDir: "/run/hostashi-valkey",
		Slice: func(s string) string { return "hostashi-site-" + s + ".slice" }, Ctl: ctl,
	}, ctl
}

func (f *fakeCtl) take() []string {
	c := f.calls
	f.calls = nil

	return c
}

func TestRender(t *testing.T) {
	m, _ := newManager(t)

	b, err := m.Render("blog", 64)
	if err != nil {
		t.Fatal(err)
	}

	conf := string(b)
	for _, want := range []string{
		"\nport 0\n", "\nunixsocket /run/hostashi-valkey/blog/valkey.sock\n", "\nunixsocketperm 600\n",
		"\ndir /run/hostashi-valkey/blog\n", "\nsave \"\"\n", "\nappendonly no\n", "\nmaxmemory 64mb\n",
		"\nmaxmemory-policy allkeys-lru\n", "\nuser default on nopass ~* &* +@all -@admin\n",
		"\nenable-debug-command no\n", "\nenable-module-command no\n", "\nenable-protected-configs no\n",
	} {
		if !strings.Contains(conf, want) {
			t.Errorf("config lacks %q:\n%s", want, conf)
		}
	}

	if strings.Contains(conf, "bind") || strings.Contains(conf, "requirepass") {
		t.Errorf("config listens on TCP or has a password:\n%s", conf)
	}

	for _, bad := range []struct {
		site string
		mb   int
	}{{"blog", 0}, {"blog", 16}, {"blog", 2048}, {"Blog", 64}, {"a/b", 64}, {"root", 64}, {"x\nport 6379", 64}} {
		if _, err := m.Render(bad.site, bad.mb); err == nil {
			t.Errorf("Render(%q, %d) accepted", bad.site, bad.mb)
		}
	}
}

func TestApplyLifecycle(t *testing.T) {
	ctx := context.Background()
	m, ctl := newManager(t)
	unit := "hostashi-valkey@blog.service"

	st, err := m.Apply(ctx, "blog", 64)
	if err != nil {
		t.Fatal(err)
	}

	if st.MemoryMB != 64 || st.State != "active" || st.UsedBytes != 4194304 || st.Socket != "/run/hostashi-valkey/blog/valkey.sock" {
		t.Errorf("status after enable: %+v", st)
	}

	if got, want := ctl.take(), []string{"daemon-reload", "start " + unit}; !slices.Equal(got, want) {
		t.Errorf("enable calls %v, want %v", got, want)
	}

	assertMode(t, filepath.Join(m.Dir, "blog.conf"), 0o600)
	assertMode(t, filepath.Join(m.Dir, "state.json"), 0o600)

	drop, err := os.ReadFile(filepath.Join(m.UnitDir, unit+".d", DropInName))
	if err != nil || !strings.Contains(string(drop), "\nSlice=hostashi-site-blog.slice\n") {
		t.Fatalf("drop-in %q, %v", drop, err)
	}

	// The same size again: the unit is only made sure to run.
	if _, err := m.Apply(ctx, "blog", 64); err != nil {
		t.Fatal(err)
	}

	if got, want := ctl.take(), []string{"start " + unit}; !slices.Equal(got, want) {
		t.Errorf("same size calls %v, want %v", got, want)
	}

	// A new size restarts it.
	if _, err := m.Apply(ctx, "blog", 128); err != nil {
		t.Fatal(err)
	}

	if got, want := ctl.take(), []string{"restart " + unit}; !slices.Equal(got, want) {
		t.Errorf("resize calls %v, want %v", got, want)
	}

	if b, _ := os.ReadFile(filepath.Join(m.Dir, "blog.conf")); !strings.Contains(string(b), "maxmemory 128mb") {
		t.Errorf("config not resized:\n%s", b)
	}

	// Flush restarts.
	if _, err := m.Flush(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	if got, want := ctl.take(), []string{"restart " + unit}; !slices.Equal(got, want) {
		t.Errorf("flush calls %v, want %v", got, want)
	}

	// Off removes everything.
	st, err = m.Apply(ctx, "blog", 0)
	if err != nil {
		t.Fatal(err)
	}

	if st.MemoryMB != 0 || st.Socket != "" {
		t.Errorf("status after disable: %+v", st)
	}

	if got, want := ctl.take(), []string{"stop " + unit, "daemon-reload"}; !slices.Equal(got, want) {
		t.Errorf("disable calls %v, want %v", got, want)
	}

	for _, p := range []string{filepath.Join(m.Dir, "blog.conf"), filepath.Join(m.UnitDir, unit+".d")} {
		if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left after disable: %v", p, err)
		}
	}

	// Off again, and flush without a cache.
	if _, err := m.Apply(ctx, "blog", 0); err != nil {
		t.Fatal(err)
	}

	if c := ctl.take(); len(c) != 0 {
		t.Errorf("second disable called %v", c)
	}

	if _, err := m.Flush(ctx, "blog"); !errors.Is(err, ErrNoCache) {
		t.Errorf("flush without cache: %v", err)
	}

	if st, err := m.Status(ctx, "blog"); err != nil || st.MemoryMB != 0 || st.State != "" {
		t.Errorf("status without cache: %+v, %v", st, err)
	}
}

func TestApplyStartFailure(t *testing.T) {
	ctx := context.Background()
	m, ctl := newManager(t)
	unit := "hostashi-valkey@blog.service"

	// A new cache that does not start is gone again.
	ctl.failOn = "start " + unit

	if _, err := m.Apply(ctx, "blog", 64); !errors.Is(err, ErrStartFailed) {
		t.Fatalf("want ErrStartFailed, got %v", err)
	}

	if st, _ := m.Status(ctx, "blog"); st.MemoryMB != 0 {
		t.Errorf("failed cache kept: %+v", st)
	}

	if _, err := os.Stat(filepath.Join(m.Dir, "blog.conf")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("config of the failed cache kept: %v", err)
	}

	// A resize that does not start goes back to the old size.
	ctl.failOn = ""

	if _, err := m.Apply(ctx, "blog", 64); err != nil {
		t.Fatal(err)
	}

	ctl.failOn = "restart " + unit

	if _, err := m.Apply(ctx, "blog", 256); !errors.Is(err, ErrStartFailed) {
		t.Fatalf("want ErrStartFailed, got %v", err)
	}

	if st, _ := m.Status(ctx, "blog"); st.MemoryMB != 64 {
		t.Errorf("size after failed resize: %d", st.MemoryMB)
	}

	if b, _ := os.ReadFile(filepath.Join(m.Dir, "blog.conf")); !strings.Contains(string(b), "maxmemory 64mb") {
		t.Errorf("config after failed resize:\n%s", b)
	}
}

func TestPausedAndRecover(t *testing.T) {
	ctx := context.Background()
	m, ctl := newManager(t)
	unit := "hostashi-valkey@shop.service"

	if err := m.SetPaused(ctx, "shop", true); err != nil {
		t.Fatal(err)
	}

	if c := ctl.take(); len(c) != 0 {
		t.Errorf("pausing a site without cache called %v", c)
	}

	if _, err := m.Apply(ctx, "shop", 64); err != nil {
		t.Fatal(err)
	}

	ctl.take()

	if err := m.SetPaused(ctx, "shop", true); err != nil {
		t.Fatal(err)
	}

	if got, want := ctl.take(), []string{"stop " + unit}; !slices.Equal(got, want) {
		t.Errorf("pause calls %v, want %v", got, want)
	}

	if _, err := m.Flush(ctx, "shop"); !errors.Is(err, ErrPaused) {
		t.Errorf("flush while paused: %v", err)
	}

	// A resize while paused keeps the unit off.
	if st, err := m.Apply(ctx, "shop", 128); err != nil || !st.Paused {
		t.Fatalf("resize while paused: %+v, %v", st, err)
	}

	if c := ctl.take(); slices.ContainsFunc(c, func(s string) bool { return strings.HasPrefix(s, "start") || strings.HasPrefix(s, "restart") }) {
		t.Errorf("resize while paused started the unit: %v", c)
	}

	if err := m.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if c := ctl.take(); len(c) != 0 {
		t.Errorf("recover started a paused cache: %v", c)
	}

	if err := m.SetPaused(ctx, "shop", false); err != nil {
		t.Fatal(err)
	}

	if got, want := ctl.take(), []string{"start " + unit}; !slices.Equal(got, want) {
		t.Errorf("resume calls %v, want %v", got, want)
	}

	// Recover rewrites a lost config and restarts the unit.
	if err := os.Remove(filepath.Join(m.Dir, "shop.conf")); err != nil {
		t.Fatal(err)
	}

	if err := m.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if got, want := ctl.take(), []string{"restart " + unit}; !slices.Equal(got, want) {
		t.Errorf("recover calls %v, want %v", got, want)
	}

	if err := m.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if got, want := ctl.take(), []string{"start " + unit}; !slices.Equal(got, want) {
		t.Errorf("second recover calls %v, want %v", got, want)
	}

	if err := m.Remove(ctx, "shop"); err != nil {
		t.Fatal(err)
	}

	if s, _ := m.Sites(); len(s) != 0 {
		t.Errorf("sites after remove: %v", s)
	}
}

func TestInvalidSite(t *testing.T) {
	ctx := context.Background()
	m, ctl := newManager(t)

	for _, s := range []string{"", "../x", "root", "Blog"} {
		if _, err := m.Apply(ctx, s, 64); err == nil {
			t.Errorf("Apply(%q) accepted", s)
		}

		if _, err := m.Flush(ctx, s); err == nil {
			t.Errorf("Flush(%q) accepted", s)
		}

		if err := m.Remove(ctx, s); err == nil {
			t.Errorf("Remove(%q) accepted", s)
		}
	}

	if len(ctl.calls) != 0 {
		t.Errorf("invalid sites reached systemd: %v", ctl.calls)
	}
}

func assertMode(t *testing.T, p string, want os.FileMode) {
	t.Helper()

	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}

	if st.Mode().Perm() != want {
		t.Errorf("%s: mode %o, want %o", p, st.Mode().Perm(), want)
	}
}
