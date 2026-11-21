// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of the database functions.
var (
	ErrDatabaseNotFound = errors.New("hosting: unknown database or database user")
	ErrDatabaseExists   = errors.New("hosting: a database or database user with this name exists")
)

// DBCreate calls db.create.
func (a RPCAgent) DBCreate(ctx context.Context, site, name string) error {
	_, err := agentapi.DBCreate.Call(ctx, a.C, agentapi.DBCreateRequest{Site: site, Name: name})

	return err
}

// DBDelete calls db.delete.
func (a RPCAgent) DBDelete(ctx context.Context, name string) error {
	_, err := agentapi.DBDelete.Call(ctx, a.C, agentapi.DBDeleteRequest{Name: name})

	return err
}

// DBUserCreate calls db.user.create.
func (a RPCAgent) DBUserCreate(ctx context.Context, database, user, password string) error {
	_, err := agentapi.DBUserCreate.Call(ctx, a.C, agentapi.DBUserCreateRequest{Database: database, User: user, Password: password})

	return err
}

// DBUserDelete calls db.user.delete.
func (a RPCAgent) DBUserDelete(ctx context.Context, user string) error {
	_, err := agentapi.DBUserDelete.Call(ctx, a.C, agentapi.DBUserDeleteRequest{User: user})

	return err
}

// DBUserPassword calls db.user.password.
func (a RPCAgent) DBUserPassword(ctx context.Context, user, password string) error {
	_, err := agentapi.DBUserPassword.Call(ctx, a.C, agentapi.DBUserPasswordRequest{User: user, Password: password})

	return err
}

// Databases lists the databases of site, or of all sites for "".
func (s *Service) Databases(ctx context.Context, site string) ([]store.Database, error) {
	if site != "" {
		if _, err := s.Get(ctx, site); err != nil {
			return nil, err
		}
	}

	return s.st.Databases(ctx, site)
}

// Database returns one database.
func (s *Service) Database(ctx context.Context, name string) (store.Database, error) {
	d, err := s.st.DatabaseByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return store.Database{}, ErrDatabaseNotFound
	}

	return d, err
}

// NewDatabase describes a database to create, optionally with its first
// user. An empty Password with a User generates one.
type NewDatabase struct {
	Site string
	Name string
	// Server is the database server's name; empty: the default server.
	Server   string
	User     string
	Password string
}

// CreateDatabase creates the database and, if asked, its first user. It
// returns the password when it generated one; the panel stores none.
func (s *Service) CreateDatabase(ctx context.Context, actor int64, req NewDatabase) (store.Database, string, error) {
	if err := (agentapi.DBCreateRequest{Site: req.Site, Name: req.Name}).Validate(); err != nil {
		return store.Database{}, "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	pw, generated, err := passwordFor(req.User, req.Password)
	if err != nil {
		return store.Database{}, "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, req.Site)
	if err != nil {
		return store.Database{}, "", err
	}

	if site.State != store.SiteActive {
		return store.Database{}, "", ErrState
	}

	if req.User != "" {
		if _, err := s.st.DatabaseOfUser(ctx, req.User); err == nil {
			return store.Database{}, "", ErrDatabaseExists
		}
	}

	be, srv, err := s.backendFor(ctx, req.Server)
	if err != nil {
		return store.Database{}, "", err
	}

	d, err := s.st.CreateDatabaseOn(ctx, req.Site, req.Name, srv, s.now())
	if errors.Is(err, store.ErrDatabaseExists) {
		return store.Database{}, "", ErrDatabaseExists
	}

	if err != nil {
		return store.Database{}, "", err
	}

	if err := be.create(ctx, req.Site, req.Name); err != nil {
		s.forgetDatabase(ctx, req.Name)

		return store.Database{}, "", err
	}

	s.audit(ctx, actor, "database.create", req.Name, map[string]string{"site": req.Site, "server": srv})

	if srv != store.LocalDBServer {
		s.dbManifestChanged(ctx)
	}

	if req.User == "" {
		return d, "", nil
	}

	if err := s.createUser(ctx, actor, be, req.Name, req.User, pw); err != nil {
		// The database is kept: it was created and is listed. The caller
		// sees why the user is missing and can add one.
		return d, "", err
	}

	d.Users = []string{req.User}

	if generated {
		return d, pw, nil
	}

	return d, "", nil
}

// DeleteDatabase drops the database and its users.
func (s *Service) DeleteDatabase(ctx context.Context, actor int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.deleteDatabase(ctx, actor, name)
}

func (s *Service) deleteDatabase(ctx context.Context, actor int64, name string) error {
	d, err := s.st.DatabaseByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return ErrDatabaseNotFound
	}

	if err != nil {
		return err
	}

	be, _, err := s.backendFor(ctx, d.Server)
	if err != nil {
		return err
	}

	if err := be.drop(ctx, name, d.Users); err != nil {
		return err
	}

	if err := s.st.DeleteDatabase(ctx, name); err != nil {
		return err
	}

	s.audit(ctx, actor, "database.delete", name, map[string]string{"site": d.Site, "server": d.Server})

	if d.Server != store.LocalDBServer {
		s.dbManifestChanged(ctx)
	}

	return nil
}

// CreateDatabaseUser adds a user with all privileges on database. It
// returns the password when it generated one.
func (s *Service) CreateDatabaseUser(ctx context.Context, actor int64, database, user, password string) (string, error) {
	if user == "" {
		return "", fmt.Errorf("%w: user is required", ErrInvalid)
	}

	pw, generated, err := passwordFor(user, password)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	be, err := s.backendOf(ctx, database)
	if err != nil {
		return "", err
	}

	if err := s.createUser(ctx, actor, be, database, user, pw); err != nil {
		return "", err
	}

	if generated {
		return pw, nil
	}

	return "", nil
}

func (s *Service) createUser(ctx context.Context, actor int64, be dbBackend, database, user, pw string) error {
	err := s.st.CreateDatabaseUser(ctx, database, user, s.now())
	if errors.Is(err, store.ErrDatabaseUserExists) {
		return ErrDatabaseExists
	}

	if err != nil {
		return err
	}

	if err := be.createUser(ctx, database, user, pw); err != nil {
		if derr := s.st.DeleteDatabaseUser(context.WithoutCancel(ctx), user); derr != nil {
			s.log.ErrorContext(ctx, "remove database user row after failed create", "user", user, "err", derr)
		}

		return err
	}

	s.audit(ctx, actor, "database.user.create", user, map[string]string{"database": database})

	return nil
}

// DeleteDatabaseUser drops a user of database.
func (s *Service) DeleteDatabaseUser(ctx context.Context, actor int64, database, user string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.userOf(ctx, database, user); err != nil {
		return err
	}

	be, err := s.backendOf(ctx, database)
	if err != nil {
		return err
	}

	if err := be.dropUser(ctx, user); err != nil {
		return err
	}

	if err := s.st.DeleteDatabaseUser(ctx, user); err != nil {
		return err
	}

	s.audit(ctx, actor, "database.user.delete", user, map[string]string{"database": database})

	return nil
}

// SetDatabaseUserPassword sets a new password, generated if empty, and
// returns the generated one.
func (s *Service) SetDatabaseUserPassword(ctx context.Context, actor int64, database, user, password string) (string, error) {
	pw, generated, err := passwordFor(user, password)
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.userOf(ctx, database, user); err != nil {
		return "", err
	}

	be, err := s.backendOf(ctx, database)
	if err != nil {
		return "", err
	}

	if err := be.setPassword(ctx, user, pw); err != nil {
		return "", err
	}

	s.audit(ctx, actor, "database.user.password", user, map[string]string{"database": database})

	if generated {
		return pw, nil
	}

	return "", nil
}

// userOf checks that user belongs to database.
func (s *Service) userOf(ctx context.Context, database, user string) error {
	got, err := s.st.DatabaseOfUser(ctx, user)
	if errors.Is(err, store.ErrNotFound) || (err == nil && got != database) {
		return ErrDatabaseNotFound
	}

	return err
}

// deleteSiteDatabases drops all databases of a site; part of site deletion.
func (s *Service) deleteSiteDatabases(ctx context.Context, actor int64, site string) error {
	dbs, err := s.st.Databases(ctx, site)
	if err != nil {
		return err
	}

	for _, d := range dbs {
		if err := s.deleteDatabase(ctx, actor, d.Name); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) forgetDatabase(ctx context.Context, name string) {
	if err := s.st.DeleteDatabase(context.WithoutCancel(ctx), name); err != nil {
		s.log.ErrorContext(ctx, "remove database row after failed create", "database", name, "err", err)
	}
}

// passwordFor validates a given password or generates one. Without a user
// no password is needed.
func passwordFor(user, password string) (pw string, generated bool, err error) {
	if user == "" {
		if password != "" {
			return "", false, fmt.Errorf("%w: a password needs a user", ErrInvalid)
		}

		return "", false, nil
	}

	if err := agentapi.ValidateDBUser(user); err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if password == "" {
		return agentapi.GenerateDBPassword(), true, nil
	}

	if err := agentapi.ValidateDBPassword(password); err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return password, false, nil
}

// dbBackend runs the database operations on the server a database lives
// on: the agent for the local server, the panel process for an external
// one (ADR-0041).
type dbBackend interface {
	create(ctx context.Context, site, name string) error
	drop(ctx context.Context, name string, users []string) error
	createUser(ctx context.Context, database, user, password string) error
	dropUser(ctx context.Context, user string) error
	setPassword(ctx context.Context, user, password string) error
}

// backendFor returns the backend of the server named server ("" for the
// default) and the server's name.
func (s *Service) backendFor(ctx context.Context, server string) (dbBackend, string, error) {
	var (
		srv store.DBServer
		err error
	)

	if server == "" {
		srv, err = s.st.DefaultDBServer(ctx)
	} else {
		srv, err = s.dbServer(ctx, server)
	}

	if err != nil {
		return nil, "", err
	}

	if srv.Local() {
		return agentDB{s.ag}, srv.Name, nil
	}

	if s.dbs == nil {
		return nil, "", ErrDBServersOff
	}

	return remoteBackend{s: s, srv: srv}, srv.Name, nil
}

// backendOf returns the backend of an existing database.
func (s *Service) backendOf(ctx context.Context, database string) (dbBackend, error) {
	d, err := s.st.DatabaseByName(ctx, database)
	if errors.Is(err, store.ErrNotFound) {
		return nil, ErrDatabaseNotFound
	}

	if err != nil {
		return nil, err
	}

	be, _, err := s.backendFor(ctx, d.Server)

	return be, err
}

// agentDB is the local server through the agent, which keeps its own
// registry and drops a database's users with it.
type agentDB struct{ ag Agent }

func (a agentDB) create(ctx context.Context, site, name string) error {
	return a.ag.DBCreate(ctx, site, name)
}

func (a agentDB) drop(ctx context.Context, name string, _ []string) error {
	if err := a.ag.DBDelete(ctx, name); err != nil && !isCode(err, agentrpc.CodeNotFound) {
		return err
	}

	return nil
}

func (a agentDB) createUser(ctx context.Context, database, user, password string) error {
	return a.ag.DBUserCreate(ctx, database, user, password)
}

func (a agentDB) dropUser(ctx context.Context, user string) error {
	if err := a.ag.DBUserDelete(ctx, user); err != nil && !isCode(err, agentrpc.CodeNotFound) {
		return err
	}

	return nil
}

func (a agentDB) setPassword(ctx context.Context, user, password string) error {
	return a.ag.DBUserPassword(ctx, user, password)
}

// remoteBackend is an external server. The panel store is the registry:
// only databases and users recorded there are ever dropped.
type remoteBackend struct {
	s   *Service
	srv store.DBServer
}

func (r remoteBackend) create(ctx context.Context, _, name string) error {
	return r.s.remote(ctx, r.srv, func(c RemoteDB) error { return c.CreateDatabase(ctx, name) })
}

func (r remoteBackend) drop(ctx context.Context, name string, users []string) error {
	// Open tool sessions end first, so no temporary user keeps rights on
	// a database created later under the same name.
	if err := r.s.endRemoteTools(ctx, r.srv, name); err != nil {
		return err
	}

	return r.s.remote(ctx, r.srv, func(c RemoteDB) error {
		// Users first, as on the local server: IF EXISTS makes a retry
		// after a partial failure safe.
		for _, u := range users {
			if err := c.DropUser(ctx, u); err != nil {
				return err
			}
		}

		return c.DropDatabase(ctx, name)
	})
}

func (r remoteBackend) createUser(ctx context.Context, database, user, password string) error {
	return r.s.remote(ctx, r.srv, func(c RemoteDB) error { return c.CreateUser(ctx, database, user, password) })
}

func (r remoteBackend) dropUser(ctx context.Context, user string) error {
	return r.s.remote(ctx, r.srv, func(c RemoteDB) error { return c.DropUser(ctx, user) })
}

func (r remoteBackend) setPassword(ctx context.Context, user, password string) error {
	return r.s.remote(ctx, r.srv, func(c RemoteDB) error { return c.SetPassword(ctx, user, password) })
}
