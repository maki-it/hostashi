// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/store"
)

// The web database tool on external servers (ADR-0041 addendum 1). The
// tool's pool reaches only the servers the panel lists for it, by the
// addresses the panel resolved; a change of that list restarts the pool.
// For each session the panel creates a temporary user on the server and
// drops it when the session ends; db_remote_tools keeps it until then,
// also across a restart.

// DBToolServers calls db.tool.servers.
func (a RPCAgent) DBToolServers(ctx context.Context, req agentapi.DBToolServersRequest) (agentapi.DBToolServersResponse, error) {
	return agentapi.DBToolServers.Call(ctx, a.C, req)
}

// DBToolRemote calls db.tool.remote.
func (a RPCAgent) DBToolRemote(ctx context.Context, req agentapi.DBToolRemoteRequest) (agentapi.DBToolOpenResponse, error) {
	return agentapi.DBToolRemote.Call(ctx, a.C, req)
}

// remoteToolMargin is how long after the session's end the panel drops the
// user at the latest: the tool itself refuses the session at its end.
const remoteToolMargin = time.Minute

// openRemoteTool opens a session on d's external server.
func (s *Service) openRemoteTool(ctx context.Context, d store.Database, minutes int) (agentapi.DBToolOpenResponse, error) {
	if s.dbs == nil {
		return agentapi.DBToolOpenResponse{}, ErrDBServersOff
	}

	srv, err := s.dbServer(ctx, d.Server)
	if err != nil {
		return agentapi.DBToolOpenResponse{}, err
	}

	if err := s.syncToolServers(ctx, srv.ID); err != nil {
		return agentapi.DBToolOpenResponse{}, err
	}

	user, password := agentapi.DBToolUserPrefix+randomHex(8), randomAlnum(32)

	req := agentapi.DBToolRemoteRequest{
		Database: d.Name, Minutes: minutes, ServerID: srv.ID, Host: srv.Host, Port: srv.Port, TLS: srv.TLSMode,
		User: user, Password: password,
	}

	if err := req.Validate(); err != nil {
		return agentapi.DBToolOpenResponse{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := s.remote(ctx, srv, func(c RemoteDB) error { return c.CreateToolUser(ctx, d.Name, user, password) }); err != nil {
		return agentapi.DBToolOpenResponse{}, err
	}

	ends := s.now().Add(time.Duration(minutes)*time.Minute + remoteToolMargin).UTC()
	bg := context.WithoutCancel(ctx)

	// Recorded before the agent sees the password, so the user never
	// outlives the panel's knowledge of it.
	if err := s.st.AddDBRemoteTool(bg, store.DBRemoteTool{User: user, Database: d.Name, ServerID: srv.ID, Expires: ends}); err != nil {
		s.dropToolUser(bg, srv, user)

		return agentapi.DBToolOpenResponse{}, err
	}

	res, err := s.dbTool.DBToolRemote(ctx, req)
	if err != nil {
		if s.dropToolUser(bg, srv, user) {
			_ = s.st.DeleteDBRemoteTool(bg, user)
		}

		return agentapi.DBToolOpenResponse{}, err
	}

	after := s.afterFunc
	if after == nil {
		after = func(d time.Duration, f func()) { time.AfterFunc(d, f) }
	}

	after(ends.Sub(s.now()), func() { s.FinishDBRemoteTools(bg) })

	return res, nil
}

// syncToolServers sends the agent every external server with the
// addresses its name gives now. need is the server a session is being
// opened on: if its name gives no address, that is the error. Another
// server keeps the addresses last sent for it, or is left out.
func (s *Service) syncToolServers(ctx context.Context, need int64) error {
	s.remoteToolsMu.Lock()
	defer s.remoteToolsMu.Unlock()

	list, err := s.st.DBServers(ctx)
	if err != nil {
		return err
	}

	next := map[int64]agentapi.DBToolServer{}
	req := agentapi.DBToolServersRequest{Servers: []agentapi.DBToolServer{}}

	for _, srv := range list {
		if srv.Local() {
			continue
		}

		entry, err := s.toolServer(ctx, srv)
		if err != nil {
			if srv.ID == need {
				return err
			}

			old, ok := s.toolServers[srv.ID]
			if !ok || old.Host != srv.Host {
				s.log.WarnContext(ctx, "database tool: leave out a server whose name gives no address", "server", srv.Name, "err", err)

				continue
			}

			entry = old
			entry.CAPEM = srv.CAPEM
		}

		next[srv.ID] = entry
		req.Servers = append(req.Servers, entry)
	}

	if err := req.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	res, err := s.dbTool.DBToolServers(ctx, req)
	if err != nil {
		return err
	}

	s.toolServers = next

	if res.Restarted {
		s.log.InfoContext(ctx, "database tool: the list of external servers changed, open sessions ended", "servers", len(req.Servers))
	}

	return nil
}

// toolServer is srv as the tool's pool needs it.
func (s *Service) toolServer(ctx context.Context, srv store.DBServer) (agentapi.DBToolServer, error) {
	addrs, err := s.serverAddrs(ctx, srv)
	if err != nil {
		return agentapi.DBToolServer{}, err
	}

	// Sorted, so a resolver that rotates its answers does not restart the
	// pool.
	slices.Sort(addrs)

	entry := agentapi.DBToolServer{ID: srv.ID, Host: srv.Host, Addrs: addrs}
	if dbserver.TLSMode(srv.TLSMode) == dbserver.TLSVerifyCA {
		entry.CAPEM = srv.CAPEM
	}

	return entry, nil
}

// removeToolServers sends the list again after a server went, so the
// tool's pool no longer reaches it. Only a failure is logged.
func (s *Service) removeToolServers(ctx context.Context) {
	if s.dbTool == nil || s.dbs == nil {
		return
	}

	if err := s.syncToolServers(ctx, 0); err != nil {
		s.log.ErrorContext(ctx, "database tool: send the external servers", "err", err)
	}
}

// FinishDBRemoteTools drops the users of sessions that have ended. Each
// session schedules it for its end; the server check runs it too, for
// sessions of an earlier run of the panel.
func (s *Service) FinishDBRemoteTools(ctx context.Context) {
	s.remoteToolsMu.Lock()
	defer s.remoteToolsMu.Unlock()

	tools, err := s.st.DBRemoteTools(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "list database tool sessions on external servers", "err", err)

		return
	}

	now := s.now()

	for _, t := range tools {
		if t.Expires.After(now) || ctx.Err() != nil {
			continue
		}

		srv, err := s.st.DBServerByID(ctx, t.ServerID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			continue
		}

		if err == nil && !s.dropToolUser(ctx, srv, t.User) {
			continue
		}

		if err := s.st.DeleteDBRemoteTool(ctx, t.User); err != nil {
			s.log.ErrorContext(ctx, "forget a database tool session", "user", t.User, "err", err)
		}
	}
}

// endRemoteTools drops the users of every session on database of srv.
func (s *Service) endRemoteTools(ctx context.Context, srv store.DBServer, database string) error {
	s.remoteToolsMu.Lock()
	defer s.remoteToolsMu.Unlock()

	tools, err := s.st.DBRemoteTools(ctx)
	if err != nil {
		return err
	}

	for _, t := range tools {
		if t.ServerID != srv.ID || t.Database != database {
			continue
		}

		if err := s.remote(ctx, srv, func(c RemoteDB) error { return c.DropToolUser(ctx, t.User) }); err != nil {
			return err
		}

		if err := s.st.DeleteDBRemoteTool(ctx, t.User); err != nil {
			return err
		}
	}

	return nil
}

// dropToolUser drops a session's temporary user and logs a failure.
func (s *Service) dropToolUser(ctx context.Context, srv store.DBServer, user string) bool {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	if err := s.remote(cctx, srv, func(c RemoteDB) error { return c.DropToolUser(cctx, user) }); err != nil {
		s.log.ErrorContext(ctx, "drop the temporary user of a database tool session", "server", srv.Name, "user", user, "err", err)

		return false
	}

	return true
}
