// SPDX-License-Identifier: AGPL-3.0-or-later

package mariadb

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// fakeTimers collects the session timers instead of starting them.
type fakeTimers struct {
	fns     []func()
	stopped int
}

func (f *fakeTimers) after(_ time.Duration, fn func()) func() bool {
	f.fns = append(f.fns, fn)

	return func() bool { f.stopped++; return true }
}

func toolManager(t *testing.T) (*Manager, *fakeDB, *fakeTimers) {
	t.Helper()

	db := &fakeDB{}
	m := newManager(t, stateDir(t), db)
	ft := &fakeTimers{}
	m.afterFunc = ft.after
	now := time.Date(2026, 11, 21, 1, 0, 0, 0, time.UTC)
	m.clock = func() time.Time { return now }

	if _, err := m.Create(t.Context(), "shop", "shop_wp"); err != nil {
		t.Fatal(err)
	}

	db.stmts = nil

	return m, db, ft
}

func TestOpenTool(t *testing.T) {
	m, db, ft := toolManager(t)

	l, err := m.OpenTool(t.Context(), "shop_wp", 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	if !agentapi.ValidDBToolUser(l.User) || l.Database != "shop_wp" {
		t.Fatalf("login %+v", l)
	}

	if err := agentapi.ValidateDBPassword(l.Password); err != nil {
		t.Fatal(err)
	}

	if want := time.Date(2026, 11, 21, 1, 30, 0, 0, time.UTC); !l.Expires.Equal(want) {
		t.Fatalf("expires %s", l.Expires)
	}

	acct := "'" + l.User + "'@'localhost'"
	want := []string{
		"CREATE USER " + acct + " IDENTIFIED BY '" + l.Password + "' WITH MAX_USER_CONNECTIONS 2",
		"GRANT ALL PRIVILEGES ON `shop\\_wp`.* TO " + acct,
	}

	if strings.Join(db.stmts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("statements\n%s", strings.Join(db.stmts, "\n"))
	}

	if s := m.ToolSessions(); len(s) != 1 || s[0].User != l.User || s[0].Database != "shop_wp" {
		t.Fatalf("sessions %+v", s)
	}

	// The tool user never enters the registry.
	if d := m.List(); len(d[0].Users) != 0 {
		t.Fatalf("registry users %v", d[0].Users)
	}

	// The session ends: user dropped, connections killed.
	db.stmts = nil

	ft.fns[0]()

	want = []string{"DROP USER IF EXISTS " + acct, "KILL HARD CONNECTION USER " + acct}
	if strings.Join(db.stmts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("close statements\n%s", strings.Join(db.stmts, "\n"))
	}

	if s := m.ToolSessions(); len(s) != 0 {
		t.Fatalf("sessions after close %+v", s)
	}

	// A second close (timer and manual close racing) does nothing.
	db.stmts = nil
	if err := m.CloseTool(t.Context(), l.User); err != nil || len(db.stmts) != 0 {
		t.Fatalf("second close: %v %v", err, db.stmts)
	}
}

func TestOpenToolErrors(t *testing.T) {
	m, db, _ := toolManager(t)
	ctx := t.Context()

	if _, err := m.OpenTool(ctx, "other", time.Minute); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown database: %v", err)
	}

	if _, err := m.OpenTool(ctx, "mysql", time.Minute); !errors.Is(err, ErrInvalid) {
		t.Fatalf("reserved database: %v", err)
	}

	if _, err := m.OpenTool(ctx, "shop_wp", 0); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no duration: %v", err)
	}

	if len(db.stmts) != 0 {
		t.Fatalf("statements %v", db.stmts)
	}

	// A failed grant drops the new user again and keeps no session.
	db.failOn, db.err = "GRANT", errors.New("boom")

	if _, err := m.OpenTool(ctx, "shop_wp", time.Minute); err == nil {
		t.Fatal("grant failure accepted")
	}

	if last := db.stmts[len(db.stmts)-1]; !strings.HasPrefix(last, "DROP USER IF EXISTS 'hostashi_tool_") {
		t.Fatalf("no undo: %v", db.stmts)
	}

	if s := m.ToolSessions(); len(s) != 0 {
		t.Fatalf("sessions %+v", s)
	}

	// A failed drop keeps the session, so the next close retries.
	db.failOn = ""

	l, err := m.OpenTool(ctx, "shop_wp", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	db.failOn = "DROP USER"

	if err := m.CloseTool(ctx, l.User); err == nil {
		t.Fatal("drop failure hidden")
	}

	if s := m.ToolSessions(); len(s) != 1 {
		t.Fatalf("session lost: %+v", s)
	}
}

func TestDeleteClosesTools(t *testing.T) {
	m, db, ft := toolManager(t)
	ctx := t.Context()

	if _, err := m.Create(ctx, "blog", "blog_wp"); err != nil {
		t.Fatal(err)
	}

	shop, err := m.OpenTool(ctx, "shop_wp", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	blog, err := m.OpenTool(ctx, "blog_wp", time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	db.stmts = nil

	if err := m.Delete(ctx, "shop_wp"); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(strings.Join(db.stmts, "\n"), "KILL HARD CONNECTION USER '"+shop.User+"'") {
		t.Fatalf("shop session not closed: %v", db.stmts)
	}

	if s := m.ToolSessions(); len(s) != 1 || s[0].User != blog.User {
		t.Fatalf("sessions %+v", s)
	}

	if ft.stopped != 1 {
		t.Fatalf("timers stopped %d", ft.stopped)
	}
}
