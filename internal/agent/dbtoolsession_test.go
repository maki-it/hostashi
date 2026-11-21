// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

func readSession(t *testing.T, tool *dbTool, user string) string {
	t.Helper()

	p := filepath.Join(tool.sessionDir(), user)

	fi, err := os.Lstat(p)
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode() != 0o640 {
		t.Fatalf("session file mode %s", fi.Mode())
	}

	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

func TestDBToolExtendAndEnd(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("needs an unprivileged uid for the tool user")
	}

	toolUser(t)

	// The agent runs with UMask=0077; the tool must still read the file.
	old := syscall.Umask(0o077)
	t.Cleanup(func() { syscall.Umask(old) })

	db := &recordingDB{}
	m := newDBManager(t, db)

	if _, err := m.Create(t.Context(), "shop", "shop_wp"); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "run", "tickets")
	tool := newDBTool(m, dir, slog.New(slog.DiscardHandler))
	tool.after = func(time.Duration, func()) {}

	res, err := tool.open(t.Context(), agentrpc.Caller{}, agentapi.DBToolOpenRequest{Database: "shop_wp", Minutes: 30})
	if err != nil {
		t.Fatal(err)
	}

	// Opening records the end for the tool, in a directory only its
	// group may read.
	if got := readSession(t, tool, res.User); got != strconv.FormatInt(res.Expires.Unix(), 10) {
		t.Fatalf("session file %q", got)
	}

	if fi, err := os.Stat(tool.sessionDir()); err != nil || fi.Mode().Perm() != 0o750 {
		t.Fatalf("session dir %v %v", fi, err)
	}

	end := res.Expires.Add(agentapi.DBToolExtendStep).Truncate(time.Second)

	info, err := tool.extend(t.Context(), agentrpc.Caller{}, agentapi.DBToolExtendRequest{User: res.User, Expires: end})
	if err != nil {
		t.Fatal(err)
	}

	if !info.Expires.Equal(end) || info.Database != "shop_wp" {
		t.Fatalf("extended %+v", info)
	}

	if got := readSession(t, tool, res.User); got != strconv.FormatInt(end.Unix(), 10) {
		t.Fatalf("session file after extend %q", got)
	}

	if s := m.ToolSessions(); !s[0].Expires.Equal(end) {
		t.Fatalf("manager %+v", s)
	}

	list, err := tool.sessions(t.Context(), agentrpc.Caller{}, agentapi.DBToolSessionsRequest{})
	if err != nil || len(list.Sessions) != 1 || list.Sessions[0].User != res.User {
		t.Fatalf("sessions %+v %v", list, err)
	}

	// Ending: the tool refuses the session, the user goes.
	db.stmts = nil

	if _, err := tool.end(t.Context(), agentrpc.Caller{}, agentapi.DBToolEndRequest{User: res.User}); err != nil {
		t.Fatal(err)
	}

	if got := readSession(t, tool, res.User); got != "0" {
		t.Fatalf("session file after end %q", got)
	}

	if len(db.stmts) != 2 || !strings.HasPrefix(db.stmts[0], "DROP USER IF EXISTS '"+res.User+"'") {
		t.Fatalf("user not dropped: %v", db.stmts)
	}

	// An ended session cannot be extended, and ending it again is fine.
	_, err = tool.extend(t.Context(), agentrpc.Caller{}, agentapi.DBToolExtendRequest{User: res.User, Expires: end})
	wantCode(t, err, agentrpc.CodeNotFound)

	if _, err := tool.end(t.Context(), agentrpc.Caller{}, agentapi.DBToolEndRequest{User: res.User}); err != nil {
		t.Fatal(err)
	}

	// A remote session is known only by its file.
	remote := agentapi.DBToolUserPrefix + "0123456789abcdef"

	_, err = tool.extend(t.Context(), agentrpc.Caller{}, agentapi.DBToolExtendRequest{User: remote, Expires: end, Remote: true})
	wantCode(t, err, agentrpc.CodeNotFound)

	if err := tool.writeSession(remote, time.Now().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	if _, err := tool.extend(t.Context(), agentrpc.Caller{}, agentapi.DBToolExtendRequest{User: remote, Expires: end, Remote: true}); err != nil {
		t.Fatal(err)
	}

	if got := readSession(t, tool, remote); got != strconv.FormatInt(end.Unix(), 10) {
		t.Fatalf("remote session file %q", got)
	}

	db.stmts = nil

	if _, err := tool.end(t.Context(), agentrpc.Caller{}, agentapi.DBToolEndRequest{User: remote, Remote: true}); err != nil {
		t.Fatal(err)
	}

	if got := readSession(t, tool, remote); got != "0" || len(db.stmts) != 0 {
		t.Fatalf("remote end: file %q, statements %v", got, db.stmts)
	}

	// Files long past their end go when the next one is written.
	stale := time.Now().Add(-sessionStale - time.Minute)
	if err := os.Chtimes(filepath.Join(tool.sessionDir(), remote), stale, stale); err != nil {
		t.Fatal(err)
	}

	if err := tool.writeSession(res.User, time.Time{}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Lstat(filepath.Join(tool.sessionDir(), remote)); !os.IsNotExist(err) {
		t.Fatalf("stale session file kept: %v", err)
	}
}

func TestDBToolSessionValidation(t *testing.T) {
	user := agentapi.DBToolUserPrefix + "0123456789abcdef"
	now := time.Now()

	for _, r := range []agentapi.DBToolExtendRequest{
		{User: "root", Expires: now.Add(time.Minute)},
		{User: user, Expires: now.Add(-time.Minute)},
		{User: user, Expires: now.Add(agentapi.DBToolMaxRemaining + 2*time.Minute)},
	} {
		if r.Validate() == nil {
			t.Errorf("%+v accepted", r)
		}
	}

	if (agentapi.DBToolEndRequest{User: "../x"}).Validate() == nil {
		t.Error("bad user accepted")
	}
}
