// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/backup"
	"github.com/maki-it/hostashi/internal/quota"
)

type limits struct{ bytes, inodes uint64 }

// fakeQuota records what the agent asks of the file system.
type fakeQuota struct {
	mu        sync.Mutex
	available bool
	state     quota.State // what Status reports while available
	enforced  []string    // dirs given to Enforce
	assignErr error
	assigned  map[string]uint32 // dir -> project
	limits    map[uint32]limits
	dirs      map[uint32]string // project -> dir given to SetLimits
}

func newFakeQuota(available bool) *fakeQuota {
	return &fakeQuota{available: available, state: quota.Enforced, assigned: map[string]uint32{}, limits: map[uint32]limits{}, dirs: map[uint32]string{}}
}

func (f *fakeQuota) Available(string) (bool, error) { return f.available, nil }

func (f *fakeQuota) Status(string) (quota.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.available {
		return quota.Off, nil
	}

	return f.state, nil
}

func (f *fakeQuota) Enforce(dir string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.available {
		return quota.ErrUnavailable
	}

	f.state = quota.Enforced
	f.enforced = append(f.enforced, dir)

	return nil
}

func (f *fakeQuota) Assign(dir string, id uint32, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.assignErr != nil {
		return f.assignErr
	}

	f.assigned[dir] = id

	return nil
}

func (f *fakeQuota) SetLimits(dir string, id uint32, b, i uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.limits[id], f.dirs[id] = limits{b, i}, dir

	return nil
}

func (f *fakeQuota) Get(_ string, id uint32) (quota.Usage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	l := f.limits[id]

	return quota.Usage{Bytes: 4096, Inodes: 3, BytesLimit: l.bytes, InodesLimit: l.inodes}, nil
}

func TestSiteQuota(t *testing.T) {
	ctx := context.Background()
	fq := newFakeQuota(true)
	c, pool := startSiteAgentCfg(t, Config{Quota: fq})

	// Counting only, as after a mount: the set must switch enforcement on.
	fq.mu.Lock()
	fq.state, fq.enforced = quota.Counting, nil
	fq.mu.Unlock()

	site, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := agentapi.SiteQuotaSet.Call(ctx, c, agentapi.SiteQuotaSetRequest{User: "shop", Bytes: 10 << 30, Inodes: 500000})
	if err != nil {
		t.Fatalf("set: %v", err)
	}

	want := agentapi.SiteQuota{Available: true, Bytes: 4096, Inodes: 3, BytesLimit: 10 << 30, InodesLimit: 500000}
	if got != want {
		t.Errorf("set returned %+v, want %+v", got, want)
	}

	if !slices.Contains(fq.enforced, pool) {
		t.Errorf("limits set without enforcing them on %s: %v", pool, fq.enforced)
	}

	if fq.assigned[pool+"/shop"] != site.UID {
		t.Errorf("home not assigned to project %d: %v", site.UID, fq.assigned)
	}

	if got, err := agentapi.SiteQuotaGet.Call(ctx, c, agentapi.SiteQuotaGetRequest{User: "shop"}); err != nil || got != want {
		t.Errorf("get = %+v, %v; want %+v", got, err, want)
	}

	// Unknown site, bad values.
	_, err = agentapi.SiteQuotaGet.Call(ctx, c, agentapi.SiteQuotaGetRequest{User: "nosuch"})
	wantCode(t, err, agentrpc.CodeNotFound)

	for _, r := range []agentapi.SiteQuotaSetRequest{
		{User: "shop", Bytes: 1 << 20},
		{User: "shop", Bytes: agentapi.MaxQuotaBytes + 1},
		{User: "shop", Inodes: 10},
		{User: "shop", Inodes: agentapi.MaxQuotaInodes + 1},
		{User: "../etc", Bytes: 10 << 30},
	} {
		_, err := agentapi.SiteQuotaSet.Call(ctx, c, r)
		wantCode(t, err, agentrpc.CodeInvalidArgument)
	}

	// Unsafe tree.
	fq.assignErr = quota.ErrUnsafe
	_, err = agentapi.SiteQuotaSet.Call(ctx, c, agentapi.SiteQuotaSetRequest{User: "shop", Bytes: 10 << 30})
	wantCode(t, err, agentrpc.CodePrecondition)

	fq.assignErr = nil

	// Deleting the site clears its limits on the pool.
	if _, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "shop"}); err != nil {
		t.Fatal(err)
	}

	if l := fq.limits[site.UID]; l != (limits{}) || fq.dirs[site.UID] != pool {
		t.Errorf("after delete: limits %+v on %q, want none on %q", l, fq.dirs[site.UID], pool)
	}
}

func TestSiteQuotaUnavailable(t *testing.T) {
	ctx := context.Background()
	fq := newFakeQuota(false)
	c, _ := startSiteAgentCfg(t, Config{Quota: fq})

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop"}); err != nil {
		t.Fatal(err)
	}

	_, err := agentapi.SiteQuotaSet.Call(ctx, c, agentapi.SiteQuotaSetRequest{User: "shop", Bytes: 10 << 30})
	wantCode(t, err, agentrpc.CodePrecondition)

	// Unlimited is fine without quota, and get reports it as unavailable.
	got, err := agentapi.SiteQuotaSet.Call(ctx, c, agentapi.SiteQuotaSetRequest{User: "shop"})
	if err != nil || got != (agentapi.SiteQuota{}) {
		t.Errorf("unlimited without quota = %+v, %v", got, err)
	}

	got, err = agentapi.SiteQuotaGet.Call(ctx, c, agentapi.SiteQuotaGetRequest{User: "shop"})
	if err != nil || got.Available {
		t.Errorf("get without quota = %+v, %v", got, err)
	}

	if len(fq.assigned) != 0 || len(fq.limits) != 0 {
		t.Errorf("touched the file system: %v %v", fq.assigned, fq.limits)
	}
}

func TestRestoreSpaceFromQuota(t *testing.T) {
	ctx := context.Background()
	fq := newFakeQuota(true)
	b := &backup.Agent{Units: &countingUnits{}}
	c, _ := startSiteAgentCfg(t, Config{Quota: fq, Backup: b})

	if b.Space == nil {
		t.Fatal("the agent did not give the backup side a space check")
	}

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "shop"}); err != nil {
		t.Fatal(err)
	}

	if sp, err := b.Space("shop"); err != nil || sp.Limited {
		t.Fatalf("site without limits: %+v %v", sp, err)
	}

	if _, err := agentapi.SiteQuotaSet.Call(ctx, c, agentapi.SiteQuotaSetRequest{User: "shop", Bytes: 1 << 30, Inodes: 1000}); err != nil {
		t.Fatal(err)
	}

	sp, err := b.Space("shop")
	if err != nil || !sp.Limited || sp.FreeBytes != 1<<30-4096 || sp.FreeInodes != 997 || sp.BytesLimit != 1<<30 {
		t.Fatalf("limited site: %+v %v", sp, err)
	}

	if _, err := b.Space("nope"); err == nil {
		t.Error("unknown site has space")
	}

	fq.available = false

	if sp, err := b.Space("shop"); err != nil || sp.Limited {
		t.Errorf("without project quota: %+v %v", sp, err)
	}
}
