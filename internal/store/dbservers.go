// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LocalDBServer is the name of the fixed row for the local MariaDB.
const LocalDBServer = "localhost"

// localDBServerID is its id (migration 0029).
const localDBServerID = 1

// TLS mode of the local server, which is reached over the socket.
const DBServerSocket = "socket"

// Errors of the database server table.
var (
	ErrDBServerExists = errors.New("store: a database server with this name exists")
	ErrDBServerInUse  = errors.New("store: the database server has databases or is the local server")
	ErrDBServerFixed  = errors.New("store: the local database server cannot be changed")
)

// DBServer is a database server row (ADR-0041). The password stays sealed;
// only the caller with the server key opens it.
type DBServer struct {
	ID             int64
	Name           string
	Host           string
	Port           int
	AdminUser      string
	SealedPassword []byte
	TLSMode        string
	CAPEM          string
	IsDefault      bool
	CreatedAt      time.Time

	// What the last check saw.
	Status DBServerStatus

	// Databases is how many databases live on the server.
	Databases int
}

// Local reports whether this is the fixed local server.
func (d DBServer) Local() bool { return d.ID == localDBServerID }

// DBServerStatus is the result of a connection check.
type DBServerStatus struct {
	Engine     string
	Version    string
	ClientHost string
	TLSActive  bool
	LastError  string
	CheckedAt  time.Time
}

// NewDBServer is a server to add. Seal is called inside the transaction
// with the new row's id and returns the sealed admin password, so the
// purpose string can name the id.
type NewDBServer struct {
	Name      string
	Host      string
	Port      int
	AdminUser string
	TLSMode   string
	CAPEM     string
	Status    DBServerStatus
	Seal      func(id int64) ([]byte, error)
}

const dbServerCols = `s.id, s.name, s.host, s.port, s.admin_user, s.admin_password, s.tls_mode, s.ca_pem,
	s.client_host, s.engine, s.version, s.tls_active, s.last_error, s.checked_at, s.is_default, s.created_at,
	(SELECT count(*) FROM databases d WHERE d.server_id = s.id)`

func scanDBServer(r rowScanner) (DBServer, error) {
	var (
		d                  DBServer
		tlsActive, isDef   int
		checked, createdAt int64
	)

	err := r.Scan(&d.ID, &d.Name, &d.Host, &d.Port, &d.AdminUser, &d.SealedPassword, &d.TLSMode, &d.CAPEM,
		&d.Status.ClientHost, &d.Status.Engine, &d.Status.Version, &tlsActive, &d.Status.LastError, &checked,
		&isDef, &createdAt, &d.Databases)
	if err != nil {
		return DBServer{}, err
	}

	d.Status.TLSActive = tlsActive == 1
	d.IsDefault = isDef == 1
	d.CreatedAt = time.Unix(createdAt, 0).UTC()

	if checked != 0 {
		d.Status.CheckedAt = time.Unix(checked, 0).UTC()
	}

	return d, nil
}

// DBServers lists all servers, the local one first, then by name.
func (s *Store) DBServers(ctx context.Context) ([]DBServer, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+dbServerCols+` FROM db_servers s ORDER BY s.id != 1, s.name`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []DBServer{}

	for rows.Next() {
		d, err := scanDBServer(rows)
		if err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// DBServerByName returns one server or ErrNotFound.
func (s *Store) DBServerByName(ctx context.Context, name string) (DBServer, error) {
	d, err := scanDBServer(s.db.QueryRowContext(ctx, `SELECT `+dbServerCols+` FROM db_servers s WHERE s.name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return DBServer{}, ErrNotFound
	}

	if err != nil {
		return DBServer{}, fmt.Errorf("store: %w", err)
	}

	return d, nil
}

// DefaultDBServer returns the server the "New database" dialog preselects.
func (s *Store) DefaultDBServer(ctx context.Context) (DBServer, error) {
	d, err := scanDBServer(s.db.QueryRowContext(ctx, `SELECT `+dbServerCols+` FROM db_servers s WHERE s.is_default = 1`))
	if err != nil {
		return DBServer{}, fmt.Errorf("store: %w", err)
	}

	return d, nil
}

// CreateDBServer adds an external server. It is never the default at first.
func (s *Store) CreateDBServer(ctx context.Context, n NewDBServer, now time.Time) (DBServer, error) {
	if n.Seal == nil || n.TLSMode == DBServerSocket {
		return DBServer{}, errors.New("store: an external server needs a sealed password and a TLS mode")
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return DBServer{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM db_servers WHERE name = ?`, n.Name).Scan(&exists); err != nil {
		return DBServer{}, fmt.Errorf("store: %w", err)
	}

	if exists > 0 {
		return DBServer{}, ErrDBServerExists
	}

	// The id is needed for the seal's purpose string: take it first, with
	// a placeholder password the CHECK accepts, and replace it below.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO db_servers (name, host, port, admin_user, admin_password, tls_mode, ca_pem,
			client_host, engine, version, tls_active, last_error, checked_at, created_at)
		VALUES (?, ?, ?, ?, x'', ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		n.Name, n.Host, n.Port, n.AdminUser, n.TLSMode, n.CAPEM,
		n.Status.ClientHost, n.Status.Engine, n.Status.Version, boolInt(n.Status.TLSActive), n.Status.LastError,
		unixOf(n.Status.CheckedAt), now.Unix())
	if err != nil {
		return DBServer{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return DBServer{}, fmt.Errorf("store: %w", err)
	}

	sealed, err := n.Seal(id)
	if err != nil {
		return DBServer{}, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE db_servers SET admin_password = ? WHERE id = ?`, sealed, id); err != nil {
		return DBServer{}, fmt.Errorf("store: %w", err)
	}

	d, err := scanDBServer(tx.QueryRowContext(ctx, `SELECT `+dbServerCols+` FROM db_servers s WHERE s.id = ?`, id))
	if err != nil {
		return DBServer{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return DBServer{}, fmt.Errorf("store: %w", err)
	}

	return d, nil
}

// SetDBServerStatus records a connection check. A missing server is
// ErrNotFound.
func (s *Store) SetDBServerStatus(ctx context.Context, name string, st DBServerStatus) error {
	res, err := s.w.ExecContext(ctx, `
		UPDATE db_servers SET client_host = ?, engine = ?, version = ?, tls_active = ?, last_error = ?, checked_at = ?
		WHERE name = ?`,
		st.ClientHost, st.Engine, st.Version, boolInt(st.TLSActive), st.LastError, unixOf(st.CheckedAt), name)

	return oneRow(res, err)
}

// SetDefaultDBServer makes name the only default.
func (s *Store) SetDefaultDBServer(ctx context.Context, name string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var id int64

	err = tx.QueryRowContext(ctx, `SELECT id FROM db_servers WHERE name = ?`, name).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE db_servers SET is_default = 0 WHERE is_default = 1`); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE db_servers SET is_default = 1 WHERE id = ?`, id); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteDBServer removes an external server without databases. If it was
// the default, the local server becomes the default again.
func (s *Store) DeleteDBServer(ctx context.Context, name string) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var (
		id    int64
		isDef int
	)

	err = tx.QueryRowContext(ctx, `SELECT id, is_default FROM db_servers WHERE name = ?`, name).Scan(&id, &isDef)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM db_servers WHERE id = ?`, id); err != nil {
		if strings.Contains(err.Error(), "database server is in use") {
			return ErrDBServerInUse
		}

		return fmt.Errorf("store: %w", err)
	}

	if isDef == 1 {
		if _, err := tx.ExecContext(ctx, `UPDATE db_servers SET is_default = 1 WHERE id = ?`, localDBServerID); err != nil {
			return fmt.Errorf("store: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

func oneRow(res sql.Result, err error) error {
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n == 0 {
		return ErrNotFound
	}

	return nil
}

func unixOf(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.Unix()
}
