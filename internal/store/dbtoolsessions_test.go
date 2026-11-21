// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"errors"
	"testing"
	"time"
)

func TestDBToolSessions(t *testing.T) {
	s := open(t, dbPath(t))
	ctx := t.Context()

	u, err := s.CreateFirstUser(ctx, "kim", "x")
	if err != nil {
		t.Fatal(err)
	}

	opened := time.Unix(2_000_000, 0).UTC()
	a := DBToolSession{User: "hostashi_tool_0000000000000001", Database: "blog_db", OpenedBy: &u.ID, OpenedAt: opened, Expires: opened.Add(2 * time.Hour)}
	b := DBToolSession{User: "hostashi_tool_0000000000000002", Database: "blog_db", Remote: true, OpenedAt: opened, Expires: opened.Add(30 * time.Minute)}
	other := DBToolSession{User: "hostashi_tool_0000000000000003", Database: "shop_db", OpenedAt: opened, Expires: opened.Add(time.Hour)}

	for _, x := range []DBToolSession{a, b, other} {
		if err := s.AddDBToolSession(ctx, x); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.AddDBToolSession(ctx, DBToolSession{User: "root", Database: "blog_db", OpenedAt: opened, Expires: opened}); err == nil {
		t.Error("a user that is not the tool's accepted")
	}

	list, err := s.DBToolSessionsOf(ctx, "blog_db")
	if err != nil || len(list) != 2 {
		t.Fatalf("sessions = %+v, %v", list, err)
	}

	// The first to end first; the opener's name comes along.
	if list[0].User != b.User || !list[0].Remote || list[0].OpenedBy != nil || list[1].OpenedByName != "kim" || *list[1].OpenedBy != u.ID {
		t.Fatalf("sessions = %+v", list)
	}

	end := opened.Add(3 * time.Hour)
	if err := s.SetDBToolSessionExpires(ctx, a.User, end, end.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	got, err := s.DBToolSessionByUser(ctx, a.User)
	if err != nil || !got.Expires.Equal(end) || !got.OpenedAt.Equal(opened) {
		t.Fatalf("after extend = %+v, %v", got, err)
	}

	if err := s.SetDBToolSessionExpires(ctx, "hostashi_tool_ffffffffffffffff", end, end); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown session: %v", err)
	}

	if err := s.DeleteDBToolSession(ctx, a.User); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DBToolSessionByUser(ctx, a.User); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted session: %v", err)
	}
}
