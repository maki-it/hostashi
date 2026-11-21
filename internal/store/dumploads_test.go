// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestDumpLoads(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(1000, 0).UTC()

	if err := s.RawExec(ctx, `INSERT INTO users (id, username) VALUES (1, 'kim')`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DumpLoadsOf(ctx, "0123456789abcdef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("none yet: %v", err)
	}

	if err := s.WatchDumpLoads(ctx, "../etc/passwd!!", nil, now); err == nil {
		t.Fatal("bad import id accepted")
	}

	by := int64(1)
	if err := s.WatchDumpLoads(ctx, "0123456789abcdef", &by, now); err != nil {
		t.Fatal(err)
	}

	// A second watch keeps the first row.
	if err := s.WatchDumpLoads(ctx, "0123456789abcdef", nil, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	got, err := s.DumpLoadsOf(ctx, "0123456789abcdef")
	if err != nil || got.State != DumpLoadsWatching || got.StartedBy == nil || *got.StartedBy != 1 || !got.CreatedAt.Equal(now) || string(got.Loads) != "[]" {
		t.Fatalf("%+v %v", got, err)
	}

	w, err := s.WatchingDumpLoads(ctx)
	if err != nil || len(w) != 1 {
		t.Fatalf("watching %+v %v", w, err)
	}

	got.State, got.Loads = DumpLoadsLoaded, []byte(`[{"database":"a"}]`)
	if err := s.SaveDumpLoads(ctx, got); err != nil {
		t.Fatal(err)
	}

	// Stop only ends a wait, never a finished load.
	if err := s.StopDumpLoads(ctx, "0123456789abcdef"); err != nil {
		t.Fatal(err)
	}

	got, err = s.DumpLoadsOf(ctx, "0123456789abcdef")
	if err != nil || got.State != DumpLoadsLoaded || string(got.Loads) != `[{"database":"a"}]` {
		t.Fatalf("%+v %v", got, err)
	}

	if w, err := s.WatchingDumpLoads(ctx); err != nil || len(w) != 0 {
		t.Fatalf("watching %+v %v", w, err)
	}

	if err := s.SaveDumpLoads(ctx, DumpLoads{ImportID: "fedcba9876543210", State: DumpLoadsLoaded, CreatedAt: now, Loads: []byte("{")}); err == nil {
		t.Fatal("invalid JSON accepted")
	}

	// Deleting the user keeps the row without it.
	if err := s.RawExec(ctx, `DELETE FROM users WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	if got, err = s.DumpLoadsOf(ctx, "0123456789abcdef"); err != nil || got.StartedBy != nil {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestDumpLoadsPruned(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	base := time.Unix(1000, 0)

	for i := range MaxDumpLoads + 5 {
		if err := s.WatchDumpLoads(ctx, fmt.Sprintf("%016x", i), nil, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	w, err := s.WatchingDumpLoads(ctx)
	if err != nil || len(w) != MaxDumpLoads || w[0].ImportID != fmt.Sprintf("%016x", 5) {
		t.Fatalf("%d kept, first %v, %v", len(w), w, err)
	}
}
