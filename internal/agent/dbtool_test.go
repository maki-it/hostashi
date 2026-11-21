// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"encoding/json"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

func toolUser(t *testing.T) {
	t.Helper()

	fakeLookup(t, map[string]*user.User{
		agentapi.DBToolUser: {Uid: strconv.Itoa(os.Getuid()), Gid: strconv.Itoa(os.Getgid())},
	})
}

func TestDBToolOpen(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("needs an unprivileged uid for the tool user")
	}

	toolUser(t)

	db := &recordingDB{}
	m := newDBManager(t, db)

	if _, err := m.Create(t.Context(), "shop", "shop_wp"); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "run", "tickets")
	tool := newDBTool(m, dir, slog.New(slog.DiscardHandler))

	var timers []func()

	tool.after = func(_ time.Duration, f func()) { timers = append(timers, f) }

	res, err := tool.open(t.Context(), agentrpc.Caller{}, agentapi.DBToolOpenRequest{Database: "shop_wp", Minutes: 30})
	if err != nil {
		t.Fatal(err)
	}

	if !agentapi.ValidDBToolTicket(res.Ticket) || !agentapi.ValidDBToolUser(res.User) {
		t.Fatalf("response %+v", res)
	}

	fi, err := os.Stat(dir)
	if err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("ticket dir %v %v", fi, err)
	}

	p := filepath.Join(dir, res.Ticket)

	fi, err = os.Lstat(p)
	if err != nil || fi.Mode() != 0o600 {
		t.Fatalf("ticket file %v %v", fi, err)
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	var tk agentapi.DBToolTicket
	if err := json.Unmarshal(raw, &tk); err != nil {
		t.Fatal(err)
	}

	if tk.Database != "shop_wp" || tk.User != res.User || agentapi.ValidateDBPassword(tk.Password) != nil ||
		!tk.Expires.Equal(res.Expires) || time.Until(tk.TicketExpires) > agentapi.DBToolTicketTTL {
		t.Fatalf("ticket %+v", tk)
	}

	// The password stays in the file; the response has none.
	if out, _ := json.Marshal(res); strings.Contains(string(out), tk.Password) {
		t.Fatal("password in the response")
	}

	// Unused: the ticket and its user go after the TTL.
	if len(timers) != 1 {
		t.Fatalf("timers %d", len(timers))
	}

	db.stmts = nil

	timers[0]()

	if _, err := os.Lstat(p); !os.IsNotExist(err) {
		t.Fatalf("ticket kept: %v", err)
	}

	if len(db.stmts) != 2 || !strings.HasPrefix(db.stmts[0], "DROP USER IF EXISTS '"+res.User+"'") {
		t.Fatalf("user not dropped: %v", db.stmts)
	}

	// Used (the tool removed the ticket): the user stays for its session.
	res, err = tool.open(t.Context(), agentrpc.Caller{}, agentapi.DBToolOpenRequest{Database: "shop_wp", Minutes: 120})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dir, res.Ticket)); err != nil {
		t.Fatal(err)
	}

	db.stmts = nil

	timers[1]()

	if len(db.stmts) != 0 {
		t.Fatalf("used ticket dropped its user: %v", db.stmts)
	}

	if s := m.ToolSessions(); len(s) != 1 || s[0].User != res.User {
		t.Fatalf("sessions %+v", s)
	}

	// Leftovers of an earlier run.
	if err := clearTickets(dir); err != nil {
		t.Fatal(err)
	}

	if e, _ := os.ReadDir(dir); len(e) != 0 {
		t.Fatalf("tickets left: %v", e)
	}
}

func TestDBToolOpenRefused(t *testing.T) {
	db := &recordingDB{}
	m := newDBManager(t, db)
	dir := filepath.Join(t.TempDir(), "tickets")
	tool := newDBTool(m, dir, slog.New(slog.DiscardHandler))
	req := agentapi.DBToolOpenRequest{Database: "shop_wp", Minutes: 30}

	// No tool user: not installed.
	fakeLookup(t, map[string]*user.User{})

	_, err := tool.open(t.Context(), agentrpc.Caller{}, req)
	wantCode(t, err, agentrpc.CodePrecondition)

	fakeLookup(t, map[string]*user.User{agentapi.DBToolUser: {Uid: "0", Gid: "0"}})

	_, err = tool.open(t.Context(), agentrpc.Caller{}, req)
	wantCode(t, err, agentrpc.CodePrecondition)

	if len(db.stmts) != 0 {
		t.Fatalf("statements %v", db.stmts)
	}

	// No MariaDB.
	_, err = newDBTool(nil, dir, slog.New(slog.DiscardHandler)).open(t.Context(), agentrpc.Caller{}, req)
	wantCode(t, err, agentrpc.CodePrecondition)

	if os.Getuid() == 0 {
		return
	}

	toolUser(t)

	// Unknown database.
	_, err = tool.open(t.Context(), agentrpc.Caller{}, req)
	wantCode(t, err, agentrpc.CodeNotFound)

	// The ticket path is a link: nothing is written through it.
	if _, err := m.Create(t.Context(), "shop", "shop_wp"); err != nil {
		t.Fatal(err)
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}

	if _, err := tool.open(t.Context(), agentrpc.Caller{}, req); err == nil {
		t.Fatal("symlinked ticket directory accepted")
	}
}

func TestDBToolValidation(t *testing.T) {
	for _, r := range []agentapi.DBToolOpenRequest{
		{Database: "shop_wp", Minutes: 0},
		{Database: "shop_wp", Minutes: 31},
		{Database: "mysql", Minutes: 30},
		{Database: "", Minutes: 30},
	} {
		if r.Validate() == nil {
			t.Errorf("%+v accepted", r)
		}
	}

	if agentapi.ValidDBToolTicket("../x") || agentapi.ValidDBToolUser("hostashi_tool_x") {
		t.Fatal("bad names accepted")
	}
}
