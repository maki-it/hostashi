// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/mariadb"
)

// dbTool hands out sessions of the web database tool (ADR-0038). The
// login goes into a ticket file that only the tool's system user can read;
// the caller gets the ticket id.
type dbTool struct {
	db  *mariadb.Manager
	dir string // ticket directory; its parent must belong to root
	log *slog.Logger
	// after replaces time.AfterFunc in tests.
	after func(d time.Duration, f func())
	now   func() time.Time

	// External servers (dbtoolremote.go). units nil: none.
	units      DBToolUnits
	serversDir string // default agentapi.DBToolServerDir
	unitDir    string // default /etc/systemd/system
	mu         sync.Mutex
}

var errNoDBTool = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the database tool is not installed on this server"}

func newDBTool(db *mariadb.Manager, dir string, log *slog.Logger) *dbTool {
	if dir == "" {
		dir = agentapi.DBToolTicketDir
	}

	return &dbTool{db: db, dir: dir, log: log, after: func(d time.Duration, f func()) { time.AfterFunc(d, f) }, now: time.Now}
}

func (t *dbTool) register(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.DBToolOpen, t.open)
	agentrpc.Handle(s, agentapi.DBToolServers, t.setServers)
	agentrpc.Handle(s, agentapi.DBToolRemote, t.remote)
}

func (t *dbTool) open(ctx context.Context, _ agentrpc.Caller, req agentapi.DBToolOpenRequest) (agentapi.DBToolOpenResponse, error) {
	if t.db == nil {
		return agentapi.DBToolOpenResponse{}, errNoDB
	}

	uid, gid, err := toolIdentity()
	if err != nil {
		t.log.WarnContext(ctx, "database tool", "err", err)

		return agentapi.DBToolOpenResponse{}, errNoDBTool
	}

	if err := t.prepareDir(uid, gid); err != nil {
		return agentapi.DBToolOpenResponse{}, err
	}

	login, err := t.db.OpenTool(ctx, req.Database, time.Duration(req.Minutes)*time.Minute)
	if err != nil {
		return agentapi.DBToolOpenResponse{}, mapDBError(err)
	}

	bg := context.WithoutCancel(ctx)
	closeUser := func() {
		cctx, cancel := context.WithTimeout(bg, 30*time.Second)
		defer cancel()

		if err := t.db.CloseTool(cctx, login.User); err != nil {
			t.log.ErrorContext(cctx, "database tool: drop user", "user", login.User, "err", err)
		}
	}

	id := newTicketID()
	ticket := agentapi.DBToolTicket{
		Database: login.Database, User: login.User, Password: login.Password,
		Expires: login.Expires, TicketExpires: t.now().Add(agentapi.DBToolTicketTTL),
	}

	if err := t.writeTicket(id, ticket, uid, gid); err != nil {
		closeUser()

		return agentapi.DBToolOpenResponse{}, err
	}

	// An unused ticket goes after its TTL, and its user with it. A used one
	// was removed by the tool; its user lives until the session ends.
	t.after(agentapi.DBToolTicketTTL, func() {
		err := os.Remove(filepath.Join(t.dir, id))
		if err == nil {
			closeUser()
		} else if !errors.Is(err, os.ErrNotExist) {
			t.log.ErrorContext(bg, "database tool: remove unused ticket", "err", err)
		}
	})

	t.log.InfoContext(ctx, "database tool session", "database", login.Database, "user", login.User, "expires", login.Expires)

	return agentapi.DBToolOpenResponse{Ticket: id, User: login.User, Expires: login.Expires}, nil
}

// toolIdentity looks up the tool's system user; without it the tool is not
// installed.
func toolIdentity() (int, int, error) {
	u, err := lookupUser(agentapi.DBToolUser)
	if err != nil {
		return 0, 0, err
	}

	uid, err := strconv.Atoi(u.Uid)
	if err != nil || uid <= 0 {
		return 0, 0, fmt.Errorf("%s: bad uid %q", agentapi.DBToolUser, u.Uid)
	}

	gid, err := strconv.Atoi(u.Gid)
	if err != nil || gid <= 0 {
		return 0, 0, fmt.Errorf("%s: bad gid %q", agentapi.DBToolUser, u.Gid)
	}

	return uid, gid, nil
}

// prepareDir makes the ticket directory a real directory of the tool user,
// mode 0700. The tool may create and remove files in it, but its parent
// belongs to root, so the directory itself cannot be swapped for a link.
func (t *dbTool) prepareDir(uid, gid int) error {
	parent := filepath.Dir(t.dir)

	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("database tool: %w", err)
	}

	if err := os.Mkdir(t.dir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("database tool: %w", err)
	}

	fi, err := os.Lstat(t.dir)
	if err != nil {
		return fmt.Errorf("database tool: %w", err)
	}

	if !fi.IsDir() {
		return fmt.Errorf("database tool: %s is not a directory", t.dir)
	}

	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid || int(st.Gid) != gid {
		if err := os.Lchown(t.dir, uid, gid); err != nil {
			return fmt.Errorf("database tool: %w", err)
		}
	}

	if fi.Mode().Perm() != 0o700 {
		if err := os.Chmod(t.dir, 0o700); err != nil {
			return fmt.Errorf("database tool: %w", err)
		}
	}

	return nil
}

// writeTicket creates the ticket file. O_EXCL and O_NOFOLLOW: whatever the
// tool user put into its directory, the agent writes only a new file.
func (t *dbTool) writeTicket(id string, ticket agentapi.DBToolTicket, uid, gid int) error {
	data, err := json.Marshal(ticket) //nolint:gosec // the ticket carries the password on purpose, readable only by the tool user
	if err != nil {
		return err
	}

	p := filepath.Join(t.dir, id)

	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return fmt.Errorf("database tool: ticket: %w", err)
	}

	_, werr := f.Write(data)
	if werr == nil {
		werr = f.Chown(uid, gid)
	}

	if cerr := f.Close(); werr == nil {
		werr = cerr
	}

	if werr != nil {
		_ = os.Remove(p)

		return fmt.Errorf("database tool: ticket: %w", werr)
	}

	return nil
}

// clearTickets removes the tickets of an earlier run; their users are gone
// too (RemoveToolUsers). A missing directory is fine.
func clearTickets(dir string) error {
	if dir == "" {
		dir = agentapi.DBToolTicketDir
	}

	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return err
	}

	for _, e := range entries {
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}

	return nil
}

func newTicketID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}
