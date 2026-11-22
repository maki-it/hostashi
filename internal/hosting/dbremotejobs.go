// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

// Import and export of databases on external servers (ADR-0041 addendum
// 1). The panel creates a temporary user on the server, resolves the
// server's name and hands both to the agent, which runs the job in a unit
// that reaches only those addresses. The user is dropped once the job has
// ended; db_remote_jobs keeps it until then, also across a restart.

// ErrDBServerResolve means the server's name gave no address the job may
// connect to.
var ErrDBServerResolve = errors.New("hosting: the database server's name gives no usable address")

// remoteJobLimit is when the panel drops a job's user even if the agent
// still reports the job: the unit ends after 6 hours.
const remoteJobLimit = 6*time.Hour + 15*time.Minute

// DBRemoteStart calls db.remote.start.
func (a RPCAgent) DBRemoteStart(ctx context.Context, req agentapi.DBRemoteJobRequest) (agentapi.DBJob, error) {
	return agentapi.DBRemoteJobStart.Call(ctx, a.C, req)
}

// DBRemoteJob calls db.remote.job.
func (a RPCAgent) DBRemoteJob(ctx context.Context, id string) (agentapi.DBJob, error) {
	return agentapi.DBRemoteJobGet.Call(ctx, a.C, agentapi.DBJobRequest{ID: id})
}

// Resolver looks up a server's addresses; net.DefaultResolver is the real
// one.
type Resolver interface {
	LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error)
}

// SetResolver replaces the resolver for the external servers' names (tests).
func (s *Service) SetResolver(r Resolver) { s.resolver = r }

// startRemoteDump starts an import or export of d on its external server.
// The caller holds s.mu.
func (s *Service) startRemoteDump(ctx context.Context, actor int64, action string, d store.Database, site, path string) (agentapi.DBJob, error) {
	if action == "database.load" || s.dbs == nil {
		return agentapi.DBJob{}, ErrDBServerLocalOnly
	}

	kind := "import"
	if action == "database.export" {
		kind = "export"
	}

	srv, err := s.dbServer(ctx, d.Server)
	if err != nil {
		return agentapi.DBJob{}, err
	}

	engine := ""

	if IsPostgres(srv) {
		engine = agentapi.RemoteEnginePostgreSQL
	} else if err := agentapi.ValidateDumpPath(path); err != nil {
		return agentapi.DBJob{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	addrs, err := s.serverAddrs(ctx, srv)
	if err != nil {
		return agentapi.DBJob{}, err
	}

	cfg, err := s.dbs.Config(srv)
	if err != nil {
		return agentapi.DBJob{}, err
	}

	user, password := "hostashi_job_"+randomHex(8), randomAlnum(32)

	req := agentapi.DBRemoteJobRequest{
		Kind: kind, Engine: engine, Site: site, Database: d.Name, Path: path,
		Host: srv.Host, Port: srv.Port, Addrs: addrs, TLS: string(cfg.TLS), CAPEM: cfg.CAPEM,
		User: user, Password: password,
	}

	if err := req.Validate(); err != nil {
		return agentapi.DBJob{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := s.remote(ctx, srv, func(c RemoteDB) error { return c.CreateJobUser(ctx, d.Name, user, password) }); err != nil {
		return agentapi.DBJob{}, err
	}

	j, err := s.ag.DBRemoteStart(ctx, req)
	if err != nil {
		s.dropJobUser(ctx, srv, d.Name, user)

		return agentapi.DBJob{}, err
	}

	if err := s.st.AddDBRemoteJob(context.WithoutCancel(ctx), store.DBRemoteJob{
		JobID: j.ID, Database: d.Name, ServerID: srv.ID, User: user, CreatedAt: s.now().UTC(),
	}); err != nil {
		// The job runs; its user stays on the server until someone drops it.
		s.log.ErrorContext(ctx, "record the temporary user of a database job", "job", j.ID, "server", srv.Name, "user", user, "err", err)
	}

	s.audit(ctx, actor, action, d.Name, map[string]string{"site": site, "path": path, "job": j.ID, "server": srv.Name})

	return j, nil
}

// serverAddrs resolves the server's name to at most agentapi.MaxRemoteAddrs
// unicast addresses. The temporary user may log in only from the host the
// server saw the panel connect from, so a server that saw an IPv4 address
// gets only IPv4 addresses, and IPv6 likewise.
func (s *Service) serverAddrs(ctx context.Context, srv store.DBServer) ([]string, error) {
	if ip, err := netip.ParseAddr(srv.Host); err == nil {
		return []string{ip.String()}, nil
	}

	var r Resolver = net.DefaultResolver
	if s.resolver != nil {
		r = s.resolver
	}

	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	found, err := r.LookupNetIP(rctx, "ip", srv.Host)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDBServerResolve, err)
	}

	family := 0

	if ch, err := netip.ParseAddr(srv.Status.ClientHost); err == nil {
		family = 6
		if ch.Unmap().Is4() {
			family = 4
		}
	}

	seen := map[netip.Addr]bool{}

	var out []string

	for _, a := range found {
		a = a.Unmap().WithZone("")

		switch {
		case !a.IsGlobalUnicast() || seen[a]:
			continue
		case family == 4 && !a.Is4(), family == 6 && !a.Is6():
			continue
		}

		seen[a] = true
		out = append(out, a.String())

		if len(out) == agentapi.MaxRemoteAddrs {
			break
		}
	}

	if len(out) == 0 {
		return nil, ErrDBServerResolve
	}

	return out, nil
}

// remoteDatabaseJob reports a job of a database on an external server and
// drops the job's user once it has ended.
func (s *Service) remoteDatabaseJob(ctx context.Context, database, id string) (agentapi.DBJob, error) {
	j, err := s.ag.DBRemoteJob(ctx, id)
	if isCode(err, agentrpc.CodeNotFound) || (err == nil && j.Database != database) {
		return agentapi.DBJob{}, ErrJobNotFound
	}

	if err != nil {
		return agentapi.DBJob{}, err
	}

	if j.State != agentapi.JobRunning {
		s.finishRemoteJobs(context.WithoutCancel(ctx), id)
	}

	return j, nil
}

// RemoteJob reports a job on an external server by its id alone, for the
// import of a restored dump (whose page knows only the job), and drops the
// job's user once it has ended.
func (s *Service) RemoteJob(ctx context.Context, id string) (agentapi.DBJob, error) {
	if (agentapi.DBJobRequest{ID: id}).Validate() != nil {
		return agentapi.DBJob{}, ErrJobNotFound
	}

	j, err := s.ag.DBRemoteJob(ctx, id)
	if isCode(err, agentrpc.CodeNotFound) {
		return agentapi.DBJob{}, ErrJobNotFound
	}

	if err != nil {
		return agentapi.DBJob{}, err
	}

	return s.remoteDatabaseJob(ctx, j.Database, id)
}

// FinishDBRemoteJobs drops the users of jobs that have ended. The panel
// runs it with the server check, so a user does not outlive a job whose
// page nobody watched or a restart of the panel.
func (s *Service) FinishDBRemoteJobs(ctx context.Context) { s.finishRemoteJobs(ctx, "") }

// finishRemoteJobs drops the user of job only, or with only empty of every
// recorded job that has ended. A user the server would not drop stays
// recorded for the next round.
func (s *Service) finishRemoteJobs(ctx context.Context, only string) {
	s.remoteJobsMu.Lock()
	defer s.remoteJobsMu.Unlock()

	jobs, err := s.st.DBRemoteJobs(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "list database jobs on external servers", "err", err)

		return
	}

	for _, rj := range jobs {
		if (only != "" && rj.JobID != only) || ctx.Err() != nil {
			continue
		}

		if s.now().Sub(rj.CreatedAt) < remoteJobLimit {
			j, err := s.ag.DBRemoteJob(ctx, rj.JobID)
			if err != nil && !isCode(err, agentrpc.CodeNotFound) {
				continue
			}

			if err == nil && j.State == agentapi.JobRunning {
				continue
			}
		}

		srv, err := s.st.DBServerByID(ctx, rj.ServerID)
		if err == nil && !s.dropJobUser(ctx, srv, rj.Database, rj.User) {
			continue
		}

		if err := s.st.DeleteDBRemoteJob(ctx, rj.JobID); err != nil {
			s.log.ErrorContext(ctx, "forget a database job", "job", rj.JobID, "err", err)
		}
	}
}

// dropJobUser drops a temporary user and logs a failure.
func (s *Service) dropJobUser(ctx context.Context, srv store.DBServer, database, user string) bool {
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	if err := s.remote(cctx, srv, func(c RemoteDB) error { return c.DropJobUser(cctx, database, user) }); err != nil {
		s.log.ErrorContext(ctx, "drop the temporary user of a database job", "server", srv.Name, "user", user, "err", err)

		return false
	}

	return true
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// randomAlnum returns n letters and digits, without bias.
func randomAlnum(n int) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

	out := make([]byte, 0, n)
	buf := make([]byte, 2*n)

	for len(out) < n {
		_, _ = rand.Read(buf)

		for _, c := range buf {
			// 248 = 4*62: drop the rest so every character is as likely.
			if c < 248 && len(out) < n {
				out = append(out, chars[c%62])
			}
		}
	}

	return string(out)
}
