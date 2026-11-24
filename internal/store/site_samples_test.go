// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"math"
	"testing"
	"time"
)

func TestSiteSamples(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	base := time.Unix(2_000_000, 0).UTC()

	for _, name := range []string{"shop", "blog"} {
		if _, err := s.CreateSite(ctx, name, "default", "", []string{name + ".example.com"}, base); err != nil {
			t.Fatal(err)
		}
	}

	samples := []SiteSample{
		{Site: "shop", At: base, CPU: 50, Mem: 100, Cache: 10, Tasks: 3, IORead: 1, IOWrite: 2, HTTPReq: 0.5, HTTPIn: 10, HTTPOut: 100},
		{Site: "blog", At: base, CPU: 5, Mem: 40},
		{Site: "gone", At: base, CPU: 99}, // not in the store: skipped
	}
	if err := s.PutSiteSamples(ctx, HostTier5m, samples); err != nil {
		t.Fatal(err)
	}

	// Same time again replaces; NaN and negative values become 0.
	later := []SiteSample{
		{Site: "shop", At: base.Add(5 * time.Minute), CPU: 150, Mem: 300, IORead: math.NaN(), HTTPReq: 1.5, HTTPOut: -1},
		{Site: "blog", At: base, CPU: 7, Mem: 40},
	}
	if err := s.PutSiteSamples(ctx, HostTier5m, later); err != nil {
		t.Fatal(err)
	}

	got, err := s.SiteSamples(ctx, HostTier5m, "shop", base)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}

	if got[0] != samples[0] || got[1].CPU != 150 || got[1].IORead != 0 || got[1].HTTPOut != 0 {
		t.Fatalf("%+v", got)
	}

	if got, err := s.SiteSamples(ctx, HostTier5m, "shop", base.Add(time.Minute)); err != nil || len(got) != 1 {
		t.Fatalf("since %+v %v", got, err)
	}

	if got, err := s.SiteSamples(ctx, HostTier1h, "shop", base); err != nil || len(got) != 0 {
		t.Fatalf("other tier %+v %v", got, err)
	}

	tot, err := s.SiteTotals(ctx, HostTier5m, base)
	if err != nil || len(tot) != 2 {
		t.Fatalf("%+v %v", tot, err)
	}

	if b := tot[0]; b.Site != "blog" || b.CPUAvg != 7 || b.MemMax != 40 {
		t.Fatalf("blog %+v", b)
	}

	want := SiteTotal{Site: "shop", CPUAvg: 100, CPUMax: 150, MemAvg: 200, MemMax: 300, IOReadBytes: 300, IOWriteBytes: 600, HTTPRequests: 600, HTTPInBytes: 3000, HTTPOutBytes: 30000}
	if tot[1] != want {
		t.Fatalf("shop %+v, want %+v", tot[1], want)
	}

	if err := s.PruneSiteSamples(ctx, HostTier5m, base.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.SiteSamples(ctx, HostTier5m, "shop", time.Time{}); len(got) != 1 {
		t.Fatalf("after prune %+v", got)
	}

	// Samples go with the site.
	if err := s.DeleteSite(ctx, "shop"); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM site_samples`).Scan(&n); err != nil || n != 0 { // blog's only row was pruned above
		t.Fatalf("rows after delete %d %v", n, err)
	}

	if err := s.PutSiteSamples(ctx, 60, samples); err == nil {
		t.Fatal("want error for an unknown tier")
	}
}
