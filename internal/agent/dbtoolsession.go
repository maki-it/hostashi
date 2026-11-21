// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Extending and ending sessions of the web database tool (ADR-0038
// addendum 1). Each session has a file in the session directory with its
// end in Unix seconds, 0 once it was ended. The tool reads it on every
// request, so a tab that is open learns the new end. The agent owns the
// files; the tool's group may only read them.

var errNoToolSession = &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "the session has ended"}

// sessionStale is how long after its last write a session file is surely
// past its end: no session ends further ahead than DBToolMaxRemaining.
const sessionStale = agentapi.DBToolMaxRemaining + 10*time.Minute

// sessionDir sits next to the ticket directory.
func (t *dbTool) sessionDir() string {
	return filepath.Join(filepath.Dir(t.dir), filepath.Base(agentapi.DBToolSessionDir))
}

// prepareSessionDir makes the session directory a real directory of the
// agent, readable by the tool's group, and removes files of sessions that
// are long over.
func (t *dbTool) prepareSessionDir(gid int) error {
	dir := t.sessionDir()

	if err := os.Mkdir(dir, 0o750); err != nil && !errors.Is(err, os.ErrExist) {
		return fmt.Errorf("database tool: %w", err)
	}

	fi, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("database tool: %w", err)
	}

	if !fi.IsDir() {
		return fmt.Errorf("database tool: %s is not a directory", dir)
	}

	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() || int(st.Gid) != gid {
		if err := os.Lchown(dir, os.Getuid(), gid); err != nil {
			return fmt.Errorf("database tool: %w", err)
		}
	}

	if fi.Mode().Perm() != 0o750 {
		if err := os.Chmod(dir, 0o750); err != nil {
			return fmt.Errorf("database tool: %w", err)
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("database tool: %w", err)
	}

	for _, e := range entries {
		info, err := e.Info()
		if err != nil || t.now().Sub(info.ModTime()) < sessionStale {
			continue
		}

		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("database tool: %w", err)
		}
	}

	return nil
}

// writeSession records the end of user's session; the zero time ends it.
// The file is replaced in one step, so the tool never reads half of it.
func (t *dbTool) writeSession(user string, expires time.Time) error {
	if !agentapi.ValidDBToolUser(user) {
		return fmt.Errorf("database tool: session user %q", user)
	}

	_, gid, err := toolIdentity()
	if err != nil {
		return errNoDBTool
	}

	if err := t.prepareSessionDir(gid); err != nil {
		return err
	}

	end := int64(0)
	if !expires.IsZero() {
		end = expires.Unix()
	}

	p := filepath.Join(t.sessionDir(), user)
	tmp := p + ".new"
	_ = os.Remove(tmp)

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o640)
	if err != nil {
		return fmt.Errorf("database tool: session: %w", err)
	}

	_, werr := f.WriteString(strconv.FormatInt(end, 10))
	if werr == nil {
		werr = f.Chown(-1, gid)
	}

	// The agent's umask would leave the tool's group out.
	if werr == nil {
		werr = f.Chmod(0o640)
	}

	if cerr := f.Close(); werr == nil {
		werr = cerr
	}

	if werr == nil {
		werr = os.Rename(tmp, p)
	}

	if werr != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("database tool: session: %w", werr)
	}

	return nil
}

// removeSession forgets the file of a session that never started.
func (t *dbTool) removeSession(user string) {
	if !agentapi.ValidDBToolUser(user) {
		return
	}

	if err := os.Remove(filepath.Join(t.sessionDir(), user)); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.log.Error("database tool: remove session file", "user", user, "err", err)
	}
}

// sessionEnd reads the end recorded for user; ok is false without a file
// or for an ended session.
func (t *dbTool) sessionEnd(user string) (time.Time, bool) {
	raw, err := os.ReadFile(filepath.Join(t.sessionDir(), user))
	if err != nil {
		return time.Time{}, false
	}

	end, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || end <= 0 {
		return time.Time{}, false
	}

	return time.Unix(end, 0).UTC(), true
}

func (t *dbTool) extend(ctx context.Context, _ agentrpc.Caller, req agentapi.DBToolExtendRequest) (agentapi.DBToolSessionInfo, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	info := agentapi.DBToolSessionInfo{User: req.User, Expires: req.Expires.UTC()}

	if req.Remote {
		// The panel keeps the user on the external server; the agent only
		// knows the session from its file.
		if end, ok := t.sessionEnd(req.User); !ok || !end.After(t.now()) {
			return agentapi.DBToolSessionInfo{}, errNoToolSession
		}
	} else {
		if t.db == nil {
			return agentapi.DBToolSessionInfo{}, errNoDB
		}

		s, err := t.db.ExtendTool(ctx, req.User, req.Expires)
		if err != nil {
			return agentapi.DBToolSessionInfo{}, mapToolSessionError(err)
		}

		info.Database = s.Database
	}

	if err := t.writeSession(req.User, info.Expires); err != nil {
		return agentapi.DBToolSessionInfo{}, err
	}

	t.log.InfoContext(ctx, "database tool session extended", "user", req.User, "expires", info.Expires)

	return info, nil
}

func (t *dbTool) end(ctx context.Context, _ agentrpc.Caller, req agentapi.DBToolEndRequest) (struct{}, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// The tool refuses the session first, then the user goes with its
	// connections.
	if _, err := os.Lstat(filepath.Join(t.sessionDir(), req.User)); err == nil {
		if err := t.writeSession(req.User, time.Time{}); err != nil {
			return struct{}{}, err
		}
	}

	if !req.Remote && t.db != nil {
		if err := t.db.CloseTool(ctx, req.User); err != nil {
			return struct{}{}, mapDBError(err)
		}
	}

	t.log.InfoContext(ctx, "database tool session ended", "user", req.User)

	return struct{}{}, nil
}

func (t *dbTool) sessions(_ context.Context, _ agentrpc.Caller, _ agentapi.DBToolSessionsRequest) (agentapi.DBToolSessionsResponse, error) {
	out := agentapi.DBToolSessionsResponse{Sessions: []agentapi.DBToolSessionInfo{}}
	if t.db == nil {
		return out, nil
	}

	for _, s := range t.db.ToolSessions() {
		out.Sessions = append(out.Sessions, agentapi.DBToolSessionInfo{User: s.User, Database: s.Database, Expires: s.Expires.UTC()})
	}

	return out, nil
}

func mapToolSessionError(err error) error {
	var rpcErr *agentrpc.Error
	if errors.As(mapDBError(err), &rpcErr) && rpcErr.Code == agentrpc.CodeNotFound {
		return errNoToolSession
	}

	return mapDBError(err)
}
