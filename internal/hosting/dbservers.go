// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of the database server functions (ADR-0041).
var (
	ErrDBServerNotFound = errors.New("hosting: unknown database server")
	ErrDBServerExists   = errors.New("hosting: a database server with this name exists")
	ErrDBServerInUse    = errors.New("hosting: the database server has databases or is the local server")
	ErrDBServersOff     = errors.New("hosting: external database servers are not available")
	// ErrDBServerCheck wraps why a server failed the connection check.
	ErrDBServerCheck = errors.New("hosting: the database server failed the check")
)

// RemoteDB is the typed operations on one external server; dbserver.Client
// implements it.
type RemoteDB interface {
	CreateDatabase(ctx context.Context, name string) error
	DropDatabase(ctx context.Context, name string) error
	CreateUser(ctx context.Context, database, user, password string) error
	DropUser(ctx context.Context, user string) error
	SetPassword(ctx context.Context, user, password string) error
}

// DBServers reaches external database servers. The panel process does
// this itself; the agent has no network (ADR-0041).
type DBServers interface {
	// Check connects with cfg, reads the server and, if privileges is set,
	// tests that the administrator can create databases and users.
	Check(ctx context.Context, cfg dbserver.Config, privileges bool) (dbserver.Info, error)
	// Seal seals an admin password for the server with id.
	Seal(password string, id int64) ([]byte, error)
	// Config opens the sealed password of srv.
	Config(srv store.DBServer) (dbserver.Config, error)
	// With runs fn with a client for srv and closes the connection after.
	With(ctx context.Context, srv store.DBServer, fn func(RemoteDB) error) error
}

// EnableDBServers turns on external database servers.
func (s *Service) EnableDBServers(d DBServers) { s.dbs = d }

// dbServerNameRe is the rule for a server's name: short, lower case, safe
// in paths and logs.
var dbServerNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// ValidateDBServerName accepts the name of an external server.
func ValidateDBServerName(name string) error {
	if !dbServerNameRe.MatchString(name) {
		return fmt.Errorf("%w: server name must match %s", ErrInvalid, dbServerNameRe)
	}

	if name == store.LocalDBServer {
		return fmt.Errorf("%w: %q is the local server", ErrInvalid, name)
	}

	return nil
}

// NewDBServer describes an external server to add.
type NewDBServer struct {
	Name          string
	Host          string
	Port          int // 0: 3306
	AdminUser     string
	AdminPassword string
	TLS           dbserver.TLSMode // empty: verify
	CAPEM         string
}

// DBServerList returns all servers, the local one first.
func (s *Service) DBServerList(ctx context.Context) ([]store.DBServer, error) {
	return s.st.DBServers(ctx)
}

// AddDBServer checks the connection, TLS and privileges and stores the
// server with its sealed admin password. A server that fails the check is
// not stored.
func (s *Service) AddDBServer(ctx context.Context, actor int64, req NewDBServer) (store.DBServer, error) {
	if s.dbs == nil {
		return store.DBServer{}, ErrDBServersOff
	}

	if err := ValidateDBServerName(req.Name); err != nil {
		return store.DBServer{}, err
	}

	if req.Port == 0 {
		req.Port = dbserver.DefaultPort
	}

	if req.TLS == "" {
		req.TLS = dbserver.TLSVerify
	}

	cfg := dbserver.Config{
		Host: req.Host, Port: req.Port, AdminUser: req.AdminUser, AdminPassword: req.AdminPassword,
		TLS: req.TLS, CAPEM: req.CAPEM,
	}
	if err := cfg.Validate(); err != nil {
		return store.DBServer{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if _, err := s.st.DBServerByName(ctx, req.Name); err == nil {
		return store.DBServer{}, ErrDBServerExists
	}

	info, err := s.dbs.Check(ctx, cfg, true)
	if err != nil {
		return store.DBServer{}, fmt.Errorf("%w: %w", ErrDBServerCheck, err)
	}

	d, err := s.st.CreateDBServer(ctx, store.NewDBServer{
		Name: req.Name, Host: req.Host, Port: req.Port, AdminUser: req.AdminUser,
		TLSMode: string(req.TLS), CAPEM: req.CAPEM, Status: statusOf(info, nil, s.now()),
		Seal: func(id int64) ([]byte, error) { return s.dbs.Seal(req.AdminPassword, id) },
	}, s.now())
	if errors.Is(err, store.ErrDBServerExists) {
		return store.DBServer{}, ErrDBServerExists
	}

	if err != nil {
		return store.DBServer{}, err
	}

	s.audit(ctx, actor, "db_server.create", req.Name, map[string]string{
		"host": req.Host, "port": strconv.Itoa(req.Port), "tls": string(req.TLS), "server": info.Display(),
	})

	return d, nil
}

// CheckDBServer connects to an external server again and records what it
// saw. A failed connection is recorded too and is not an error.
func (s *Service) CheckDBServer(ctx context.Context, name string) (store.DBServer, error) {
	srv, err := s.dbServer(ctx, name)
	if err != nil {
		return store.DBServer{}, err
	}

	if srv.Local() {
		return srv, nil
	}

	if s.dbs == nil {
		return store.DBServer{}, ErrDBServersOff
	}

	cfg, err := s.dbs.Config(srv)
	if err != nil {
		return store.DBServer{}, err
	}

	info, cerr := s.dbs.Check(ctx, cfg, false)

	st := statusOf(info, cerr, s.now())
	if cerr != nil {
		// Keep what the server was, so the list still names it.
		st.Engine, st.Version, st.ClientHost = srv.Status.Engine, srv.Status.Version, srv.Status.ClientHost
	}

	if err := s.st.SetDBServerStatus(ctx, name, st); err != nil {
		return store.DBServer{}, err
	}

	return s.dbServer(ctx, name)
}

// SetDefaultDBServer makes name the server the "New database" dialog
// preselects. Existing databases stay where they are.
func (s *Service) SetDefaultDBServer(ctx context.Context, actor int64, name string) error {
	err := s.st.SetDefaultDBServer(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return ErrDBServerNotFound
	}

	if err != nil {
		return err
	}

	s.audit(ctx, actor, "db_server.default", name, nil)

	return nil
}

// DeleteDBServer removes an external server without databases. Nothing on
// the server itself changes.
func (s *Service) DeleteDBServer(ctx context.Context, actor int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	err := s.st.DeleteDBServer(ctx, name)

	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrDBServerNotFound
	case errors.Is(err, store.ErrDBServerInUse):
		return ErrDBServerInUse
	case err != nil:
		return err
	}

	s.audit(ctx, actor, "db_server.delete", name, nil)

	return nil
}

func (s *Service) dbServer(ctx context.Context, name string) (store.DBServer, error) {
	srv, err := s.st.DBServerByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return store.DBServer{}, ErrDBServerNotFound
	}

	return srv, err
}

// statusOf turns a check into the stored status.
func statusOf(info dbserver.Info, err error, now time.Time) store.DBServerStatus {
	st := store.DBServerStatus{
		Engine: info.Engine, Version: info.Version, ClientHost: info.ClientHost, TLSActive: info.TLS, CheckedAt: now,
	}

	if err != nil {
		st.LastError = err.Error()
	}

	return st
}

// remote runs fn against the external server srv.
func (s *Service) remote(ctx context.Context, srv store.DBServer, fn func(RemoteDB) error) error {
	if s.dbs == nil {
		return ErrDBServersOff
	}

	err := s.dbs.With(ctx, srv, fn)

	switch {
	case errors.Is(err, dbserver.ErrExists):
		return ErrDatabaseExists
	default:
		return err
	}
}

// RemoteDBServers is the real DBServers: it seals passwords with the server
// key and connects with internal/dbserver.
type RemoteDBServers struct {
	Box *secrets.Box
}

func dbServerPurpose(id int64) string { return "db-server:" + strconv.FormatInt(id, 10) }

// Check implements DBServers.
func (r RemoteDBServers) Check(ctx context.Context, cfg dbserver.Config, privileges bool) (dbserver.Info, error) {
	db, err := dbserver.Open(cfg)
	if err != nil {
		return dbserver.Info{}, err
	}

	defer db.Close()

	info, err := dbserver.Probe(ctx, db)
	if err != nil {
		return dbserver.Info{}, err
	}

	if !privileges {
		return info, nil
	}

	c, err := dbserver.NewClient(db, info.ClientHost)
	if err != nil {
		return dbserver.Info{}, err
	}

	if err := c.CheckPrivileges(ctx); err != nil {
		return dbserver.Info{}, err
	}

	return info, nil
}

// Seal implements DBServers.
func (r RemoteDBServers) Seal(password string, id int64) ([]byte, error) {
	return r.Box.Seal([]byte(password), dbServerPurpose(id))
}

// Config implements DBServers.
func (r RemoteDBServers) Config(srv store.DBServer) (dbserver.Config, error) {
	pw, err := r.Box.Open(srv.SealedPassword, dbServerPurpose(srv.ID))
	if err != nil {
		return dbserver.Config{}, fmt.Errorf("hosting: open the admin password of %s: %w", srv.Name, err)
	}

	return dbserver.Config{
		Host: srv.Host, Port: srv.Port, AdminUser: srv.AdminUser, AdminPassword: string(pw),
		TLS: dbserver.TLSMode(srv.TLSMode), CAPEM: srv.CAPEM,
	}, nil
}

// With implements DBServers.
func (r RemoteDBServers) With(ctx context.Context, srv store.DBServer, fn func(RemoteDB) error) error {
	cfg, err := r.Config(srv)
	if err != nil {
		return err
	}

	db, err := dbserver.Open(cfg)
	if err != nil {
		return err
	}

	defer db.Close()

	host := srv.Status.ClientHost
	if host == "" {
		info, err := dbserver.Probe(ctx, db)
		if err != nil {
			return err
		}

		host = info.ClientHost
	}

	c, err := dbserver.NewClient(db, host)
	if err != nil {
		return err
	}

	return fn(c)
}
