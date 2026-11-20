// SPDX-License-Identifier: AGPL-3.0-or-later

package siteslice

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type countCtl struct {
	reloads int
	sets    []string // slice name, then the properties
	fail    error
}

func (c *countCtl) DaemonReload(context.Context) error { c.reloads++; return nil }

func (c *countCtl) SetSliceProperties(_ context.Context, slice string, props []string) error {
	if c.fail != nil {
		return c.fail
	}

	c.sets = append(c.sets, slice+" "+strings.Join(props, " "))

	return nil
}

func TestName(t *testing.T) {
	for user, want := range map[string]string{
		"shop":    "hostashi-site-shop.slice",
		"my-shop": `hostashi-site-my\x2dshop.slice`,
		"a_b-c-d": `hostashi-site-a_b\x2dc\x2dd.slice`,
	} {
		if got := Name(user); got != want {
			t.Errorf("Name(%q) = %q, want %q", user, got, want)
		}
	}
}

func TestRender(t *testing.T) {
	got, err := Render("shop", Limits{MemoryMax: 512 << 20, TasksMax: 128, CPUWeight: 200})
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{"[Slice]", "CPUWeight=200", "MemoryHigh=483183819", "MemoryMax=536870912", "TasksMax=128", "MemoryAccounting=yes"} {
		if !strings.Contains(string(got), "\n"+line+"\n") {
			t.Errorf("missing %q in\n%s", line, got)
		}
	}

	if strings.Contains(string(got), "CPUQuota") {
		t.Errorf("CPUQuota without a quota:\n%s", got)
	}

	open, err := Render("shop", Limits{CPUQuotaPercent: 150})
	if err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{"CPUWeight=100", "CPUQuota=150%", "MemoryMax=infinity", "MemoryHigh=infinity", "TasksMax=infinity"} {
		if !strings.Contains(string(open), "\n"+line+"\n") {
			t.Errorf("missing %q in\n%s", line, open)
		}
	}

	for _, bad := range []Limits{
		{MemoryMax: 1 << 20},
		{MemoryMax: agentapi.MaxMemoryMax + 1},
		{TasksMax: 3},
		{TasksMax: agentapi.MaxTasksMax + 1},
		{CPUWeight: 10001},
		{CPUQuotaPercent: agentapi.MaxCPUQuotaPercent + 1},
	} {
		if _, err := Render("shop", bad); err == nil {
			t.Errorf("Render accepted %+v", bad)
		}
	}

	for _, user := range []string{"", "../etc", "root", "Shop"} {
		if _, err := Render(user, Limits{}); err == nil {
			t.Errorf("Render accepted user %q", user)
		}
	}
}

func TestManager(t *testing.T) {
	ctx := context.Background()
	ctl := &countCtl{}
	m := &Manager{Dir: t.TempDir(), Ctl: ctl}

	if _, ok, err := m.Get("shop"); err != nil || ok {
		t.Fatalf("Get before Set: %v %v", ok, err)
	}

	want := Limits{MemoryMax: 1 << 30, TasksMax: 256, CPUWeight: 200, CPUQuotaPercent: 300}
	if err := m.Set(ctx, "my-shop", 1234, want); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(m.Dir, `hostashi-site-my\x2dshop.slice`)

	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("slice file: %v %v", st, err)
	}

	if got, ok, err := m.Get("my-shop"); err != nil || !ok || got != want {
		t.Fatalf("Get = %+v %v %v, want %+v", got, ok, err, want)
	}

	if ctl.reloads != 1 {
		t.Errorf("reloads after first Set: %d", ctl.reloads)
	}

	props := ` CPUWeight=200 CPUQuota=300% MemoryHigh=966367638 MemoryMax=1073741824 TasksMax=256`
	if len(ctl.sets) != 2 || ctl.sets[0] != `hostashi-site-my\x2dshop.slice`+props || ctl.sets[1] != "user-1234.slice"+props {
		t.Errorf("applied %q", ctl.sets)
	}

	// The user slice drop-in carries the same limits for SSH sessions.
	drop, err := os.ReadFile(filepath.Join(m.Dir, "user-1234.slice.d", "10-hostashi.conf"))
	if err != nil || !strings.Contains(string(drop), "[Slice]\nMemoryAccounting=yes\n") || !strings.Contains(string(drop), "\nMemoryMax=1073741824\n") ||
		strings.Contains(string(drop), "[Unit]") {
		t.Fatalf("user drop-in %q %v", drop, err)
	}

	if err := m.Set(ctx, "my-shop", 1234, want); err != nil || ctl.reloads != 1 {
		t.Errorf("unchanged Set reloaded systemd (%d) or failed: %v", ctl.reloads, err)
	}

	if len(ctl.sets) != 4 {
		t.Errorf("unchanged Set did not apply the limits again: %q", ctl.sets)
	}

	ctl.fail = errors.New("boom")

	if err := m.Set(ctx, "my-shop", 1234, want); err == nil {
		t.Error("Set hid a failed apply")
	}

	ctl.fail = nil

	if err := m.Set(ctx, "my-shop", 1234, Limits{}); err != nil {
		t.Fatal(err)
	}

	if got, _, _ := m.Get("my-shop"); got != (Limits{}) {
		t.Errorf("after clearing: %+v", got)
	}

	wantClear := `user-1234.slice CPUWeight=100 CPUQuota= MemoryHigh=infinity MemoryMax=infinity TasksMax=infinity`
	if last := ctl.sets[len(ctl.sets)-1]; last != wantClear {
		t.Errorf("clearing applied %q, want %q", last, wantClear)
	}

	if err := m.Set(ctx, "my-shop", 1234, Limits{TasksMax: 2}); err == nil {
		t.Error("Set accepted TasksMax 2")
	}

	// Root's and the system users' slices are never touched.
	for _, uid := range []uint32{0, 999} {
		if err := m.Set(ctx, "my-shop", uid, Limits{}); err == nil {
			t.Errorf("Set accepted uid %d", uid)
		}
	}

	if err := m.Remove(ctx, "my-shop", 999); err == nil {
		t.Error("Remove accepted uid 999")
	}

	if err := m.Remove(ctx, "my-shop", 1234); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("slice file still there: %v", err)
	}

	if err := m.Remove(ctx, "my-shop", 1234); err != nil {
		t.Errorf("second Remove: %v", err)
	}

	if ctl.reloads != 3 {
		t.Errorf("reloads: %d, want 3", ctl.reloads)
	}

	if entries, _ := os.ReadDir(m.Dir); len(entries) != 0 {
		t.Errorf("left files: %v", entries)
	}
}

func TestRemoveClearsRuntimeDropIns(t *testing.T) {
	ctl := &countCtl{}
	m := &Manager{Dir: t.TempDir(), Ctl: ctl, ControlDir: t.TempDir()}

	if err := m.Set(t.Context(), "my-shop", 1234, Limits{TasksMax: 64}); err != nil {
		t.Fatal(err)
	}

	dropins := filepath.Join(m.ControlDir, `hostashi-site-my\x2dshop.slice.d`)
	if err := os.MkdirAll(dropins, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dropins, "50-TasksMax.conf"), []byte("[Slice]\nTasksMax=64\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	other := filepath.Join(m.ControlDir, "hostashi-site-shop.slice.d")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := m.Remove(t.Context(), "my-shop", 1234); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dropins); !os.IsNotExist(err) {
		t.Errorf("runtime drop-ins still there: %v", err)
	}

	if _, err := os.Stat(other); err != nil {
		t.Errorf("other site's drop-ins removed: %v", err)
	}
}

func TestRemoveKeepsForeignUserDropIns(t *testing.T) {
	ctl := &countCtl{}
	m := &Manager{Dir: t.TempDir(), Ctl: ctl, ControlDir: t.TempDir()}

	if err := m.Set(t.Context(), "shop", 1234, Limits{TasksMax: 64}); err != nil {
		t.Fatal(err)
	}

	foreign := filepath.Join(m.Dir, "user-1234.slice.d", "60-admin.conf")
	if err := os.WriteFile(foreign, []byte("[Slice]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runtime := filepath.Join(m.ControlDir, "user-1234.slice.d")
	if err := os.MkdirAll(runtime, 0o755); err != nil {
		t.Fatal(err)
	}

	// Without the uid only the site's slice goes.
	if err := m.Remove(t.Context(), "shop", 0); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(m.Dir, "user-1234.slice.d", "10-hostashi.conf")); err != nil {
		t.Errorf("drop-in removed without the uid: %v", err)
	}

	if err := m.Remove(t.Context(), "shop", 1234); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(m.Dir, "user-1234.slice.d", "10-hostashi.conf")); !os.IsNotExist(err) {
		t.Errorf("drop-in left: %v", err)
	}

	if _, err := os.Stat(foreign); err != nil {
		t.Errorf("foreign drop-in removed: %v", err)
	}

	if _, err := os.Stat(runtime); !os.IsNotExist(err) {
		t.Errorf("runtime drop-ins left: %v", err)
	}
}

func TestModesUnderAgentUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	m := &Manager{Dir: t.TempDir(), Ctl: &countCtl{}}
	if err := m.Set(t.Context(), "shop", 1234, Limits{TasksMax: 64}); err != nil {
		t.Fatal(err)
	}

	for name, want := range map[string]os.FileMode{
		"hostashi-site-shop.slice":           0o644,
		"user-1234.slice.d":                  0o755 | os.ModeDir,
		"user-1234.slice.d/10-hostashi.conf": 0o644,
	} {
		if fi, err := os.Stat(filepath.Join(m.Dir, name)); err != nil || fi.Mode() != want {
			t.Errorf("%s: %v %v, want %v", name, fi.Mode(), err, want)
		}
	}
}
