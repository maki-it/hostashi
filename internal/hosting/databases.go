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
	Site     string
	Name     string
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

	d, err := s.st.CreateDatabase(ctx, req.Site, req.Name, s.now())
	if errors.Is(err, store.ErrDatabaseExists) {
		return store.Database{}, "", ErrDatabaseExists
	}

	if err != nil {
		return store.Database{}, "", err
	}

	if err := s.ag.DBCreate(ctx, req.Site, req.Name); err != nil {
		s.forgetDatabase(ctx, req.Name)

		return store.Database{}, "", err
	}

	s.audit(ctx, actor, "database.create", req.Name, map[string]string{"site": req.Site})

	if req.User == "" {
		return d, "", nil
	}

	if err := s.createUser(ctx, actor, req.Name, req.User, pw); err != nil {
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

	if err := s.ag.DBDelete(ctx, name); err != nil && !isCode(err, agentrpc.CodeNotFound) {
		return err
	}

	if err := s.st.DeleteDatabase(ctx, name); err != nil {
		return err
	}

	s.audit(ctx, actor, "database.delete", name, map[string]string{"site": d.Site})

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

	if _, err := s.st.DatabaseByName(ctx, database); errors.Is(err, store.ErrNotFound) {
		return "", ErrDatabaseNotFound
	} else if err != nil {
		return "", err
	}

	if err := s.createUser(ctx, actor, database, user, pw); err != nil {
		return "", err
	}

	if generated {
		return pw, nil
	}

	return "", nil
}

func (s *Service) createUser(ctx context.Context, actor int64, database, user, pw string) error {
	err := s.st.CreateDatabaseUser(ctx, database, user, s.now())
	if errors.Is(err, store.ErrDatabaseUserExists) {
		return ErrDatabaseExists
	}

	if err != nil {
		return err
	}

	if err := s.ag.DBUserCreate(ctx, database, user, pw); err != nil {
		if derr := s.st.DeleteDatabaseUser(context.WithoutCancel(ctx), user); derr != nil {
			s.log.Error("remove database user row after failed create", "user", user, "err", derr)
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

	if err := s.ag.DBUserDelete(ctx, user); err != nil && !isCode(err, agentrpc.CodeNotFound) {
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

	if err := s.ag.DBUserPassword(ctx, user, pw); err != nil {
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
		s.log.Error("remove database row after failed create", "database", name, "err", err)
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
