// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"crypto/sha256"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The benchmarks and the load test below measure the panel database under
// the load of a single server (docs/sqlite-load.md). They are not part of the
// normal test run: benchmarks run with -bench, the load test only with
// HOSTASHI_SQLITE_LOAD set (a duration such as 10s).

// loadFixture is a seeded database and the keys to its rows.
type loadFixture struct {
	st       *Store
	path     string
	sites    []string
	sessions [][]byte
	userID   int64
	tokenID  int64
}

func openTB(tb testing.TB) (*Store, string) {
	tb.Helper()

	dir := filepath.Join(tb.TempDir(), "lib")
	if err := os.Mkdir(dir, 0o750); err != nil {
		tb.Fatal(err)
	}

	path := filepath.Join(dir, "hostashi.db")

	s, err := Open(context.Background(), path)
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() { _ = s.Close() })

	return s, path
}

// seedLoad creates n active sites with three domains, two cron jobs and one
// database each, one admin with 20 sessions and one API token, and 10 000
// audit entries.
func seedLoad(tb testing.TB, n int) loadFixture {
	tb.Helper()

	ctx := context.Background()
	st, path := openTB(tb)
	now := time.Now()

	u, err := st.CreateFirstUser(ctx, "admin", "x")
	if err != nil {
		tb.Fatal(err)
	}

	f := loadFixture{st: st, path: path, userID: u.ID}

	for i := range n {
		name := "site" + strconv.Itoa(i)
		domains := []string{name + ".example.com", "www." + name + ".example.com", name + ".example.org"}

		site, err := st.CreateSite(ctx, name, "default", "8.4", domains, now)
		if err != nil {
			tb.Fatal(err)
		}

		if err := st.SetSiteState(ctx, name, SiteActive, now); err != nil {
			tb.Fatal(err)
		}

		for j := range 2 {
			if _, err := st.InsertCronJob(ctx, CronJob{
				SiteID: site.ID, Schedule: "*/5 * * * *", Command: "php cron" + strconv.Itoa(j) + ".php",
			}, 50, now); err != nil {
				tb.Fatal(err)
			}
		}

		if _, err := st.CreateDatabase(ctx, name, "db_"+name, now); err != nil {
			tb.Fatal(err)
		}

		f.sites = append(f.sites, name)
	}

	for i := range 20 {
		h := sha256.Sum256([]byte("session" + strconv.Itoa(i)))
		if err := st.CreateSession(ctx, u.ID, h[:], now, now.Add(time.Hour)); err != nil {
			tb.Fatal(err)
		}

		f.sessions = append(f.sessions, h[:])
	}

	th := sha256.Sum256([]byte("token"))

	tok, err := st.CreateAPIToken(ctx, APIToken{UserID: u.ID, Name: "bench", Prefix: "hst_bench", Scopes: []string{"sites:read"}, CreatedAt: now}, th[:])
	if err != nil {
		tb.Fatal(err)
	}

	f.tokenID = tok.ID

	for i := range 10_000 {
		if err := st.Audit(ctx, &u.ID, "site.update", "site"+strconv.Itoa(i%max(n, 1)), map[string]string{"field": "php"}); err != nil {
			tb.Fatal(err)
		}
	}

	return f
}

var loadSizes = []int{22, 300, 1000}

// BenchmarkSessionUser is the lookup behind every panel request.
func BenchmarkSessionUser(b *testing.B) {
	f := seedLoad(b, 22)
	ctx := context.Background()

	var i atomic.Int64

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			h := f.sessions[int(i.Add(1))%len(f.sessions)]
			if _, err := f.st.SessionUser(ctx, h, time.Now()); err != nil {
				b.Error(err)

				return
			}
		}
	})
}

// BenchmarkSites lists every site with its domains (site list, metrics
// scrape, TLS worker).
func BenchmarkSites(b *testing.B) {
	for _, n := range loadSizes {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			f := seedLoad(b, n)
			ctx := context.Background()

			for b.Loop() {
				got, err := f.st.Sites(ctx)
				if err != nil || len(got) != n {
					b.Fatal(len(got), err)
				}
			}
		})
	}
}

// BenchmarkSiteByName is the lookup behind every per-site API call.
func BenchmarkSiteByName(b *testing.B) {
	f := seedLoad(b, 300)
	ctx := context.Background()

	var i atomic.Int64

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := f.st.SiteByName(ctx, f.sites[int(i.Add(1))%len(f.sites)]); err != nil {
				b.Error(err)

				return
			}
		}
	})
}

// BenchmarkAuditParallel appends audit entries from many goroutines, the
// write that every change makes.
func BenchmarkAuditParallel(b *testing.B) {
	f := seedLoad(b, 22)
	ctx := context.Background()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if err := f.st.Audit(ctx, &f.userID, "bench", "x", nil); err != nil {
				b.Error(err)

				return
			}
		}
	})
}

// BenchmarkSiteFlagParallel toggles a site flag in a transaction from many
// goroutines (BEGIN IMMEDIATE, read, write, commit).
func BenchmarkSiteFlagParallel(b *testing.B) {
	f := seedLoad(b, 300)
	ctx := context.Background()

	var i atomic.Int64

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			n := i.Add(1)
			if _, err := f.st.SetSiteHSTS(ctx, f.sites[int(n)%len(f.sites)], n%2 == 0, time.Now()); err != nil {
				b.Error(err)

				return
			}
		}
	})
}

// BenchmarkCopyTo is the hourly VACUUM INTO for the backup.
func BenchmarkCopyTo(b *testing.B) {
	f := seedLoad(b, 1000)
	ctx := context.Background()
	dst := filepath.Join(b.TempDir(), "panel.db")

	for b.Loop() {
		if err := f.st.CopyTo(ctx, dst); err != nil {
			b.Fatal(err)
		}
	}
}

// latencies collects durations per operation.
type latencies struct {
	mu   sync.Mutex
	ops  map[string][]time.Duration
	errs map[string]int
}

func (l *latencies) add(op string, d time.Duration, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err != nil {
		l.errs[op]++

		return
	}

	l.ops[op] = append(l.ops[op], d)
}

func pct(ds []time.Duration, p float64) time.Duration {
	if len(ds) == 0 {
		return 0
	}

	return ds[min(len(ds)-1, int(float64(len(ds))*p))]
}

// TestSQLiteLoad runs a mixed load for HOSTASHI_SQLITE_LOAD: panel and API
// readers, writers that change sites and append audit entries, a metrics
// scrape every second and a database copy every two seconds, all at once.
// It reports throughput and latency per operation and fails on any error.
func TestSQLiteLoad(t *testing.T) {
	v := os.Getenv("HOSTASHI_SQLITE_LOAD")
	if v == "" {
		t.Skip("set HOSTASHI_SQLITE_LOAD=10s to run")
	}

	dur, err := time.ParseDuration(v)
	if err != nil {
		t.Fatal(err)
	}

	sites := 300
	if s := os.Getenv("HOSTASHI_SQLITE_LOAD_SITES"); s != "" {
		if sites, err = strconv.Atoi(s); err != nil {
			t.Fatal(err)
		}
	}

	readers, writers := 32, 8

	f := seedLoad(t, sites)
	ctx, cancel := context.WithTimeout(context.Background(), dur)

	defer cancel()

	l := &latencies{ops: map[string][]time.Duration{}, errs: map[string]int{}}

	var wg sync.WaitGroup

	timed := func(op string, fn func() error) {
		start := time.Now()
		err := fn()

		if ctx.Err() != nil {
			return
		}

		l.add(op, time.Since(start), err)

		if err != nil {
			t.Errorf("%s: %v", op, err)
		}
	}

	for r := range readers {
		wg.Go(func() {
			for i := r; ctx.Err() == nil; i++ {
				timed("session", func() error {
					_, err := f.st.SessionUser(ctx, f.sessions[i%len(f.sessions)], time.Now())

					return err
				})
				timed("site", func() error {
					_, err := f.st.SiteByName(ctx, f.sites[i%len(f.sites)])

					return err
				})
				timed("cron.list", func() error {
					_, err := f.st.CronJobs(ctx, int64(i%len(f.sites)+1))

					return err
				})
			}
		})
	}

	for w := range writers {
		wg.Go(func() {
			for i := w; ctx.Err() == nil; i++ {
				site := f.sites[i%len(f.sites)]

				timed("site.flag", func() error {
					_, err := f.st.SetSiteHSTS(ctx, site, i%2 == 0, time.Now())

					return err
				})
				timed("audit", func() error {
					return f.st.Audit(ctx, &f.userID, "site.hsts", site, nil)
				})
				timed("token.touch", func() error {
					return f.st.TouchAPIToken(ctx, f.tokenID, time.Now().Add(time.Duration(i)*time.Minute), "192.0.2.1")
				})
			}
		})
	}

	periodic := func(op string, every time.Duration, fn func() error) {
		wg.Go(func() {
			tk := time.NewTicker(every)
			defer tk.Stop()

			for {
				timed(op, fn)

				select {
				case <-ctx.Done():
					return
				case <-tk.C:
				}
			}
		})
	}

	periodic("sites.list", time.Second, func() error {
		_, err := f.st.Sites(ctx)

		return err
	})

	dst := filepath.Join(t.TempDir(), "panel.db")

	periodic("copy", 2*time.Second, func() error { return f.st.CopyTo(ctx, dst) })

	wg.Wait()

	ops := make([]string, 0, len(l.ops))
	for op := range l.ops {
		ops = append(ops, op)
	}

	slices.Sort(ops)

	t.Logf("sites=%d readers=%d writers=%d duration=%s", sites, readers, writers, dur)
	t.Logf("%-12s %9s %9s %10s %10s %10s %10s", "op", "count", "ops/s", "p50", "p99", "max", "errors")

	for _, op := range ops {
		ds := l.ops[op]
		slices.Sort(ds)
		t.Logf("%-12s %9d %9.0f %10s %10s %10s %10d", op, len(ds), float64(len(ds))/dur.Seconds(),
			pct(ds, 0.50).Round(time.Microsecond), pct(ds, 0.99).Round(time.Microsecond),
			ds[len(ds)-1].Round(time.Microsecond), l.errs[op])
	}

	for _, p := range []string{f.path, f.path + "-wal"} {
		if fi, err := os.Stat(p); err == nil {
			t.Logf("%s: %d KiB", filepath.Base(p), fi.Size()/1024)
		}
	}

	for op, n := range l.errs {
		if n > 0 {
			t.Errorf("%s: %d errors", op, n)
		}
	}
}
