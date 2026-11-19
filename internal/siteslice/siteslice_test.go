// SPDX-License-Identifier: AGPL-3.0-or-later

package siteslice

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type countCtl struct{ reloads int }

func (c *countCtl) DaemonReload(context.Context) error { c.reloads++; return nil }

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
	if err := m.Set(ctx, "my-shop", want); err != nil {
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

	if err := m.Set(ctx, "my-shop", want); err != nil || ctl.reloads != 1 {
		t.Errorf("unchanged Set reloaded systemd (%d) or failed: %v", ctl.reloads, err)
	}

	if err := m.Set(ctx, "my-shop", Limits{}); err != nil {
		t.Fatal(err)
	}

	if got, _, _ := m.Get("my-shop"); got != (Limits{}) {
		t.Errorf("after clearing: %+v", got)
	}

	if err := m.Set(ctx, "my-shop", Limits{TasksMax: 2}); err == nil {
		t.Error("Set accepted TasksMax 2")
	}

	if err := m.Remove(ctx, "my-shop"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("slice file still there: %v", err)
	}

	if err := m.Remove(ctx, "my-shop"); err != nil {
		t.Errorf("second Remove: %v", err)
	}

	if ctl.reloads != 3 {
		t.Errorf("reloads: %d, want 3", ctl.reloads)
	}

	if entries, _ := os.ReadDir(m.Dir); len(entries) != 0 {
		t.Errorf("left files: %v", entries)
	}
}
