// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"testing"
	"time"
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
