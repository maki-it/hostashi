// SPDX-License-Identifier: AGPL-3.0-or-later

package servermon

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// SiteAgent is what the monitor asks the agent about sites (ADR-0063).
type SiteAgent interface {
	SitesStats(ctx context.Context) (agentapi.SitesStatsResponse, error)
}

// SiteStore is the part of internal/store the site samples use.
type SiteStore interface {
	PutSiteSamples(ctx context.Context, tier int, samples []store.SiteSample) error
	SiteSamples(ctx context.Context, tier int, site string, since time.Time) ([]store.SiteSample, error)
	SiteTotals(ctx context.Context, tier int, since time.Time) ([]store.SiteTotal, error)
	PruneSiteSamples(ctx context.Context, tier int, before time.Time) error
}

// sites holds the per-site tiers; the zero value is ready.
type sites struct {
	prevAt time.Time
	prev   map[string]agentapi.SiteUsage
	ring   map[string][]store.SiteSample
	b5, b1 map[string]*siteBucket
}

// delta is cur minus prev; a smaller cur means the counter started again
// (cgroup recreated, agent restarted), so cur itself is what happened since.
func delta(prev, cur uint64) uint64 {
	if cur < prev {
		return cur
	}

	return cur - prev
}

// DeriveSite turns two raw readings of a site into one point. ok is false
// when no rate can be computed (first reading, clock went back, too long a
// gap).
func DeriveSite(prevAt time.Time, prev agentapi.SiteUsage, at time.Time, cur agentapi.SiteUsage) (store.SiteSample, bool) {
	gap := at.Sub(prevAt)
	if gap <= 0 || gap > maxGap {
		return store.SiteSample{}, false
	}

	dt := gap.Seconds()
	rate := func(p, c uint64) float64 { return float64(delta(p, c)) / dt }

	p := store.SiteSample{
		Site: cur.Name, At: at,
		// usage_usec per second of wall time: 1e6 is one full core.
		CPU:     100 * rate(prev.CPUUsec, cur.CPUUsec) / 1e6,
		Tasks:   cur.Tasks,
		IORead:  rate(prev.IOReadBytes, cur.IOReadBytes),
		IOWrite: rate(prev.IOWriteBytes, cur.IOWriteBytes),
		HTTPReq: rate(prev.HTTPRequests, cur.HTTPRequests),
		HTTPIn:  rate(prev.HTTPBytesIn, cur.HTTPBytesIn),
		HTTPOut: rate(prev.HTTPBytesOut, cur.HTTPBytesOut),
	}

	if cur.MemoryKnown {
		p.Mem, p.Cache = cur.MemoryBytes, cur.CacheBytes
	}

	return p, true
}

// siteBucket averages the points of one site in one tier step.
type siteBucket struct {
	start time.Time
	n     int
	sum   store.SiteSample
	memF  float64
	cache float64
	tasks float64
}

func (b *siteBucket) add(p store.SiteSample) {
	if b.n == 0 {
		b.sum, b.memF, b.cache, b.tasks = store.SiteSample{Site: p.Site}, 0, 0, 0
	}

	b.n++
	b.sum.CPU += p.CPU
	b.sum.IORead += p.IORead
	b.sum.IOWrite += p.IOWrite
	b.sum.HTTPReq += p.HTTPReq
	b.sum.HTTPIn += p.HTTPIn
	b.sum.HTTPOut += p.HTTPOut
	b.memF += float64(p.Mem)
	b.cache += float64(p.Cache)
	b.tasks += float64(p.Tasks)
}

func (b *siteBucket) avg() store.SiteSample {
	n := float64(max(b.n, 1))

	return store.SiteSample{
		Site: b.sum.Site, At: b.start,
		CPU: b.sum.CPU / n, Mem: uint64(b.memF / n), Cache: uint64(b.cache / n), Tasks: uint64(b.tasks/n + 0.5),
		IORead: b.sum.IORead / n, IOWrite: b.sum.IOWrite / n,
		HTTPReq: b.sum.HTTPReq / n, HTTPIn: b.sum.HTTPIn / n, HTTPOut: b.sum.HTTPOut / n,
	}
}

// feedSite adds p to its site's bucket of a tier and returns the points to
// store, as feed does for the host.
func feedSite(buckets map[string]*siteBucket, tier int, p store.SiteSample) []store.SiteSample {
	step := time.Duration(tier) * time.Second
	start := p.At.Truncate(step)

	b := buckets[p.Site]
	if b == nil {
		b = &siteBucket{}
		buckets[p.Site] = b
	}

	var out []store.SiteSample

	if b.n > 0 && !start.Equal(b.start) {
		out = append(out, b.avg())
		b.n = 0
	}

	if b.n == 0 {
		b.start = start
	}

	b.add(p)

	if b.n%4 == 0 {
		out = append(out, b.avg())
	}

	return out
}

// SampleSites asks the agent for one reading of every site and feeds the
// tiers. Without SiteAgent or SiteStore it does nothing.
func (m *Monitor) SampleSites(ctx context.Context) {
	if m.SiteAgent == nil || m.SiteStore == nil {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, SampleEvery)
	defer cancel()

	res, err := m.SiteAgent.SitesStats(ctx)
	at := m.now()

	if err != nil {
		if !errors.Is(err, context.Canceled) {
			m.log().WarnContext(ctx, "servermon: site stats", "err", err)
		}

		return
	}

	m.mu.Lock()

	s := &m.sites
	if s.ring == nil {
		s.ring, s.b5, s.b1 = map[string][]store.SiteSample{}, map[string]*siteBucket{}, map[string]*siteBucket{}
	}

	cur := make(map[string]agentapi.SiteUsage, len(res.Sites))

	var flush5, flush1 []store.SiteSample

	for _, u := range res.Sites {
		cur[u.Name] = u

		prev, ok := s.prev[u.Name]
		if !ok {
			continue
		}

		p, ok := DeriveSite(s.prevAt, prev, at, u)
		if !ok {
			continue
		}

		r := s.ring[u.Name]

		r = append(r, p)
		if len(r) > RingSize {
			r = append(r[:0], r[len(r)-RingSize:]...)
		}

		s.ring[u.Name] = r
		flush5 = append(flush5, feedSite(s.b5, store.HostTier5m, p)...)
		flush1 = append(flush1, feedSite(s.b1, store.HostTier1h, p)...)
	}

	// A site the agent no longer lists is gone: forget its live state.
	for name := range s.ring {
		if _, ok := cur[name]; !ok {
			delete(s.ring, name)
			delete(s.b5, name)
			delete(s.b1, name)
		}
	}

	s.prev, s.prevAt = cur, at
	m.mu.Unlock()

	if err := m.SiteStore.PutSiteSamples(ctx, store.HostTier5m, flush5); err != nil {
		m.log().WarnContext(ctx, "servermon: store site samples", "tier", store.HostTier5m, "err", err)
	}

	if err := m.SiteStore.PutSiteSamples(ctx, store.HostTier1h, flush1); err != nil {
		m.log().WarnContext(ctx, "servermon: store site samples", "tier", store.HostTier1h, "err", err)
	}
}

// pruneSites drops old site samples.
func (m *Monitor) pruneSites(ctx context.Context, now time.Time) {
	if m.SiteStore == nil {
		return
	}

	if err := m.SiteStore.PruneSiteSamples(ctx, store.HostTier5m, now.Add(-Keep5m)); err != nil {
		m.log().WarnContext(ctx, "servermon: prune site samples", "err", err)
	}

	if err := m.SiteStore.PruneSiteSamples(ctx, store.HostTier1h, now.Add(-Keep1h)); err != nil {
		m.log().WarnContext(ctx, "servermon: prune site samples", "err", err)
	}
}

// rangeTier is the tier and the start of a range other than Range1h.
func rangeTier(rng string, now time.Time) (int, time.Time, error) {
	switch rng {
	case Range24h:
		return store.HostTier5m, now.Add(-24 * time.Hour), nil
	case Range7d:
		return store.HostTier1h, now.Add(-7 * 24 * time.Hour), nil
	case Range30d:
		return store.HostTier1h, now.Add(-30 * 24 * time.Hour), nil
	}

	return 0, time.Time{}, ErrRange
}

// SiteSeries returns a site's points of a range, oldest first, and the step
// between them in seconds.
func (m *Monitor) SiteSeries(ctx context.Context, site, rng string) ([]store.SiteSample, int, error) {
	now := m.now()

	if rng == Range1h {
		m.mu.Lock()
		defer m.mu.Unlock()

		out := []store.SiteSample{}

		for _, p := range m.sites.ring[site] {
			if !p.At.Before(now.Add(-time.Hour)) {
				out = append(out, p)
			}
		}

		return out, int(SampleEvery / time.Second), nil
	}

	tier, since, err := rangeTier(rng, now)
	if err != nil {
		return nil, 0, err
	}

	if m.SiteStore == nil {
		return []store.SiteSample{}, tier, nil
	}

	s, err := m.SiteStore.SiteSamples(ctx, tier, site, since)

	return s, tier, err
}

// Sort keys of SiteRanking.
const (
	SortCPU     = "cpu"
	SortMemory  = "memory"
	SortTraffic = "traffic"
	SortIO      = "io"
)

// ErrSort is returned for a sort key that is not one of Sort*.
var ErrSort = errors.New("servermon: unknown sort")

// SiteRanking sums up every site over a range, the biggest first by sort.
func (m *Monitor) SiteRanking(ctx context.Context, rng, by string) ([]store.SiteTotal, error) {
	key, ok := map[string]func(store.SiteTotal) float64{
		SortCPU:     func(t store.SiteTotal) float64 { return t.CPUAvg },
		SortMemory:  func(t store.SiteTotal) float64 { return float64(t.MemAvg) },
		SortTraffic: func(t store.SiteTotal) float64 { return t.HTTPInBytes + t.HTTPOutBytes },
		SortIO:      func(t store.SiteTotal) float64 { return t.IOReadBytes + t.IOWriteBytes },
	}[by]
	if !ok {
		return nil, ErrSort
	}

	var (
		out []store.SiteTotal
		err error
	)

	now := m.now()

	if rng == Range1h {
		out = m.ringTotals(now.Add(-time.Hour))
	} else {
		tier, since, rerr := rangeTier(rng, now)
		if rerr != nil {
			return nil, rerr
		}

		if m.SiteStore == nil {
			return []store.SiteTotal{}, nil
		}

		if out, err = m.SiteStore.SiteTotals(ctx, tier, since); err != nil {
			return nil, err
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if a, b := key(out[i]), key(out[j]); a != b {
			return a > b
		}

		return out[i].Site < out[j].Site
	})

	return out, nil
}

// ringTotals sums up the 15 s points since t.
func (m *Monitor) ringTotals(since time.Time) []store.SiteTotal {
	m.mu.Lock()
	defer m.mu.Unlock()

	step := SampleEvery.Seconds()
	out := make([]store.SiteTotal, 0, len(m.sites.ring))

	for name, ring := range m.sites.ring {
		t := store.SiteTotal{Site: name}

		var (
			n   int
			mem float64
		)

		for _, p := range ring {
			if p.At.Before(since) {
				continue
			}

			n++
			t.CPUAvg += p.CPU
			t.CPUMax = max(t.CPUMax, p.CPU)
			mem += float64(p.Mem)
			t.MemMax = max(t.MemMax, p.Mem)
			t.IOReadBytes += p.IORead * step
			t.IOWriteBytes += p.IOWrite * step
			t.HTTPRequests += p.HTTPReq * step
			t.HTTPInBytes += p.HTTPIn * step
			t.HTTPOutBytes += p.HTTPOut * step
		}

		if n == 0 {
			continue
		}

		t.CPUAvg /= float64(n)
		t.MemAvg = uint64(mem / float64(n))
		out = append(out, t)
	}

	return out
}
