// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestReadPoolRefusesWrites(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()

	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('k', 'v')`); err == nil {
		t.Fatal("read pool accepted a write")
	}

	if _, ok, err := s.Setting(ctx, "k"); err != nil || ok {
		t.Fatalf("setting stored: %v %v", ok, err)
	}
}

func TestCopyToRestoresQueryOnly(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()

	// One read connection, so the copy and the write below share it.
	s.db.SetMaxOpenConns(1)

	if err := s.CopyTo(ctx, filepath.Join(t.TempDir(), "panel.db")); err != nil {
		t.Fatal(err)
	}

	if _, err := s.db.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('k', 'v')`); err == nil {
		t.Fatal("read connection writable after the copy")
	}
}

func TestParallelWritesSucceed(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	for i := range 8 {
		if _, err := s.CreateSite(ctx, "s"+strconv.Itoa(i), "default", "", []string{fmt.Sprintf("s%d.example.com", i)}, now); err != nil {
			t.Fatal(err)
		}
	}

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for g := range 32 {
		wg.Go(func() {
			for i := range 20 {
				name := "s" + strconv.Itoa((g+i)%8)

				_, err := s.SetSiteHSTS(ctx, name, i%2 == 0, now)
				if err == nil {
					err = s.Audit(ctx, nil, "test", name, nil)
				}

				if err != nil {
					mu.Lock()

					errs = append(errs, err)

					mu.Unlock()
				}
			}
		})
	}

	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := s.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action = ?`, "test", &n); err != nil || n != 32*20 {
		t.Fatalf("audit entries %d, %v", n, err)
	}
}

func TestWriterQueueHonorsContext(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()

	// Hold the only write connection.
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = tx.Rollback() }()

	short, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()

	start := time.Now()

	if err := s.SetSetting(short, "k", "v"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want deadline exceeded, got %v", err)
	}

	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("waited %s for a canceled write", d)
	}

	// Reads go on while the writer is busy.
	if _, _, err := s.Setting(ctx, "k"); err != nil {
		t.Fatal(err)
	}
}

func TestSitesListsDomainsPerSite(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()
	now := time.Unix(2_000_000, 0)

	if _, err := s.CreateSite(ctx, "bb", "default", "", []string{"b2.example.com", "b1.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CreateSite(ctx, "aa", "default", "", []string{"a.example.com"}, now); err != nil {
		t.Fatal(err)
	}

	got, err := s.Sites(ctx)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0].Name != "aa" || fmt.Sprint(got[0].Domains) != "[a.example.com]" ||
		got[1].Name != "bb" || fmt.Sprint(got[1].Domains) != "[b2.example.com b1.example.com]" {
		t.Fatalf("sites %+v", got)
	}
}
