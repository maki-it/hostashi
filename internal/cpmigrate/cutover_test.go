// SPDX-License-Identifier: AGPL-3.0-or-later

package cpmigrate_test

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/cpmigrate"
	"github.com/maki-it/hostashi/internal/store"
)

type fakeCutSites struct {
	st  *store.Store
	mu  sync.Mutex
	tls []string
	err error
}

func (f *fakeCutSites) Get(ctx context.Context, name string) (store.Site, error) {
	return f.st.SiteByName(ctx, name)
}

func (f *fakeCutSites) SetTLS(_ context.Context, _ int64, name string, on bool) (store.Site, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if on {
		f.tls = append(f.tls, name)
	}

	return store.Site{}, f.err
}

type fakeResolver struct {
	mu  sync.Mutex
	ips map[string][]netip.Addr
}

func (f *fakeResolver) set(host string, ips ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []netip.Addr
	for _, s := range ips {
		out = append(out, netip.MustParseAddr(s))
	}

	f.ips[host] = out
}

func (f *fakeResolver) Lookup(_ context.Context, host string) ([]netip.Addr, time.Duration, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	ips, ok := f.ips[host]
	if !ok {
		return nil, 0, cpmigrate.ErrDomainNotFound
	}

	return ips, 300 * time.Second, nil
}

func TestCutover(t *testing.T) {
	svc, st := newService(t, nil)
	ctx := t.Context()
	now := time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

	svc.SetNow(func() time.Time { return now })

	if _, err := svc.StartCutover(ctx, 1, "blog"); !errors.Is(err, cpmigrate.ErrNoCutover) {
		t.Fatalf("not enabled: %v", err)
	}

	if _, err := st.CreateSite(ctx, "blog", "default", "8.4", []string{"blog.example.com", "www.blog.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	sites := &fakeCutSites{st: st}
	res := &fakeResolver{ips: map[string][]netip.Addr{}}
	server := []netip.Addr{netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("2001:db8::7")}

	// No RunCutover: the test drives the checks itself.
	svc.EnableCutover(sites, res, func(context.Context) ([]netip.Addr, error) { return server, nil })

	if _, err := svc.StartCutover(ctx, 1, "blog"); !errors.Is(err, cpmigrate.ErrSiteState) {
		t.Fatalf("creating site: %v", err)
	}

	if _, err := svc.StartCutover(ctx, 1, "../x"); !errors.Is(err, cpmigrate.ErrBadRequest) {
		t.Fatalf("bad name: %v", err)
	}

	if err := st.SetSiteState(ctx, "blog", store.SiteActive, now); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.Cutover(ctx, "blog"); !errors.Is(err, cpmigrate.ErrNoCutover) {
		t.Fatalf("none yet: %v", err)
	}

	c, err := svc.StartCutover(ctx, 1, "blog")
	if err != nil || c.State != store.CutoverWaiting || !c.Deadline.Equal(now.Add(48*time.Hour)) || len(c.Server) != 2 {
		t.Fatalf("start %+v %v", c, err)
	}

	if _, err := svc.StartCutover(ctx, 1, "blog"); !errors.Is(err, cpmigrate.ErrCutoverRunning) {
		t.Fatalf("twice: %v", err)
	}

	// Still the old server, and one name not resolved at all.
	res.set("blog.example.com", "198.51.100.1")
	svc.CheckCutovers(ctx)

	c, _ = svc.Cutover(ctx, "blog")
	if c.State != store.CutoverWaiting || len(c.Answers) != 2 || c.Answers[0].Here || c.Answers[0].Addrs[0] != "198.51.100.1" ||
		c.Answers[0].TTL == nil || *c.Answers[0].TTL != 300 || c.Answers[1].TTL != nil ||
		c.Answers[1].Error != "no such domain or no address" || c.CheckedAt == nil {
		t.Fatalf("old server %+v", c)
	}

	// Half switched: the AAAA record still names the old server.
	res.set("blog.example.com", "203.0.113.7", "2001:db8::1")
	res.set("www.blog.example.com", "203.0.113.7")
	svc.CheckCutovers(ctx)

	if c, _ = svc.Cutover(ctx, "blog"); c.State != store.CutoverWaiting || c.Answers[0].Here || !c.Answers[1].Here {
		t.Fatalf("half %+v", c)
	}

	if len(sites.tls) != 0 {
		t.Fatalf("tls too early: %v", sites.tls)
	}

	res.set("blog.example.com", "203.0.113.7", "::ffff:203.0.113.7", "2001:db8::7")
	svc.CheckCutovers(ctx)

	if c, _ = svc.Cutover(ctx, "blog"); c.State != store.CutoverSwitched || c.DoneAt == nil || c.Error != "" || len(sites.tls) != 1 {
		t.Fatalf("switched %+v %v", c, sites.tls)
	}

	// Done cut-overs are not checked again.
	svc.CheckCutovers(ctx)

	if len(sites.tls) != 1 {
		t.Fatalf("checked again: %v", sites.tls)
	}

	// A new cut-over that never switches fails after 48 hours.
	res.set("blog.example.com", "198.51.100.1")

	if _, err := svc.StartCutover(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	now = now.Add(48 * time.Hour)

	svc.CheckCutovers(ctx)

	if c, _ = svc.Cutover(ctx, "blog"); c.State != store.CutoverFailed || c.Error == "" {
		t.Fatalf("deadline %+v", c)
	}

	// Cancel, and a failure to turn on HTTPS is shown.
	if _, err := svc.StartCutover(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	if c, err = svc.CancelCutover(ctx, 1, "blog"); err != nil || c.State != store.CutoverCanceled {
		t.Fatalf("cancel %+v %v", c, err)
	}

	if _, err := svc.StartCutover(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	sites.err = errors.New("no issuer")

	res.set("blog.example.com", "203.0.113.7")
	svc.CheckCutovers(ctx)

	if c, _ = svc.Cutover(ctx, "blog"); c.State != store.CutoverSwitched || c.Error == "" {
		t.Fatalf("tls error %+v", c)
	}
}

func TestRunCutover(t *testing.T) {
	svc, st := newService(t, nil)
	ctx, cancel := context.WithCancel(t.Context())
	now := time.Now()

	if _, err := st.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if err := st.SetSiteState(ctx, "blog", store.SiteActive, now); err != nil {
		t.Fatal(err)
	}

	sites := &fakeCutSites{st: st}
	res := &fakeResolver{ips: map[string][]netip.Addr{}}
	res.set("blog.example.com", "203.0.113.7")
	svc.EnableCutover(sites, res, func(context.Context) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
	})

	done := make(chan struct{})

	go func() { svc.RunCutover(ctx, time.Hour); close(done) }()

	// Starting kicks the loop; no need to wait for the hour.
	if _, err := svc.StartCutover(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(10 * time.Second)

	for {
		if c, _ := svc.Cutover(ctx, "blog"); c.State == store.CutoverSwitched {
			break
		}

		if time.Now().After(deadline) {
			t.Fatal("not switched")
		}

		time.Sleep(10 * time.Millisecond)
	}

	cancel()
	<-done
}
