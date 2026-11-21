// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func auditFixture(t *testing.T) *Store {
	t.Helper()

	s := open(t, dbPath(t))
	ctx := context.Background()

	u, err := s.CreateFirstUser(ctx, "kim", "hash")
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range []struct {
		at, action, target, detail string
		user                       bool
	}{
		{"2026-11-01T10:00:00.000Z", "login", "kim", `{"ip":"192.0.2.1"}`, true},
		{"2026-11-02T10:00:00.000Z", "site.create", "Shop", `{}`, true},
		{"2026-11-03T10:00:00.000Z", "site.cert.upload", "shop", `{"source":"own"}`, true},
		{"2026-11-04T10:00:00.000Z", "site.certificate", "blog", `{}`, true},
		{"2026-11-05T10:00:00.000Z", "login.failed", "a%b_c", `{"ip":"192.0.2.9"}`, false},
		{"2026-11-06T10:00:00.000Z", "sitex.create", "abc", `{}`, true},
	} {
		var uid any
		if r.user {
			uid = u.ID
		}

		if err := s.RawExec(ctx, `INSERT INTO audit_log (at, user_id, actor, action, target, detail)
			VALUES (?, ?, coalesce((SELECT username FROM users WHERE id = ?), ''), ?, ?, ?)`,
			r.at, uid, uid, r.action, r.target, r.detail); err != nil {
			t.Fatal(err)
		}
	}

	return s
}

func auditIDs(es []AuditEntry) []int64 {
	out := make([]int64, 0, len(es))
	for _, e := range es {
		out = append(out, e.ID)
	}

	return out
}

func TestAuditEntriesFilter(t *testing.T) {
	s := auditFixture(t)
	ctx := context.Background()

	day := func(d int) time.Time { return time.Date(2026, 11, d, 0, 0, 0, 0, time.UTC) }

	for name, tc := range map[string]struct {
		f    AuditFilter
		want []int64
	}{
		"all newest first": {AuditFilter{}, []int64{6, 5, 4, 3, 2, 1}},
		// "site" matches site.* but neither sitex.create nor a longer word.
		"action prefix":      {AuditFilter{Action: "site"}, []int64{4, 3, 2}},
		"action exact":       {AuditFilter{Action: "site.cert"}, []int64{3}},
		"action no wildcard": {AuditFilter{Action: "site_"}, nil},
		"action leaf":        {AuditFilter{Action: "login"}, []int64{5, 1}},
		"actor":              {AuditFilter{Actor: "kim"}, []int64{6, 4, 3, 2, 1}},
		"actor none":         {AuditFilter{Actor: "Kim"}, nil},
		"target case":        {AuditFilter{Target: "SHOP"}, []int64{3, 2}},
		"target wildcards":   {AuditFilter{Target: "%"}, []int64{5}},
		"target underscore":  {AuditFilter{Target: "b_c"}, []int64{5}},
		"target literal _":   {AuditFilter{Target: "_"}, []int64{5}},
		"since":              {AuditFilter{Since: day(5)}, []int64{6, 5}},
		"until exclusive":    {AuditFilter{Until: day(2).Add(10 * time.Hour)}, []int64{1}},
		"range":              {AuditFilter{Since: day(2), Until: day(4)}, []int64{3, 2}},
		"combined":           {AuditFilter{Action: "site", Target: "shop", Since: day(3)}, []int64{3}},
	} {
		t.Run(name, func(t *testing.T) {
			got, more, err := s.AuditEntries(ctx, tc.f, 0, 50)
			if err != nil {
				t.Fatal(err)
			}

			if more || !slices.Equal(auditIDs(got), tc.want) {
				t.Errorf("got %v more=%v, want %v", auditIDs(got), more, tc.want)
			}
		})
	}
}

func TestAuditEntriesPaging(t *testing.T) {
	s := auditFixture(t)
	ctx := context.Background()

	var (
		all    []int64
		before int64
	)

	for range 10 {
		page, more, err := s.AuditEntries(ctx, AuditFilter{}, before, 4)
		if err != nil {
			t.Fatal(err)
		}

		all = append(all, auditIDs(page)...)

		if !more {
			break
		}

		before = page[len(page)-1].ID
	}

	if !slices.Equal(all, []int64{6, 5, 4, 3, 2, 1}) {
		t.Fatalf("pages: %v", all)
	}

	// The page holds exactly the rest: no further page.
	page, more, err := s.AuditEntries(ctx, AuditFilter{}, 3, 2)
	if err != nil || more || !slices.Equal(auditIDs(page), []int64{2, 1}) {
		t.Fatalf("last page: %v %v %v", auditIDs(page), more, err)
	}

	if _, _, err := s.AuditEntries(ctx, AuditFilter{}, 0, 0); err == nil {
		t.Error("limit 0 accepted")
	}
}

func TestAuditEntryFields(t *testing.T) {
	s := auditFixture(t)
	ctx := context.Background()

	got, _, err := s.AuditEntries(ctx, AuditFilter{Action: "login"}, 0, 10)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}

	failed, login := got[0], got[1]
	if failed.UserID != nil || failed.Actor != "" || string(failed.Detail) != `{"ip":"192.0.2.9"}` {
		t.Errorf("failed login: %+v", failed)
	}

	if login.UserID == nil || *login.UserID != 1 || login.Actor != "kim" || login.Target != "kim" ||
		!login.At.Equal(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)) || login.At.Location() != time.UTC {
		t.Errorf("login: %+v", login)
	}

	// An entry written by Audit reads back with its time.
	if err := s.Audit(ctx, nil, "audit.export", "", map[string]string{"format": "csv"}); err != nil {
		t.Fatal(err)
	}

	got, _, err = s.AuditEntries(ctx, AuditFilter{Action: "audit"}, 0, 1)
	if err != nil || len(got) != 1 || time.Since(got[0].At) > time.Minute || string(got[0].Detail) != `{"format":"csv"}` {
		t.Fatalf("new entry: %+v %v", got, err)
	}
}

func TestEachAuditEntry(t *testing.T) {
	s := auditFixture(t)
	ctx := context.Background()

	var ids []int64

	err := s.EachAuditEntry(ctx, AuditFilter{Action: "site"}, func(e AuditEntry) error {
		ids = append(ids, e.ID)

		return nil
	})
	if err != nil || !slices.Equal(ids, []int64{2, 3, 4}) {
		t.Fatalf("oldest first: %v %v", ids, err)
	}

	stop := errors.New("stop")
	n := 0

	err = s.EachAuditEntry(ctx, AuditFilter{}, func(AuditEntry) error {
		n++

		return stop
	})
	if !errors.Is(err, stop) || n != 1 {
		t.Fatalf("stop: %v after %d", err, n)
	}
}

func TestAuditActions(t *testing.T) {
	s := auditFixture(t)

	got, err := s.AuditActions(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"login", "login.failed", "site.cert.upload", "site.certificate", "site.create", "sitex.create"}
	if !slices.Equal(got, want) {
		t.Errorf("got %v", got)
	}
}
