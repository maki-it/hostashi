// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

// Open sessions of the web database tool (ADR-0038 addendum 1): the panel
// lists them per database, extends one by DBToolExtendStep, at most
// DBToolMaxRemaining ahead, and ends one at once. db_tool_sessions says who
// opened a session; the agent (local) and db_remote_tools (external) say
// whether it still runs.

var (
	// ErrToolSessionNotFound means the session has ended or is not one of
	// this database's.
	ErrToolSessionNotFound = errors.New("hosting: no such database tool session")
	// ErrToolSessionLimit means the session already ends as late as it may.
	ErrToolSessionLimit = errors.New("hosting: the session cannot be extended further")
)

// DBToolSessionView is an open session as the panel shows it.
type DBToolSessionView struct {
	ID       string    `json:"id"`
	OpenedBy string    `json:"user"`
	Opened   time.Time `json:"opened"`
	Expires  time.Time `json:"expires"`
}

// DBToolExtend calls db.tool.extend.
func (a RPCAgent) DBToolExtend(ctx context.Context, req agentapi.DBToolExtendRequest) (agentapi.DBToolSessionInfo, error) {
	return agentapi.DBToolExtend.Call(ctx, a.C, req)
}

// DBToolEnd calls db.tool.end.
func (a RPCAgent) DBToolEnd(ctx context.Context, req agentapi.DBToolEndRequest) error {
	_, err := agentapi.DBToolEnd.Call(ctx, a.C, req)

	return err
}

// DBToolSessions calls db.tool.sessions.
func (a RPCAgent) DBToolSessions(ctx context.Context) ([]agentapi.DBToolSessionInfo, error) {
	res, err := agentapi.DBToolSessions.Call(ctx, a.C, agentapi.DBToolSessionsRequest{})

	return res.Sessions, err
}

// recordToolSession notes who opened a session. A failure only costs the
// session its line in the panel, so it is logged.
func (s *Service) recordToolSession(ctx context.Context, actor int64, d store.Database, res agentapi.DBToolOpenResponse) {
	t := store.DBToolSession{
		User: res.User, Database: d.Name, Remote: d.Server != store.LocalDBServer,
		OpenedAt: s.now().UTC(), Expires: res.Expires.UTC(),
	}
	if actor > 0 {
		t.OpenedBy = &actor
	}

	if err := s.st.AddDBToolSession(context.WithoutCancel(ctx), t); err != nil {
		s.log.ErrorContext(ctx, "record a database tool session", "user", res.User, "err", err)
	}
}

func (s *Service) toolSessionAgent() (DBToolAgent, error) {
	if s.dbTool == nil {
		return nil, ErrNoDBTool
	}

	return s.dbTool, nil
}

// DBToolSessionsOf lists the open sessions on database, the first to end
// first. Rows of sessions that no longer run are dropped on the way.
func (s *Service) DBToolSessionsOf(ctx context.Context, database string) ([]DBToolSessionView, error) {
	if _, err := s.toolDatabase(ctx, database); err != nil {
		return nil, err
	}

	a, err := s.toolSessionAgent()
	if err != nil {
		return nil, err
	}

	rows, err := s.st.DBToolSessionsOf(ctx, database)
	if err != nil {
		return nil, err
	}

	live := map[string]bool{}

	local, err := a.DBToolSessions(ctx)
	if err != nil {
		return nil, err
	}

	for _, l := range local {
		live[l.User] = true
	}

	remote, err := s.st.DBRemoteTools(ctx)
	if err != nil {
		return nil, err
	}

	for _, r := range remote {
		live[r.User] = true
	}

	now := s.now()
	out := []DBToolSessionView{}

	for _, r := range rows {
		if !live[r.User] || !r.Expires.After(now) {
			if err := s.st.DeleteDBToolSession(ctx, r.User); err != nil {
				s.log.ErrorContext(ctx, "forget an ended database tool session", "user", r.User, "err", err)
			}

			continue
		}

		out = append(out, DBToolSessionView{ID: r.User, OpenedBy: r.OpenedByName, Opened: r.OpenedAt, Expires: r.Expires})
	}

	return out, nil
}

// toolDatabase returns database if it exists.
func (s *Service) toolDatabase(ctx context.Context, database string) (store.Database, error) {
	if err := agentapi.ValidateDBName(database); err != nil {
		return store.Database{}, ErrDatabaseNotFound
	}

	d, err := s.st.DatabaseByName(ctx, database)
	if errors.Is(err, store.ErrNotFound) {
		return store.Database{}, ErrDatabaseNotFound
	}

	return d, err
}

// toolSession returns the running session id on database.
func (s *Service) toolSession(ctx context.Context, database, id string) (store.DBToolSession, error) {
	if !agentapi.ValidDBToolUser(id) {
		return store.DBToolSession{}, ErrToolSessionNotFound
	}

	t, err := s.st.DBToolSessionByUser(ctx, id)
	if errors.Is(err, store.ErrNotFound) || (err == nil && (t.Database != database || !t.Expires.After(s.now()))) {
		return store.DBToolSession{}, ErrToolSessionNotFound
	}

	return t, err
}

// ExtendDBToolSession moves the end of session id on database
// DBToolExtendStep later, at most DBToolMaxRemaining from now.
func (s *Service) ExtendDBToolSession(ctx context.Context, actor int64, database, id string) (DBToolSessionView, error) {
	if _, err := s.toolDatabase(ctx, database); err != nil {
		return DBToolSessionView{}, err
	}

	a, err := s.toolSessionAgent()
	if err != nil {
		return DBToolSessionView{}, err
	}

	t, err := s.toolSession(ctx, database, id)
	if err != nil {
		return DBToolSessionView{}, err
	}

	now := s.now()
	end := t.Expires.Add(agentapi.DBToolExtendStep)

	if limit := now.Add(agentapi.DBToolMaxRemaining); end.After(limit) {
		end = limit
	}

	end = end.Truncate(time.Second).UTC()
	if !end.After(t.Expires) {
		return DBToolSessionView{}, ErrToolSessionLimit
	}

	if t.Remote {
		err = s.extendRemoteTool(ctx, a, t, end)
	} else {
		_, err = a.DBToolExtend(ctx, agentapi.DBToolExtendRequest{User: t.User, Expires: end})
		if err == nil {
			err = s.st.SetDBToolSessionExpires(ctx, t.User, end, end)
		}
	}

	if isCode(err, agentrpc.CodeNotFound) {
		_ = s.st.DeleteDBToolSession(ctx, t.User)

		return DBToolSessionView{}, ErrToolSessionNotFound
	}

	if err != nil {
		return DBToolSessionView{}, err
	}

	s.audit(ctx, actor, "database.tool.extend", database, map[string]string{
		"dbUser": t.User, "expires": end.Format(time.RFC3339),
	})

	return DBToolSessionView{ID: t.User, OpenedBy: t.OpenedByName, Opened: t.OpenedAt, Expires: end}, nil
}

// extendRemoteTool keeps the user on the external server longer: the
// panel's own record first, so the user never lives past what the panel
// knows, then the tool.
func (s *Service) extendRemoteTool(ctx context.Context, a DBToolAgent, t store.DBToolSession, end time.Time) error {
	s.remoteToolsMu.Lock()
	err := s.st.SetDBToolSessionExpires(ctx, t.User, end, end.Add(remoteToolMargin))
	s.remoteToolsMu.Unlock()

	if err != nil {
		return err
	}

	if _, err := a.DBToolExtend(ctx, agentapi.DBToolExtendRequest{User: t.User, Expires: end, Remote: true}); err != nil {
		return err
	}

	after := s.afterFunc
	if after == nil {
		after = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
	}

	bg := context.WithoutCancel(ctx)

	after(end.Add(remoteToolMargin).Sub(s.now()), func() { s.FinishDBRemoteTools(bg) })

	return nil
}

// EndDBToolSession ends session id on database now: the tool refuses it,
// and its user goes with its connections.
func (s *Service) EndDBToolSession(ctx context.Context, actor int64, database, id string) error {
	d, err := s.toolDatabase(ctx, database)
	if err != nil {
		return err
	}

	a, err := s.toolSessionAgent()
	if err != nil {
		return err
	}

	t, err := s.toolSession(ctx, database, id)
	if err != nil {
		return err
	}

	if err := a.DBToolEnd(ctx, agentapi.DBToolEndRequest{User: t.User, Remote: t.Remote}); err != nil {
		return err
	}

	if t.Remote {
		if err := s.endRemoteTool(ctx, d, t.User); err != nil {
			return err
		}
	}

	if err := s.st.DeleteDBToolSession(ctx, t.User); err != nil {
		return err
	}

	s.audit(ctx, actor, "database.tool.end", database, map[string]string{"dbUser": t.User})

	return nil
}

// endRemoteTool drops the user of one session on d's external server.
func (s *Service) endRemoteTool(ctx context.Context, d store.Database, user string) error {
	srv, err := s.dbServer(ctx, d.Server)
	if err != nil {
		return err
	}

	s.remoteToolsMu.Lock()
	defer s.remoteToolsMu.Unlock()

	if err := s.remote(ctx, srv, func(c RemoteDB) error { return c.DropToolUser(ctx, user) }); err != nil {
		return err
	}

	return s.st.DeleteDBRemoteTool(ctx, user)
}
