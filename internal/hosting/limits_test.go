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
	// conns records db.site.connections.set; noDB answers it like a
	// server without MariaDB, connFail fails it.
	conns    map[string]uint32
	noDB     bool
	connFail error
}

func newFakeLimits() *fakeLimits {
	return &fakeLimits{slices: map[string]agentapi.SiteLimits{}, quotas: map[string][2]uint64{}, conns: map[string]uint32{}}
}

func (f *fakeLimits) DBSiteConnections(_ context.Context, site string, n uint32) error {
	switch {
	case f.noDB:
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "MariaDB is not available on this server"}
	case f.connFail != nil:
		return f.connFail
	}

	f.conns[site] = n

	return nil
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
		if _, err := s.CreateLimitPreset(ctx, 1, name, vals, store.PresetCaches{}, false); !errors.Is(err, ErrInvalid) {
			t.Errorf("name %q: %v", name, err)
		}
	}

	if _, err := s.CreateLimitPreset(ctx, 1, "small", vals, store.PresetCaches{}, false); !errors.Is(err, ErrPresetExists) {
		t.Errorf("duplicate: %v", err)
	}

	if _, err := s.CreateLimitPreset(ctx, 1, "open", store.Limits{PHPWorkers: 5, CPUPriority: "normal"}, store.PresetCaches{}, true); !errors.Is(err, ErrUnlimitedDefault) {
		t.Errorf("unlimited default: %v", err)
	}

	if _, err := s.CreateLimitPreset(ctx, 1, "open", store.Limits{PHPWorkers: 5, CPUPriority: "normal"}, store.PresetCaches{}, false); err != nil {
		t.Errorf("unlimited preset (not default): %v", err)
	}

	// The default preset cannot be made unlimited by an update either.
	if _, err := s.UpdateLimitPreset(ctx, 1, "small", "small", store.Limits{PHPWorkers: 5, CPUPriority: "normal"}, store.PresetCaches{}, false); !errors.Is(err, ErrUnlimitedDefault) {
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

	if _, err := s.UpdateLimitPreset(ctx, 1, "large", "xl", l, store.PresetCaches{}, false); err != nil {
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

	if _, err := s.UpdateLimitPreset(ctx, 1, "medium", "medium", medium.Limits, store.PresetCaches{}, true); err != nil {
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

	if _, err := s.UpdateLimitPreset(ctx, 1, "medium", "medium", l, store.PresetCaches{}, false); err != nil {
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

func TestImportLimits(t *testing.T) {
	def := store.Limits{DiskBytes: 10 << 30, DiskInodes: 500_000, MemoryBytes: 512 << 20, PHPWorkers: 5, Tasks: 128, CPUPriority: "normal"}

	if l, raised := ImportLimits(def, Usage{Bytes: 2 << 30, Inodes: 1000}); raised || l != def {
		t.Errorf("small site: %+v %v", l, raised)
	}

	// 9 GiB * 1.5 = 13.5 GiB -> 14 GiB; 400k * 1.5 = 600k files.
	l, raised := ImportLimits(def, Usage{Bytes: 9 << 30, Inodes: 400_000})
	if !raised || l.DiskBytes != 14<<30 || l.DiskInodes != 600_000 || l.MemoryBytes != def.MemoryBytes || l.PHPWorkers != 5 {
		t.Errorf("large site: %+v %v", l, raised)
	}

	if l, _ := ImportLimits(def, Usage{Bytes: 1, Inodes: 333_334}); l.DiskBytes != def.DiskBytes || l.DiskInodes != 501_000 {
		t.Errorf("rounding: %+v", l)
	}

	if l, _ := ImportLimits(def, Usage{Bytes: 1 << 62}); l.DiskBytes != int64(agentapi.MaxQuotaBytes) {
		t.Errorf("clamp: %d", l.DiskBytes)
	}

	open := def
	open.DiskBytes, open.DiskInodes = 0, 0

	if l, raised := ImportLimits(open, Usage{Bytes: 100 << 30, Inodes: 5_000_000}); raised || l != open {
		t.Errorf("unlimited stays unlimited: %+v %v", l, raised)
	}
}

func TestCreateWithUsage(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()
	ag := newFakeLimits()

	s.EnableLimits(ag)

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "big", Domains: []string{"big.example.com"}, Usage: &Usage{Bytes: 20 << 30, Inodes: 100}}); err != nil {
		t.Fatal(err)
	}

	v, err := s.SiteLimits(ctx, "big")
	if err != nil || v.Preset != "" || v.Limits.DiskBytes != 30<<30 || v.Limits.DiskInodes != 500_000 || v.Limits.MemoryBytes != 512<<20 {
		t.Fatalf("imported big site %+v %v", v, err)
	}

	if ag.quotas["big"] != [2]uint64{30 << 30, 500_000} {
		t.Errorf("quota %v", ag.quotas["big"])
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "tiny", Domains: []string{"tiny.example.com"}, Usage: &Usage{Bytes: 1 << 20, Inodes: 10}}); err != nil {
		t.Fatal(err)
	}

	if v, _ := s.SiteLimits(ctx, "tiny"); v.Preset != "small" {
		t.Errorf("small import should follow the default preset: %+v", v)
	}
}

// The database connection limit (ADR-0057) goes out with the other limits
// and with every new local database user.
func TestDatabaseConnectionLimits(t *testing.T) {
	s, fa, _ := newService(t)
	ctx := t.Context()
	ag := newFakeLimits()
	s.EnableLimits(ag)

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if ag.conns["shop"] != 20 {
		t.Fatalf("new site: %v", ag.conns)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_wp", User: "shop_wp"}); err != nil {
		t.Fatal(err)
	}

	if fa.userMax["shop_wp"] != 20 {
		t.Fatalf("user: %v", fa.userMax)
	}

	custom := store.Limits{PHPWorkers: 5, CPUPriority: "normal", DatabaseConnections: 7}
	if _, err := s.SetSiteLimits(ctx, 1, "shop", "", &custom); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateDatabaseUser(ctx, 1, "shop_wp", "second", ""); err != nil {
		t.Fatal(err)
	}

	if ag.conns["shop"] != 7 || fa.userMax["second"] != 7 {
		t.Fatalf("custom: %v %v", ag.conns, fa.userMax)
	}

	// No MariaDB on the server: the other limits still count as applied.
	ag.noDB = true

	v, err := s.SetSiteLimits(ctx, 1, "shop", "small", nil)
	if err != nil || v.AppliedAt.IsZero() || v.ApplyError != "" {
		t.Fatalf("without MariaDB: %+v %v", v, err)
	}

	// A failure is recorded on the site.
	ag.noDB, ag.connFail = false, errDown

	if _, err := s.SetSiteLimits(ctx, 1, "shop", "medium", nil); err == nil {
		t.Fatal("failure not reported")
	}

	if v, _ := s.SiteLimits(ctx, "shop"); !v.AppliedAt.IsZero() || v.ApplyError == "" {
		t.Fatalf("failure not recorded: %+v", v)
	}

	if err := CheckLimits(store.Limits{PHPWorkers: 5, CPUPriority: "normal", DatabaseConnections: 10001}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("too many connections: %v", err)
	}

	if err := CheckLimits(store.Limits{PHPWorkers: 5, CPUPriority: "normal", DatabaseBytes: -1}, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("negative size: %v", err)
	}
}

func TestApplyPendingLimits(t *testing.T) {
	s, _, st := newService(t)
	ctx := t.Context()

	s.ApplyPendingLimits(ctx) // without the agent part: nothing

	ag := newFakeLimits()
	s.EnableLimits(ag)

	for _, n := range []string{"shop", "blog"} {
		if _, err := s.Create(ctx, 1, CreateRequest{Name: n, Domains: []string{n + ".example.com"}}); err != nil {
			t.Fatal(err)
		}
	}

	shop, _ := st.SiteByName(ctx, "shop")

	// As after the migration: shop is pending, blog applied.
	if err := st.MarkSiteLimits(ctx, shop.ID, 0, ""); err != nil {
		t.Fatal(err)
	}

	ag.conns = map[string]uint32{}

	s.ApplyPendingLimits(ctx)

	if len(ag.conns) != 1 || ag.conns["shop"] != 20 {
		t.Fatalf("applied %v", ag.conns)
	}

	if v, _ := s.SiteLimits(ctx, "shop"); v.AppliedAt.IsZero() {
		t.Fatalf("not marked: %+v", v)
	}

	// A failure stays pending for the next start.
	if err := st.MarkSiteLimits(ctx, shop.ID, 0, ""); err != nil {
		t.Fatal(err)
	}

	ag.connFail = errDown

	s.ApplyPendingLimits(ctx)

	if v, _ := s.SiteLimits(ctx, "shop"); !v.AppliedAt.IsZero() || v.ApplyError == "" {
		t.Fatalf("failure: %+v", v)
	}
}
