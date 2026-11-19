// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

type fakeLimits struct {
	slices  map[string]agentapi.SiteLimits
	quotas  map[string][2]uint64
	noQuota bool
	fail    error
}

func newFakeLimits() *fakeLimits {
	return &fakeLimits{slices: map[string]agentapi.SiteLimits{}, quotas: map[string][2]uint64{}}
}

func (f *fakeLimits) SiteLimitsSet(_ context.Context, user string, l agentapi.SiteLimits) error {
	if f.fail != nil {
		return f.fail
	}

	f.slices[user] = l

	return nil
}

func (f *fakeLimits) SiteQuotaSet(_ context.Context, user string, b, i uint64) (agentapi.SiteQuota, error) {
	if f.noQuota && (b != 0 || i != 0) {
		return agentapi.SiteQuota{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "no project quota"}
	}

	f.quotas[user] = [2]uint64{b, i}

	return agentapi.SiteQuota{Available: !f.noQuota, BytesLimit: b, InodesLimit: i}, nil
}

func (f *fakeLimits) SiteQuotaGet(_ context.Context, user string) (agentapi.SiteQuota, error) {
	q := f.quotas[user]

	return agentapi.SiteQuota{Available: !f.noQuota, Bytes: 1 << 30, BytesLimit: q[0], InodesLimit: q[1]}, nil
}

func TestCheckLimits(t *testing.T) {
	good := store.Limits{DiskBytes: 1 << 30, DiskInodes: 10_000, MemoryBytes: 256 << 20, PHPWorkers: 2, Tasks: 32, CPUPriority: "low"}
	if err := CheckLimits(good, true); err != nil {
		t.Fatal(err)
	}

	unlimited := store.Limits{PHPWorkers: 2, CPUPriority: "normal"}
	if err := CheckLimits(unlimited, false); err != nil {
		t.Errorf("unlimited custom values: %v", err)
	}

	if err := CheckLimits(unlimited, true); !errors.Is(err, ErrUnlimitedDefault) {
		t.Errorf("unlimited default: %v", err)
	}

	for name, mod := range map[string]func(*store.Limits){
		"priority":     func(l *store.Limits) { l.CPUPriority = "max" },
		"no workers":   func(l *store.Limits) { l.PHPWorkers = 0 },
		"many workers": func(l *store.Limits) { l.PHPWorkers = agentapi.MaxPHPWorkers + 1 },
		"tiny disk":    func(l *store.Limits) { l.DiskBytes = 1 << 20 },
		"few inodes":   func(l *store.Limits) { l.DiskInodes = 10 },
		"tiny memory":  func(l *store.Limits) { l.MemoryBytes = 1 << 20 },
		"few tasks":    func(l *store.Limits) { l.Tasks = 2 },
		"negative":     func(l *store.Limits) { l.DiskBytes = -1 },
		"cpu quota":    func(l *store.Limits) { l.CPUQuotaPercent = int64(agentapi.MaxCPUQuotaPercent) + 1 },
	} {
		l := good
		mod(&l)

		if err := CheckLimits(l, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLimitPresetsService(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()

	all, err := s.LimitPresets(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("presets %v %v", all, err)
	}

	vals := store.Limits{DiskBytes: 2 << 30, DiskInodes: 50_000, MemoryBytes: 256 << 20, PHPWorkers: 3, Tasks: 64, CPUPriority: "low"}

	for _, name := range []string{"", "Tiny", "a b", "-x", "../x"} {
		if _, err := s.CreateLimitPreset(ctx, 1, name, vals, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("name %q: %v", name, err)
		}
	}

	if _, err := s.CreateLimitPreset(ctx, 1, "small", vals, false); !errors.Is(err, ErrPresetExists) {
		t.Errorf("duplicate: %v", err)
	}

	if _, err := s.CreateLimitPreset(ctx, 1, "open", store.Limits{PHPWorkers: 5, CPUPriority: "normal"}, true); !errors.Is(err, ErrUnlimitedDefault) {
		t.Errorf("unlimited default: %v", err)
	}

	if _, err := s.CreateLimitPreset(ctx, 1, "open", store.Limits{PHPWorkers: 5, CPUPriority: "normal"}, false); err != nil {
		t.Errorf("unlimited preset (not default): %v", err)
	}

	// The default preset cannot be made unlimited by an update either.
	if _, err := s.UpdateLimitPreset(ctx, 1, "small", "small", store.Limits{PHPWorkers: 5, CPUPriority: "normal"}, false); !errors.Is(err, ErrUnlimitedDefault) {
		t.Errorf("update default to unlimited: %v", err)
	}

	if err := s.DeleteLimitPreset(ctx, 1, "small"); !errors.Is(err, ErrPresetDefault) {
		t.Errorf("delete default: %v", err)
	}

	if err := s.DeleteLimitPreset(ctx, 1, "nope"); !errors.Is(err, ErrPresetNotFound) {
		t.Errorf("delete unknown: %v", err)
	}

	if err := s.DeleteLimitPreset(ctx, 1, "open"); err != nil {
		t.Error(err)
	}
}

func TestSiteLimitsService(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()

	// Without the agent part, a new site gets no limits.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "old", Domains: []string{"old.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if v, err := s.SiteLimits(ctx, "old"); err != nil || v.Set {
		t.Fatalf("site without limits agent: %+v %v", v, err)
	}

	if _, err := s.SetSiteLimits(ctx, 1, "old", "small", nil); !errors.Is(err, ErrNoLimits) {
		t.Errorf("without agent: %v", err)
	}

	ag := newFakeLimits()
	s.EnableLimits(ag)

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	v, err := s.SiteLimits(ctx, "shop")
	if err != nil || !v.Set || v.Preset != "small" || v.AppliedAt.IsZero() || v.ApplyError != "" {
		t.Fatalf("new site %+v %v", v, err)
	}

	if got := ag.slices["shop"]; got.MemoryMax != 512<<20 || got.TasksMax != 128 || got.CPUWeight != agentapi.CPUWeightNormal || got.CPUQuotaPercent != 0 {
		t.Errorf("slice of a new site %+v", got)
	}

	if got := ag.quotas["shop"]; got != [2]uint64{10 << 30, 500_000} {
		t.Errorf("quota of a new site %v", got)
	}

	// Another preset, then custom unlimited values.
	if _, err := s.SetSiteLimits(ctx, 1, "shop", "medium", nil); err != nil {
		t.Fatal(err)
	}

	if ag.slices["shop"].CPUWeight != agentapi.CPUWeightHigh || ag.quotas["shop"][0] != 25<<30 {
		t.Errorf("medium: %+v %v", ag.slices["shop"], ag.quotas["shop"])
	}

	open := store.Limits{PHPWorkers: 8, CPUPriority: "low", CPUQuotaPercent: 150}

	v, err = s.SetSiteLimits(ctx, 1, "shop", "", &open)
	if err != nil || v.Preset != "" || v.Limits != open {
		t.Fatalf("custom %+v %v", v, err)
	}

	if got := ag.slices["shop"]; got.MemoryMax != 0 || got.TasksMax != 0 || got.CPUWeight != agentapi.CPUWeightLow || got.CPUQuotaPercent != 150 {
		t.Errorf("unlimited slice %+v", got)
	}

	if ag.quotas["shop"] != [2]uint64{} {
		t.Errorf("unlimited quota %v", ag.quotas["shop"])
	}

	for name, call := range map[string]func() error{
		"both":    func() error { _, err := s.SetSiteLimits(ctx, 1, "shop", "small", &open); return err },
		"neither": func() error { _, err := s.SetSiteLimits(ctx, 1, "shop", "", nil); return err },
		"bad custom": func() error {
			_, err := s.SetSiteLimits(ctx, 1, "shop", "", &store.Limits{CPUPriority: "x"})
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, err := s.SetSiteLimits(ctx, 1, "shop", "nope", nil); !errors.Is(err, ErrPresetNotFound) {
		t.Errorf("unknown preset: %v", err)
	}

	if _, err := s.SetSiteLimits(ctx, 1, "nope", "small", nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown site: %v", err)
	}

	// A failed apply keeps the values as desired state and records the error.
	ag.fail = errors.New("agent down")

	if _, err := s.SetSiteLimits(ctx, 1, "shop", "large", nil); err == nil {
		t.Fatal("failed apply hidden")
	}

	v, _ = s.SiteLimits(ctx, "shop")
	if v.Preset != "large" || v.ApplyError != "agent down" || !v.AppliedAt.IsZero() {
		t.Errorf("after failed apply %+v", v)
	}

	ag.fail = nil

	// A preset change reaches the sites that follow it.
	large, _ := s.LimitPreset(ctx, "large")
	l := large.Limits
	l.MemoryBytes = 3 << 30

	if _, err := s.UpdateLimitPreset(ctx, 1, "large", "xl", l, false); err != nil {
		t.Fatal(err)
	}

	v, _ = s.SiteLimits(ctx, "shop")
	if v.Preset != "xl" || v.Limits.MemoryBytes != 3<<30 || v.ApplyError != "" || v.AppliedAt.IsZero() {
		t.Errorf("follower after preset change %+v", v)
	}

	if ag.slices["shop"].MemoryMax != 3<<30 {
		t.Errorf("follower slice %+v", ag.slices["shop"])
	}

	// No project quota: the slice is applied, the disk limits are not
	// enforced, and that is no failure.
	ag.noQuota = true

	if _, err := s.SetSiteLimits(ctx, 1, "shop", "small", nil); err != nil {
		t.Fatalf("without project quota: %v", err)
	}

	if u, err := s.SiteUsage(ctx, "shop"); err != nil || u.Available {
		t.Errorf("usage without quota %+v %v", u, err)
	}
}

func webWorkers(f *fakeAgent, site string) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for _, ws := range f.web.Sites {
		if ws.User == site {
			return ws.PHPWorkers, true
		}
	}

	return 0, false
}

func TestPHPWorkersReachThePool(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	s.EnableLimits(newFakeLimits())

	// The default preset's workers go out with the first web state.
	medium, err := s.LimitPreset(ctx, "medium")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpdateLimitPreset(ctx, 1, "medium", "medium", medium.Limits, true); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if n, ok := webWorkers(ag, "blog"); !ok || n != 10 {
		t.Fatalf("workers of a new site: %d %v", n, ok)
	}

	custom := medium.Limits
	custom.PHPWorkers = 3

	applies := ag.applies

	if _, err := s.SetSiteLimits(ctx, 1, "blog", "", &custom); err != nil {
		t.Fatal(err)
	}

	if n, _ := webWorkers(ag, "blog"); n != 3 || ag.applies != applies+1 {
		t.Errorf("custom workers: %d, applies %d -> %d", n, applies, ag.applies)
	}

	// Same workers: no new web state.
	custom.MemoryBytes = 2 << 30
	applies = ag.applies

	if _, err := s.SetSiteLimits(ctx, 1, "blog", "", &custom); err != nil || ag.applies != applies {
		t.Errorf("unchanged workers sent the web state again (%d -> %d): %v", applies, ag.applies, err)
	}

	// Back on the preset, then the preset changes its workers.
	if _, err := s.SetSiteLimits(ctx, 1, "blog", "medium", nil); err != nil {
		t.Fatal(err)
	}

	l := medium.Limits
	l.PHPWorkers = 15

	if _, err := s.UpdateLimitPreset(ctx, 1, "medium", "medium", l, false); err != nil {
		t.Fatal(err)
	}

	if n, _ := webWorkers(ag, "blog"); n != 15 {
		t.Errorf("workers after preset change: %d", n)
	}

	// A site without PHP sends none.
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "static", Domains: []string{"static.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if n, ok := webWorkers(ag, "static"); !ok || n != 0 {
		t.Errorf("static site workers: %d %v", n, ok)
	}
}
