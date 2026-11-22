// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

type fakeSizer struct {
	calls int
	sizes map[string]int64
	err   error
}

func (f *fakeSizer) DBSizes(context.Context) (map[string]int64, error) {
	f.calls++

	return f.sizes, f.err
}

func TestDatabaseSizes(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()

	if _, err := s.DatabaseSizes(ctx); !errors.Is(err, ErrNoDBSizes) {
		t.Fatalf("without agent: %v", err)
	}

	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }
	f := &fakeSizer{err: errDown}
	s.EnableDBSizes(f)

	if _, err := s.DatabaseSizes(ctx); !errors.Is(err, errDown) {
		t.Fatalf("agent down: %v", err)
	}

	// A failure is not cached.
	f.err, f.sizes = nil, map[string]int64{"shop_wp": 1 << 20}

	z, err := s.DatabaseSizes(ctx)
	if err != nil || z.Bytes["shop_wp"] != 1<<20 || !z.MeasuredAt.Equal(now) || f.calls != 2 {
		t.Fatalf("%+v %v calls=%d", z, err, f.calls)
	}

	// Within five minutes the measurement is reused.
	now = now.Add(4 * time.Minute)
	f.sizes = map[string]int64{"shop_wp": 2 << 20}

	if z, _ := s.DatabaseSizes(ctx); z.Bytes["shop_wp"] != 1<<20 || f.calls != 2 {
		t.Fatalf("cached: %+v calls=%d", z, f.calls)
	}

	now = now.Add(time.Minute)

	if z, _ := s.DatabaseSizes(ctx); z.Bytes["shop_wp"] != 2<<20 || f.calls != 3 {
		t.Fatalf("after five minutes: %+v calls=%d", z, f.calls)
	}

	// A clock that went back measures again.
	now = now.Add(-time.Hour)

	if _, _ = s.DatabaseSizes(ctx); f.calls != 4 {
		t.Fatalf("clock back: calls=%d", f.calls)
	}

	// No databases: an empty map, not nil.
	f.sizes = nil
	now = now.Add(time.Hour)

	if z, err := s.DatabaseSizes(ctx); err != nil || z.Bytes == nil {
		t.Fatalf("empty: %+v %v", z, err)
	}
}

func TestDatabaseDoctor(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()

	s.EnableLimits(newFakeLimits())

	if c, err := s.DatabaseDoctor(ctx, time.Now()); err != nil || c != nil {
		t.Fatalf("without agent: %v %v", c, err)
	}

	for _, name := range []string{"shop", "blog"} {
		if _, err := s.Create(ctx, 1, CreateRequest{Name: name, Domains: []string{name + ".example.com"}}); err != nil {
			t.Fatal(err)
		}
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_wp", User: "shop_wp"}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_log", User: "shop_log"}); err != nil {
		t.Fatal(err)
	}

	// The small preset warns from 80 % of 2 GiB; both databases count.
	f := &fakeSizer{sizes: map[string]int64{"shop_wp": 1 << 30, "shop_log": 700 << 20, "other": 5 << 30}}
	s.EnableDBSizes(f)

	checks, err := s.DatabaseDoctor(ctx, time.Now())
	if err != nil || len(checks) != 1 {
		t.Fatalf("%+v %v", checks, err)
	}

	c := checks[0]
	if c.ID != "site.database" || c.Site != "shop" || c.Status != "warn" || c.Summary != "databases: 1.7 GiB of 2.0 GiB (84 %)" {
		t.Fatalf("%+v", c)
	}

	// No MariaDB on the server: no checks, no error.
	s.dbSizes = dbSizeCache{}
	f.err = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "MariaDB is not available on this server"}

	if c, err := s.DatabaseDoctor(ctx, time.Now()); err != nil || c != nil {
		t.Fatalf("without MariaDB: %v %v", c, err)
	}

	// The agent is down: an error, so the health page says a check failed.
	f.err = errDown

	if _, err := s.DatabaseDoctor(ctx, time.Now()); !errors.Is(err, errDown) {
		t.Fatalf("agent down: %v", err)
	}
}
