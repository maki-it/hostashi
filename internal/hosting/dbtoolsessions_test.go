// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestLocalDBToolSessions(t *testing.T) {
	s, _, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_wp"}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	user := "hostashi_tool_0123456789abcdef"
	tool := &fakeDBTool{res: agentapi.DBToolOpenResponse{Ticket: "0123456789abcdef0123456789abcdef", User: user, Expires: now.Add(30 * time.Minute)}}

	// No tool: nothing to list.
	if _, err := s.DBToolSessionsOf(ctx, "shop_wp"); !errors.Is(err, ErrNoDBTool) {
		t.Fatalf("no tool: %v", err)
	}

	if err := s.EnableDBTool(tool, 8444); err != nil {
		t.Fatal(err)
	}

	if err := s.EnablePanel("panel.example.com"); err != nil {
		t.Fatal(err)
	}

	if err := s.savePanelTLS(ctx, PanelTLS{Domains: []string{"panel.example.com"}, NotAfter: now.Add(30 * 24 * time.Hour), Live: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); err != nil {
		t.Fatal(err)
	}

	list, err := s.DBToolSessionsOf(ctx, "shop_wp")
	if err != nil || len(list) != 1 {
		t.Fatalf("sessions %+v %v", list, err)
	}

	if l := list[0]; l.ID != user || l.OpenedBy != "kim" || !l.Opened.Equal(now) || !l.Expires.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("session %+v", l)
	}

	if _, err := s.DBToolSessionsOf(ctx, "other"); !errors.Is(err, ErrDatabaseNotFound) {
		t.Fatalf("unknown database: %v", err)
	}

	// Extending adds 30 minutes, up to two hours from now.
	for _, want := range []time.Duration{60 * time.Minute, 90 * time.Minute, 120 * time.Minute} {
		v, err := s.ExtendDBToolSession(ctx, 1, "shop_wp", user)
		if err != nil || !v.Expires.Equal(now.Add(want)) {
			t.Fatalf("extend to %s: %+v %v", want, v, err)
		}
	}

	if _, err := s.ExtendDBToolSession(ctx, 1, "shop_wp", user); !errors.Is(err, ErrToolSessionLimit) {
		t.Fatalf("past the limit: %v", err)
	}

	// Later, the limit moves along with the clock.
	now = now.Add(10 * time.Minute)

	if v, err := s.ExtendDBToolSession(ctx, 1, "shop_wp", user); err != nil || !v.Expires.Equal(now.Add(agentapi.DBToolMaxRemaining)) {
		t.Fatalf("extend later: %+v %v", v, err)
	}

	if len(tool.extends) != 4 || tool.extends[3].Remote {
		t.Fatalf("agent extends %+v", tool.extends)
	}

	// Another database's session is not reachable through this one.
	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_two"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ExtendDBToolSession(ctx, 1, "shop_two", user); !errors.Is(err, ErrToolSessionNotFound) {
		t.Fatalf("other database: %v", err)
	}

	if err := s.EndDBToolSession(ctx, 1, "shop_two", user); !errors.Is(err, ErrToolSessionNotFound) {
		t.Fatalf("end through another database: %v", err)
	}

	if err := s.EndDBToolSession(ctx, 1, "shop_wp", user); err != nil {
		t.Fatal(err)
	}

	if len(tool.ends) != 1 || tool.ends[0] != (agentapi.DBToolEndRequest{User: user}) {
		t.Fatalf("agent ends %+v", tool.ends)
	}

	if list, _ := s.DBToolSessionsOf(ctx, "shop_wp"); len(list) != 0 {
		t.Fatalf("after end %+v", list)
	}

	if err := s.EndDBToolSession(ctx, 1, "shop_wp", user); !errors.Is(err, ErrToolSessionNotFound) {
		t.Fatalf("end twice: %v", err)
	}

	for _, action := range []string{"database.tool.extend", "database.tool.end"} {
		var n int
		if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action = ? AND target = 'shop_wp'`, action, &n); err != nil || n == 0 {
			t.Errorf("audit %s: %d %v", action, n, err)
		}
	}

	// A session the agent no longer knows (it restarted) drops out of the
	// list.
	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); err != nil {
		t.Fatal(err)
	}

	delete(tool.local, user)

	if list, _ := s.DBToolSessionsOf(ctx, "shop_wp"); len(list) != 0 {
		t.Fatalf("ghost session %+v", list)
	}

	if _, err := s.ExtendDBToolSession(ctx, 1, "shop_wp", user); !errors.Is(err, ErrToolSessionNotFound) {
		t.Fatalf("extend a gone session: %v", err)
	}
}

func TestRemoteDBToolSessions(t *testing.T) {
	s, tool, f, _, now := remoteToolSetup(t)
	ctx := t.Context()

	// The fake agent's answers use the real clock.
	*now = time.Now().UTC().Truncate(time.Second)

	var scheduled []time.Duration

	s.afterFunc = func(d time.Duration, _ func()) { scheduled = append(scheduled, d) }

	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); err != nil {
		t.Fatal(err)
	}

	users := toolUsers(f)
	if len(users) != 1 {
		t.Fatalf("users %v", users)
	}

	user := users[0]

	list, err := s.DBToolSessionsOf(ctx, "shop_wp")
	if err != nil || len(list) != 1 || list[0].ID != user {
		t.Fatalf("sessions %+v %v", list, err)
	}

	v, err := s.ExtendDBToolSession(ctx, 1, "shop_wp", user)
	if err != nil {
		t.Fatal(err)
	}

	if len(tool.extends) != 1 || !tool.extends[0].Remote || !tool.extends[0].Expires.Equal(v.Expires) {
		t.Fatalf("agent extends %+v", tool.extends)
	}

	// The panel keeps the user on the server until the new end.
	rt, err := s.st.DBRemoteTools(ctx)
	if err != nil || len(rt) != 1 || !rt[0].Expires.Equal(v.Expires.Add(remoteToolMargin)) {
		t.Fatalf("remote tools %+v %v", rt, err)
	}

	if len(scheduled) != 2 || scheduled[1] < 50*time.Minute {
		t.Fatalf("scheduled %v", scheduled)
	}

	if err := s.EndDBToolSession(ctx, 1, "shop_wp", user); err != nil {
		t.Fatal(err)
	}

	if len(tool.ends) != 1 || !tool.ends[0].Remote {
		t.Fatalf("agent ends %+v", tool.ends)
	}

	if slices.Contains(toolUsers(f), user) {
		t.Fatal("user kept on the server")
	}

	if rt, _ := s.st.DBRemoteTools(ctx); len(rt) != 0 {
		t.Fatalf("remote tools after end %+v", rt)
	}
}
