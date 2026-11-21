// SPDX-License-Identifier: AGPL-3.0-or-later

package servermon

import (
	"context"
	"errors"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// maxGap is the longest time between two raw samples that still gives a
// rate; after a longer pause (panel or agent down) the next sample only
// becomes the new base.
const maxGap = 5 * time.Minute

// Derive turns two raw samples into one dashboard point. ok is false when
// no rate can be computed (first sample, clock went back, too long a gap).
func Derive(prevAt time.Time, prev agentapi.HostStats, at time.Time, cur agentapi.HostStats) (store.HostSample, bool) {
	dt := at.Sub(prevAt).Seconds()
	if dt <= 0 || at.Sub(prevAt) > maxGap {
		return store.HostSample{}, false
	}

	h := store.HostSample{
		At: at, Load1: cur.Load1,
		MemTotal: cur.Memory.TotalBytes, MemUsed: sub(cur.Memory.TotalBytes, cur.Memory.AvailableBytes),
		SwapTotal: cur.Memory.SwapTotalBytes, SwapUsed: sub(cur.Memory.SwapTotalBytes, cur.Memory.SwapFreeBytes),
		Disks: make([]store.HostDiskSample, 0, len(cur.Disks)),
		Net:   []store.HostNetSample{},
	}

	if total := float64(sub(cur.CPU.Total(), prev.CPU.Total())); total > 0 && cur.CPU.Total() >= prev.CPU.Total() {
		idle := float64(sub(cur.CPU.Idle, prev.CPU.Idle))
		iowait := float64(sub(cur.CPU.IOWait, prev.CPU.IOWait))
		steal := float64(sub(cur.CPU.Steal, prev.CPU.Steal))
		h.CPUBusy = clampPct(100 * (total - idle - iowait) / total)
		h.CPUIOWait = clampPct(100 * iowait / total)
		h.CPUSteal = clampPct(100 * steal / total)
	}

	for _, d := range cur.Disks {
		h.Disks = append(h.Disks, store.HostDiskSample{Name: d.Name, Total: d.TotalBytes, Used: sub(d.TotalBytes, d.FreeBytes)})
	}

	before := make(map[string]agentapi.HostNet, len(prev.Net))
	for _, n := range prev.Net {
		before[n.Name] = n
	}

	for _, n := range cur.Net {
		p, ok := before[n.Name]
		if !ok || n.RxBytes < p.RxBytes || n.TxBytes < p.TxBytes {
			continue // new interface or counter reset: no rate this time
		}

		h.Net = append(h.Net, store.HostNetSample{
			Name: n.Name, Rx: float64(n.RxBytes-p.RxBytes) / dt, Tx: float64(n.TxBytes-p.TxBytes) / dt,
		})
	}

	return h, true
}

func sub(a, b uint64) uint64 {
	if b > a {
		return 0
	}

	return a - b
}

func clampPct(v float64) float64 { return min(max(v, 0), 100) }

// bucket averages the points of one tier step.
type bucket struct {
	start time.Time
	n     int
	sum   store.HostSample
	last  store.HostSample // disks and memory totals come from the last point
	nets  map[string][2]float64
	order []string
}

func (b *bucket) add(h store.HostSample) {
	if b.n == 0 {
		b.nets, b.order = map[string][2]float64{}, nil
		b.sum = store.HostSample{}
	}

	b.n++
	b.sum.CPUBusy += h.CPUBusy
	b.sum.CPUIOWait += h.CPUIOWait
	b.sum.CPUSteal += h.CPUSteal
	b.sum.Load1 += h.Load1
	b.sum.MemUsed += h.MemUsed
	b.sum.SwapUsed += h.SwapUsed
	b.last = h

	for _, n := range h.Net {
		v, ok := b.nets[n.Name]
		if !ok {
			b.order = append(b.order, n.Name)
		}

		b.nets[n.Name] = [2]float64{v[0] + n.Rx, v[1] + n.Tx}
	}
}

// avg is the bucket's point, stamped with the bucket's start. Network
// rates are averaged over all points, so a missing point counts as zero.
func (b *bucket) avg() store.HostSample {
	n := float64(b.n)
	cnt := uint64(max(b.n, 1))
	out := store.HostSample{
		At: b.start, CPUBusy: b.sum.CPUBusy / n, CPUIOWait: b.sum.CPUIOWait / n, CPUSteal: b.sum.CPUSteal / n,
		Load1: b.sum.Load1 / n, MemUsed: b.sum.MemUsed / cnt, MemTotal: b.last.MemTotal,
		SwapUsed: b.sum.SwapUsed / cnt, SwapTotal: b.last.SwapTotal, Disks: b.last.Disks,
		Net: make([]store.HostNetSample, 0, len(b.order)),
	}

	for _, name := range b.order {
		v := b.nets[name]
		out.Net = append(out.Net, store.HostNetSample{Name: name, Rx: v[0] / n, Tx: v[1] / n})
	}

	return out
}

// Sample asks the agent for one raw sample and feeds the tiers.
func (m *Monitor) Sample(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, SampleEvery)
	defer cancel()

	s, err := m.Agent.HostStats(ctx)
	at := m.now()

	if err != nil {
		if !errors.Is(err, context.Canceled) {
			m.log().WarnContext(ctx, "servermon: host stats", "err", err)
		}

		return
	}

	m.mu.Lock()
	prev := m.prev
	m.prev = &raw{at: at, s: s}

	var (
		h  store.HostSample
		ok bool
	)

	if prev != nil {
		h, ok = Derive(prev.at, prev.s, at, s)
	}

	var flush []tierPoint

	if ok {
		m.ring = append(m.ring, h)
		if len(m.ring) > RingSize {
			m.ring = append(m.ring[:0], m.ring[len(m.ring)-RingSize:]...)
		}

		flush = append(flush, feed(&m.b5, store.HostTier5m, h)...)
		flush = append(flush, feed(&m.b1, store.HostTier1h, h)...)
	}
	m.mu.Unlock()

	for _, p := range flush {
		if err := m.Store.PutHostSample(ctx, p.tier, p.h); err != nil {
			m.log().WarnContext(ctx, "servermon: store sample", "tier", p.tier, "err", err)
		}
	}
}

type tierPoint struct {
	tier int
	h    store.HostSample
}

// feed adds h to the bucket of a tier; a point of a later step closes the
// bucket and returns its average to store. The running bucket is also
// returned (and stored again later), so a panel restart loses at most one
// sample interval of a tier.
func feed(b *bucket, tier int, h store.HostSample) []tierPoint {
	step := time.Duration(tier) * time.Second
	start := h.At.Truncate(step)

	var out []tierPoint

	if b.n > 0 && !start.Equal(b.start) {
		out = append(out, tierPoint{tier, b.avg()})
		b.n = 0
	}

	if b.n == 0 {
		b.start = start
	}

	b.add(h)

	// Keep the open bucket up to date in SQLite as well, at most once a
	// minute is enough [E]; here it is written whenever it holds a multiple
	// of four points (one minute of 15 s samples).
	if b.n%4 == 0 {
		out = append(out, tierPoint{tier, b.avg()})
	}

	return out
}

// Ranges of the dashboard.
const (
	Range1h  = "1h"
	Range24h = "24h"
	Range7d  = "7d"
	Range30d = "30d"
)

// Series returns the points of a range, oldest first, and the step
// between them in seconds.
func (m *Monitor) Series(ctx context.Context, rng string) ([]store.HostSample, int, error) {
	now := m.now()

	switch rng {
	case Range1h:
		m.mu.Lock()
		defer m.mu.Unlock()

		out := make([]store.HostSample, 0, len(m.ring))
		for _, h := range m.ring {
			if !h.At.Before(now.Add(-time.Hour)) {
				out = append(out, h)
			}
		}

		return out, int(SampleEvery / time.Second), nil
	case Range24h:
		s, err := m.Store.HostSamples(ctx, store.HostTier5m, now.Add(-24*time.Hour))

		return s, store.HostTier5m, err
	case Range7d:
		s, err := m.Store.HostSamples(ctx, store.HostTier1h, now.Add(-7*24*time.Hour))

		return s, store.HostTier1h, err
	case Range30d:
		s, err := m.Store.HostSamples(ctx, store.HostTier1h, now.Add(-30*24*time.Hour))

		return s, store.HostTier1h, err
	}

	return nil, 0, ErrRange
}

// ErrRange is returned for a range that is not one of Range*.
var ErrRange = errors.New("servermon: unknown range")
