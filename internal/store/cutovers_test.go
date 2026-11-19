// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"
)

func TestCutovers(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(1000, 0).UTC()

	if _, err := s.CreateSite(ctx, "blog", "default", "", []string{"blog.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if err := s.StartCutover(ctx, Cutover{Site: "nope", StartedAt: now, Deadline: now}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	if _, err := s.CutoverBySite(ctx, "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("none yet: %v", err)
	}

	c := Cutover{Site: "blog", StartedAt: now, Deadline: now.Add(48 * time.Hour)}
	if err := s.StartCutover(ctx, c); err != nil {
		t.Fatal(err)
	}

	if err := s.StartCutover(ctx, c); !errors.Is(err, ErrCutoverRunning) {
		t.Fatalf("second start: %v", err)
	}

	got, err := s.CutoverBySite(ctx, "blog")
	if err != nil || got.State != CutoverWaiting || !got.Deadline.Equal(c.Deadline) || !got.CheckedAt.IsZero() || string(got.Answers) != "[]" || got.StartedBy != nil {
		t.Fatalf("%+v %v", got, err)
	}

	w, err := s.WaitingCutovers(ctx)
	if err != nil || len(w) != 1 || w[0].Site != "blog" {
		t.Fatalf("waiting %+v %v", w, err)
	}

	got.CheckedAt, got.Answers = now.Add(time.Minute), []byte(`[{"domain":"blog.example.com"}]`)
	if ok, err := s.UpdateCutover(ctx, got); !ok || err != nil {
		t.Fatalf("update: %v %v", ok, err)
	}

	got.State, got.DoneAt, got.Error = CutoverSwitched, now.Add(2*time.Minute), "x"
	if ok, err := s.UpdateCutover(ctx, got); !ok || err != nil {
		t.Fatalf("switch: %v %v", ok, err)
	}

	// A finished cut-over is not changed again.
	got.State = CutoverCanceled
	if ok, err := s.UpdateCutover(ctx, got); ok || err != nil {
		t.Fatalf("update after done: %v %v", ok, err)
	}

	got, err = s.CutoverBySite(ctx, "blog")
	if err != nil || got.State != CutoverSwitched || got.Error != "x" || !got.DoneAt.Equal(now.Add(2*time.Minute)) || string(got.Answers) != `[{"domain":"blog.example.com"}]` {
		t.Fatalf("after switch %+v %v", got, err)
	}

	if w, err := s.WaitingCutovers(ctx); err != nil || len(w) != 0 {
		t.Fatalf("waiting after switch %+v %v", w, err)
	}

	// Starting again replaces the finished one.
	if err := s.StartCutover(ctx, c); err != nil {
		t.Fatal(err)
	}

	if got, err := s.CutoverBySite(ctx, "blog"); err != nil || got.State != CutoverWaiting || got.Error != "" || !got.DoneAt.IsZero() {
		t.Fatalf("restart %+v %v", got, err)
	}

	if err := s.DeleteSite(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CutoverBySite(ctx, "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after site delete: %v", err)
	}
}
