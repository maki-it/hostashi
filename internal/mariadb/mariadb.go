// SPDX-License-Identifier: AGPL-3.0-or-later

// Package mariadb creates and drops the sites' databases and database users
// on the local MariaDB server. It runs in the agent and talks to the server
// over its Unix socket as root, which MariaDB on Debian and Ubuntu
// authenticates through the unix_socket plugin, so no administrator
// password exists anywhere.
//
// There is no general SQL interface. Every statement is built here from
// names that passed the agentapi validators and is quoted again on the way
// in. MariaDB takes no placeholders in CREATE USER and GRANT, so the rules
// for names and passwords are what keeps the statements safe.
//
// The agent manages only databases and users that it created itself and
// recorded in its registry. A database or user that already exists on the
// server is never adopted, changed or dropped.
package mariadb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// DefaultSocket is the server socket of the Debian and Ubuntu packages.
const DefaultSocket = "/run/mysqld/mysqld.sock"

// userHost is the only host database users get: the Unix socket, which is
// what PHP uses for DB_HOST "localhost". Remote access is a later feature.
const userHost = "localhost"

// Errors a caller can act on.
var (
	ErrNotFound    = errors.New("mariadb: unknown database or user")
	ErrExists      = errors.New("mariadb: database or user already exists")
	ErrUnavailable = errors.New("mariadb: server not reachable")
	ErrInvalid     = errors.New("mariadb: invalid name or password")
)

// Execer runs one statement. *sql.DB implements it; tests use a fake.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Options configure a Manager.
type Options struct {
	// OwnerUID must own the state directory: 0 on a server, the test user in
	// unprivileged tests.
	OwnerUID uint32
	Log      *slog.Logger
}

// Manager serializes all changes, because each one touches the server and
// the registry together.
type Manager struct {
	mu  sync.Mutex
	db  Execer
	reg *registry
	log *slog.Logger

	// Import and export (jobs.go); nil until EnableDumps.
	dumps *Dumps
	jobs  map[string]*agentapi.DBJob
	busy  map[string]bool
	done  map[string]chan struct{}
}

// Connect opens a pool to the server at socket as root. It does not connect
// yet: a server that is not installed or not running only fails the calls.
func Connect(socket string) *sql.DB {
	c := mysql.NewConfig()
	c.Net = "unix"
	c.Addr = socket
	c.User = "root"
	c.Timeout = 5 * time.Second
	c.ReadTimeout = 60 * time.Second
	c.WriteTimeout = 60 * time.Second

	conn, err := mysql.NewConnector(c)
	if err != nil {
		// NewConnector only fails on an invalid configuration, and this
		// one is fixed.
		panic(err)
	}

	db := sql.OpenDB(conn)
	db.SetMaxOpenConns(2)
	db.SetConnMaxIdleTime(time.Minute)

	return db
}

// New loads the registry from stateDir and uses db for the statements.
func New(stateDir string, db Execer, opt Options) (*Manager, error) {
	reg, err := openRegistry(stateDir, opt.OwnerUID)
	if err != nil {
		return nil, err
	}

	if opt.Log == nil {
		opt.Log = slog.New(slog.DiscardHandler)
	}

	return &Manager{db: db, reg: reg, log: opt.Log}, nil
}

// List returns all registered databases sorted by name.
func (m *Manager) List() []Database {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.reg.list()
}

// SiteHasDatabases reports whether site owns at least one database.
func (m *Manager) SiteHasDatabases(site string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, d := range m.reg.dbs {
		if d.Site == site {
			return true
		}
	}

	return false
}

// Create creates an empty database for site. A database of that name that
// exists on the server but not in the registry is an ErrExists, never
// adopted.
func (m *Manager) Create(ctx context.Context, site, name string) (Database, error) {
	if err := (agentapi.DBCreateRequest{Site: site, Name: name}).Validate(); err != nil {
		return Database{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.reg.dbs[name]; ok {
		return Database{}, ErrExists
	}

	ident, err := quoteName(name)
	if err != nil {
		return Database{}, err
	}

	// Without IF NOT EXISTS: an existing database must fail here.
	if err := m.exec(ctx, "create database", "CREATE DATABASE "+ident+" CHARACTER SET utf8mb4"); err != nil {
		return Database{}, err
	}

	before := m.reg.snapshot()
	d := &Database{Name: name, Site: site, Users: []string{}}
	m.reg.dbs[name] = d

	if err := m.reg.commit(before); err != nil {
		m.undo(ctx, "DROP DATABASE IF EXISTS "+ident)

		return Database{}, err
	}

	return Database{Name: name, Site: site, Users: []string{}}, nil
}

// Delete drops the database and all its users.
func (m *Manager) Delete(ctx context.Context, name string) error {
	if err := agentapi.ValidateDBName(name); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.reg.dbs[name]
	if !ok {
		return ErrNotFound
	}

	if m.busy[name] {
		return ErrBusy
	}

	// Users first: they are useless without the database, and IF EXISTS
	// makes a retry after a partial failure safe.
	for _, u := range d.Users {
		acct, err := quoteAccount(u)
		if err != nil {
			return err
		}

		if err := m.exec(ctx, "drop user", "DROP USER IF EXISTS "+acct); err != nil {
			return err
		}
	}

	ident, err := quoteName(name)
	if err != nil {
		return err
	}

	if err := m.exec(ctx, "drop database", "DROP DATABASE IF EXISTS "+ident); err != nil {
		return err
	}

	before := m.reg.snapshot()

	for _, u := range d.Users {
		delete(m.reg.users, u)
	}

	delete(m.reg.dbs, name)

	return m.reg.commit(before)
}

// CreateUser creates a user that has all privileges on database and nothing
// else, and can log in only through the local socket.
func (m *Manager) CreateUser(ctx context.Context, database, user, password string) error {
	if err := (agentapi.DBUserCreateRequest{Database: database, User: user, Password: password}).Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	d, ok := m.reg.dbs[database]
	if !ok {
		return ErrNotFound
	}

	if _, ok := m.reg.users[user]; ok {
		return ErrExists
	}

	acct, err := quoteAccount(user)
	if err != nil {
		return err
	}

	pw, err := quotePassword(password)
	if err != nil {
		return err
	}

	grantOn, err := quoteGrantName(database)
	if err != nil {
		return err
	}

	// Without IF NOT EXISTS: an existing account must fail here.
	if err := m.exec(ctx, "create user", "CREATE USER "+acct+" IDENTIFIED BY "+pw); err != nil {
		return err
	}

	if err := m.exec(ctx, "grant", "GRANT ALL PRIVILEGES ON "+grantOn+".* TO "+acct); err != nil {
		m.undo(ctx, "DROP USER IF EXISTS "+acct)

		return err
	}

	before := m.reg.snapshot()

	d.Users = append(d.Users, user)
	m.reg.users[user] = database

	if err := m.reg.commit(before); err != nil {
		m.undo(ctx, "DROP USER IF EXISTS "+acct)

		return err
	}

	return nil
}

// DeleteUser drops a user.
func (m *Manager) DeleteUser(ctx context.Context, user string) error {
	if err := agentapi.ValidateDBUser(user); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	database, ok := m.reg.users[user]
	if !ok {
		return ErrNotFound
	}

	acct, err := quoteAccount(user)
	if err != nil {
		return err
	}

	if err := m.exec(ctx, "drop user", "DROP USER IF EXISTS "+acct); err != nil {
		return err
	}

	before := m.reg.snapshot()
	d := m.reg.dbs[database]

	kept := d.Users[:0]
	for _, u := range d.Users {
		if u != user {
			kept = append(kept, u)
		}
	}

	d.Users = kept

	delete(m.reg.users, user)

	return m.reg.commit(before)
}

// SetPassword replaces the password of a registered user.
func (m *Manager) SetPassword(ctx context.Context, user, password string) error {
	if err := (agentapi.DBUserPasswordRequest{User: user, Password: password}).Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.reg.users[user]; !ok {
		return ErrNotFound
	}

	acct, err := quoteAccount(user)
	if err != nil {
		return err
	}

	pw, err := quotePassword(password)
	if err != nil {
		return err
	}

	return m.exec(ctx, "set password", "ALTER USER "+acct+" IDENTIFIED BY "+pw)
}

// exec runs one statement. The returned error never contains the statement
// or the server's message, because both can contain a password: only the
// step and the server's error number reach the log.
func (m *Manager) exec(ctx context.Context, step, stmt string) error {
	if _, err := m.db.ExecContext(ctx, stmt); err != nil {
		return classify(step, err)
	}

	return nil
}

// undo runs a compensating statement and only logs a failure.
func (m *Manager) undo(ctx context.Context, stmt string) {
	if err := m.exec(context.WithoutCancel(ctx), "undo", stmt); err != nil {
		m.log.ErrorContext(ctx, "mariadb: undo failed, the server may keep an object the registry does not list", "err", err)
	}
}

// MariaDB error numbers that mean "already exists".
const (
	errDBCreateExists = 1007 // ER_DB_CREATE_EXISTS
	errCannotUser     = 1396 // ER_CANNOT_USER, CREATE USER of an existing account
)

func classify(step string, err error) error {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case errDBCreateExists, errCannotUser:
			return fmt.Errorf("%w (%s, server error %d)", ErrExists, step, me.Number)
		default:
			return fmt.Errorf("mariadb: %s: server error %d", step, me.Number)
		}
	}

	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EACCES) {
		return fmt.Errorf("%w (%s)", ErrUnavailable, step)
	}

	return fmt.Errorf("mariadb: %s: %w", step, err)
}

// quoteName returns a database name as a quoted identifier. The validator
// already excludes backticks; the check here keeps that true even if a
// caller skipped it.
func quoteName(name string) (string, error) {
	if err := agentapi.ValidateDBName(name); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return "`" + name + "`", nil
}

// quoteGrantName quotes a database name for GRANT. There, "_" and "%" are
// LIKE wildcards: a grant on `shop_wp` would also cover a database
// "shopxwp" of another site. Names cannot contain "%", "_" is escaped.
func quoteGrantName(name string) (string, error) {
	q, err := quoteName(name)
	if err != nil {
		return "", err
	}

	return strings.ReplaceAll(q, "_", `\_`), nil
}

// quoteAccount returns 'user'@'localhost'.
func quoteAccount(user string) (string, error) {
	if err := agentapi.ValidateDBUser(user); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return "'" + user + "'@'" + userHost + "'", nil
}

// quotePassword returns the password as a string literal. The validator
// excludes quotes and backslashes, so no escaping is needed, independent of
// the server's sql_mode.
func quotePassword(pw string) (string, error) {
	if err := agentapi.ValidateDBPassword(pw); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return "'" + pw + "'", nil
}
