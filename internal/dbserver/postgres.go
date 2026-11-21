// SPDX-License-Identifier: AGPL-3.0-or-later

package dbserver

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// PostgreSQL servers (ADR-0051). The model on the server:
//
//   - Every database has a group role OwnerRole(name) without LOGIN that
//     owns it. CONNECT and TEMPORARY are revoked from PUBLIC, so only
//     members of that role reach the database.
//   - Every database user is a LOGIN role that is a member of the owner
//     role and starts its sessions as it (ALTER ROLE ... SET role), so all
//     users of a database share its objects as on MariaDB.
//   - The administrator is made a member of every role it creates, which
//     PostgreSQL 16 and later need to create a database for another owner
//     and to end that role's sessions.
//
// Passwords leave the panel only as SCRAM-SHA-256 verifiers, so the server
// log never sees a plain password even with log_statement = 'all'.

// EnginePostgreSQL is the engine of a PostgreSQL server.
const EnginePostgreSQL = "PostgreSQL"

// DefaultPostgresPort is PostgreSQL's port.
const DefaultPostgresPort = 5432

// MinPostgresMajor is the oldest supported major version (ADR-0051).
const MinPostgresMajor = 14

// PostgresMaintenanceDB is the database the administrator connects to.
// [A] Every PostgreSQL server has it, managed offers included.
const PostgresMaintenanceDB = "postgres"

// maxPGIdent is PostgreSQL's identifier limit (NAMEDATALEN - 1); longer
// names would be cut silently.
const maxPGIdent = 63

// IsPostgres reports whether engine is PostgreSQL.
func IsPostgres(engine string) bool { return engine == EnginePostgreSQL }

// DefaultPortFor returns the default port of engine.
func DefaultPortFor(engine string) int {
	if IsPostgres(engine) {
		return DefaultPostgresPort
	}

	return DefaultPort
}

// Names PostgreSQL keeps for itself, on top of the agentapi rules.
var (
	pgReservedDatabases = map[string]bool{"postgres": true, "template0": true, "template1": true}
	pgReservedRoles     = map[string]bool{
		"postgres": true, "public": true, "none": true,
		"current_user": true, "current_role": true, "session_user": true, "user": true,
	}
)

// ValidatePostgresDBName accepts a database name for a PostgreSQL server.
func ValidatePostgresDBName(name string) error {
	if err := agentapi.ValidateDBName(name); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if len(name) > maxPGIdent || pgReservedDatabases[name] || strings.HasPrefix(name, "pg_") {
		return fmt.Errorf("%w: database %q is reserved or longer than %d characters on PostgreSQL", ErrInvalid, name, maxPGIdent)
	}

	return nil
}

// ValidatePostgresUser accepts a database user for a PostgreSQL server.
func ValidatePostgresUser(name string) error {
	if err := agentapi.ValidateDBUser(name); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if pgReservedRoles[name] || strings.HasPrefix(name, "pg_") {
		return fmt.Errorf("%w: user %q is reserved on PostgreSQL", ErrInvalid, name)
	}

	return nil
}

// OwnerRole is the group role that owns database. Site users cannot take
// names starting with "hostashi", and the hash keeps the name short.
func OwnerRole(database string) string {
	sum := sha256.Sum256([]byte(database))

	return "hostashi_db_" + hex.EncodeToString(sum[:8])
}

// pgIdent quotes a name that passed a validator; validated names hold no
// double quote, so quoting needs no escaping.
func pgIdent(name string) string { return `"` + name + `"` }

// openPostgres returns a small pool for a PostgreSQL server on database.
func openPostgres(c Config, database string) (*sql.DB, error) {
	pc, err := postgresConfig(c, database)
	if err != nil {
		return nil, err
	}

	db := stdlib.OpenDB(*pc)
	db.SetMaxOpenConns(2)
	db.SetConnMaxIdleTime(time.Minute)

	return db, nil
}

// postgresConfig builds the pgx configuration. sslmode=disable in the
// string keeps PGSSLMODE and friends from adding fallbacks; every other
// field that the environment, ~/.pgpass or a service file can set is
// overwritten, so the panel's environment changes nothing.
func postgresConfig(c Config, database string) (*pgx.ConnConfig, error) {
	tc, err := c.tlsConfig()
	if err != nil {
		return nil, err
	}

	pc, err := pgx.ParseConfig("sslmode=disable")
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	pc.Host, pc.Port, pc.Database = c.Host, uint16(c.Port), database //nolint:gosec // Validate keeps the port in range
	pc.User, pc.Password = c.AdminUser, c.AdminPassword
	pc.TLSConfig, pc.Fallbacks = tc, nil
	pc.SSLNegotiation = ""
	pc.ConnectTimeout = connectTimeout
	pc.KerberosSrvName, pc.KerberosSpn = "", ""
	pc.RuntimeParams = map[string]string{
		"application_name":  "hostashi",
		"statement_timeout": strconv.Itoa(int(ioTimeout.Milliseconds())),
	}

	return pc, nil
}

// ProbePostgres reads a PostgreSQL server like Probe: version, this host's
// address as the server sees it, TLS, and whether the administrator may
// create databases and roles. It rejects versions before 14 and an
// administrator without CREATEDB or CREATEROLE.
func ProbePostgres(ctx context.Context, db Querier) (Info, error) {
	var (
		num, host                string
		ssl, super, mkdb, mkrole bool
	)

	err := db.QueryRowContext(ctx, `SELECT current_setting('server_version_num'),
		coalesce(host(inet_client_addr()), ''),
		coalesce((SELECT ssl FROM pg_stat_ssl WHERE pid = pg_backend_pid()), false),
		r.rolsuper, r.rolcreatedb, r.rolcreaterole
		FROM pg_roles r WHERE r.rolname = current_user`).Scan(&num, &host, &ssl, &super, &mkdb, &mkrole)
	if err != nil {
		return Info{}, classifyPG("probe", err)
	}

	ver, err := ParsePostgresVersion(num)
	if err != nil {
		return Info{}, err
	}

	if err := ValidateClientHost(host); err != nil {
		return Info{}, err
	}

	if !super && (!mkdb || !mkrole) {
		return Info{}, fmt.Errorf("%w (probe): the administrator needs CREATEDB and CREATEROLE", ErrAccess)
	}

	return Info{Engine: EnginePostgreSQL, Version: ver, ClientHost: host, TLS: ssl, Superuser: super}, nil
}

// ParsePostgresVersion turns server_version_num (for example "170006")
// into "17.6" and checks the minimum.
func ParsePostgresVersion(num string) (string, error) {
	n, err := strconv.Atoi(num)
	if err != nil || n <= 0 {
		return "", fmt.Errorf("%w: cannot read version %q", ErrUnsupported, num)
	}

	major, minor := n/10000, n%10000
	if major < MinPostgresMajor {
		return "", fmt.Errorf("%w: PostgreSQL %d is older than %d", ErrUnsupported, major, MinPostgresMajor)
	}

	return strconv.Itoa(major) + "." + strconv.Itoa(minor), nil
}

// PostgresMajor returns the major version of a Version from ProbePostgres,
// 0 if it cannot be read.
func PostgresMajor(version string) int {
	m, _, _ := strings.Cut(version, ".")
	n, _ := strconv.Atoi(m)

	return n
}

// PGClient runs the typed operations on one PostgreSQL server. Like
// Client it holds no state of its own.
type PGClient struct {
	db Execer
	// in runs fn on a connection to another database of the server.
	in func(ctx context.Context, database string, fn func(Execer) error) error
	// Now is the clock for VALID UNTIL; nil means time.Now.
	Now func() time.Time
}

func (c *PGClient) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}

	return time.Now()
}

// NewPGClient uses db, a connection to the maintenance database, and cfg
// for connections to single databases.
func NewPGClient(db Execer, cfg Config) *PGClient {
	return &PGClient{db: db, in: func(_ context.Context, database string, fn func(Execer) error) error {
		d, err := openPostgres(cfg, database)
		if err != nil {
			return err
		}

		defer d.Close()

		return fn(d)
	}}
}

// CheckPrivileges does once what Hostashi does later: a database with its
// owner role and a user, then drops all of it. A failure names the step.
func (c *PGClient) CheckPrivileges(ctx context.Context) error {
	suffix := strings.ToLower(agentapi.GenerateDBPassword()[:8])
	name := probePrefix + suffix

	if err := c.createDatabase(ctx, name); err != nil {
		return err
	}

	defer c.undo(ctx, "DROP DATABASE IF EXISTS "+pgIdent(name))
	defer c.undo(ctx, "DROP ROLE IF EXISTS "+pgIdent(OwnerRole(name)))

	user := name + "_u"
	if err := c.createRole(ctx, name, user, agentapi.GenerateDBPassword(), ""); err != nil {
		return err
	}

	defer c.undo(ctx, "DROP ROLE IF EXISTS "+pgIdent(user))

	if err := c.dropRole(ctx, name, user); err != nil {
		return err
	}

	return c.dropDatabase(ctx, name)
}

// CreateDatabase creates the database and its owner role. An existing one
// is ErrExists, never adopted.
func (c *PGClient) CreateDatabase(ctx context.Context, name string) error {
	if err := ValidatePostgresDBName(name); err != nil {
		return err
	}

	return c.createDatabase(ctx, name)
}

func (c *PGClient) createDatabase(ctx context.Context, name string) error {
	owner, db := pgIdent(OwnerRole(name)), pgIdent(name)

	if err := c.exec(ctx, "create owner role", "CREATE ROLE "+owner+" NOLOGIN"); err != nil {
		return err
	}

	steps := []struct{ step, stmt string }{
		{"grant owner role", "GRANT " + owner + " TO CURRENT_USER"},
		{"create database", "CREATE DATABASE " + db + " OWNER " + owner + " ENCODING 'UTF8' TEMPLATE template0"},
		{"revoke public", "REVOKE CONNECT, TEMPORARY ON DATABASE " + db + " FROM PUBLIC"},
	}

	for _, s := range steps {
		if err := c.exec(ctx, s.step, s.stmt); err != nil {
			if s.step != "grant owner role" {
				c.undo(ctx, "DROP DATABASE IF EXISTS "+db)
			}

			c.undo(ctx, "DROP ROLE IF EXISTS "+owner)

			return err
		}
	}

	return nil
}

// DropDatabase ends the sessions on the database, drops it and its owner
// role; a missing one is not an error.
func (c *PGClient) DropDatabase(ctx context.Context, name string) error {
	if err := ValidatePostgresDBName(name); err != nil {
		return err
	}

	return c.dropDatabase(ctx, name)
}

func (c *PGClient) dropDatabase(ctx context.Context, name string) error {
	if err := c.exec(ctx, "drop database", "DROP DATABASE IF EXISTS "+pgIdent(name)+" WITH (FORCE)"); err != nil {
		return err
	}

	return c.exec(ctx, "drop owner role", "DROP ROLE IF EXISTS "+pgIdent(OwnerRole(name)))
}

// CreateUser creates a login role for database with the rights of its
// owner and nothing else. An existing role is ErrExists.
func (c *PGClient) CreateUser(ctx context.Context, database, user, password string) error {
	if err := (agentapi.DBUserCreateRequest{Database: database, User: user, Password: password}).Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := ValidatePostgresDBName(database); err != nil {
		return err
	}

	if err := ValidatePostgresUser(user); err != nil {
		return err
	}

	return c.createRole(ctx, database, user, password, "")
}

func (c *PGClient) createRole(ctx context.Context, database, user, password, with string) error {
	verifier, err := scramVerifier(password)
	if err != nil {
		return err
	}

	role, owner := pgIdent(user), pgIdent(OwnerRole(database))

	// IN ROLE fails with "role does not exist" for an unknown database.
	if err := c.exec(ctx, "create user", "CREATE ROLE "+role+" LOGIN PASSWORD '"+verifier+"'"+with+" IN ROLE "+owner); err != nil {
		return err
	}

	for _, s := range [][2]string{
		{"grant user role", "GRANT " + role + " TO CURRENT_USER"},
		{"set role", "ALTER ROLE " + role + " SET role = '" + OwnerRole(database) + "'"},
	} {
		if err := c.exec(ctx, s[0], s[1]); err != nil {
			c.undo(ctx, "DROP ROLE IF EXISTS "+role)

			return err
		}
	}

	return nil
}

// DropUser ends the user's sessions, hands what it owns in database to the
// owner role and drops it; a missing user is not an error.
func (c *PGClient) DropUser(ctx context.Context, database, user string) error {
	if err := ValidatePostgresDBName(database); err != nil {
		return err
	}

	if err := agentapi.ValidateDBUser(user); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return c.dropRole(ctx, database, user)
}

func (c *PGClient) dropRole(ctx context.Context, database, user string) error {
	role := pgIdent(user)

	// Login first off, so no new session starts while the old ones end.
	err := c.exec(ctx, "lock user", "ALTER ROLE "+role+" NOLOGIN")
	if errors.Is(err, ErrNotFound) {
		return nil
	}

	if err != nil {
		return err
	}

	if _, err := c.db.ExecContext(ctx, "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE usename = $1", user); err != nil {
		return classifyPG("end sessions", err)
	}

	// Objects the user made after RESET ROLE belong to it; they stay in
	// the database, owned by the owner role.
	err = c.in(ctx, database, func(db Execer) error {
		owner := pgIdent(OwnerRole(database))
		if _, err := db.ExecContext(ctx, "REASSIGN OWNED BY "+role+" TO "+owner); err != nil {
			return classifyPG("reassign", err)
		}

		if _, err := db.ExecContext(ctx, "DROP OWNED BY "+role); err != nil {
			return classifyPG("drop owned", err)
		}

		return nil
	})
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}

	return c.exec(ctx, "drop user", "DROP ROLE IF EXISTS "+role)
}

// SetPassword replaces a user's password.
func (c *PGClient) SetPassword(ctx context.Context, user, password string) error {
	if err := (agentapi.DBUserPasswordRequest{User: user, Password: password}).Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	verifier, err := scramVerifier(password)
	if err != nil {
		return err
	}

	return c.exec(ctx, "set password", "ALTER ROLE "+pgIdent(user)+" PASSWORD '"+verifier+"'")
}

// ErrPostgresNotYet: what PostgreSQL servers do not support yet (ADR-0051).
var ErrPostgresNotYet = fmt.Errorf("%w: not available for PostgreSQL yet", ErrUnsupported)

// JobRoleValidFor is how long the password of a job's temporary role is
// valid: the job unit ends after 6 hours, the panel drops the role after
// 6 hours 15 minutes at the latest.
const JobRoleValidFor = 6*time.Hour + 30*time.Minute

// JobRoleConnections is the connection limit of a job's role: pg_dump and
// pg_restore use one, psql one.
const JobRoleConnections = 2

// CreateJobUser creates the temporary role of an import or export
// (ADR-0051): a member of the database's owner role like CreateUser, with
// at most JobRoleConnections connections and a password that expires
// after JobRoleValidFor.
func (c *PGClient) CreateJobUser(ctx context.Context, database, user, password string) error {
	if err := ValidatePostgresDBName(database); err != nil {
		return err
	}

	if err := agentapi.ValidateRemoteJobUser(user, password); err != nil || password == "" {
		return fmt.Errorf("%w: job user", ErrInvalid)
	}

	until := c.now().Add(JobRoleValidFor).UTC().Format("2006-01-02 15:04:05+00")

	return c.createRole(ctx, database, user, password, fmt.Sprintf(" CONNECTION LIMIT %d VALID UNTIL '%s'", JobRoleConnections, until))
}

// DropJobUser drops the temporary role of a job like DropUser; a missing
// role is not an error.
func (c *PGClient) DropJobUser(ctx context.Context, database, user string) error {
	if err := ValidatePostgresDBName(database); err != nil {
		return err
	}

	if err := agentapi.ValidateRemoteJobUser(user, ""); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return c.dropRole(ctx, database, user)
}

// CreateToolUser creates the temporary user of a web database tool session
// (ADR-0051): a member of the database's owner role like any user of it,
// with at most agentapi.DBToolMaxConnections connections.
func (c *PGClient) CreateToolUser(ctx context.Context, database, user, password string) error {
	if err := ValidatePostgresDBName(database); err != nil {
		return err
	}

	if !agentapi.ValidDBToolUser(user) || !agentapi.ValidRemotePassword(password) {
		return fmt.Errorf("%w: tool user", ErrInvalid)
	}

	return c.createRole(ctx, database, user, password, fmt.Sprintf(" CONNECTION LIMIT %d", agentapi.DBToolMaxConnections))
}

// DropToolUser ends the sessions of a tool session's user and drops it
// like DropUser; a missing one is not an error.
func (c *PGClient) DropToolUser(ctx context.Context, database, user string) error {
	if err := ValidatePostgresDBName(database); err != nil {
		return err
	}

	if !agentapi.ValidDBToolUser(user) {
		return fmt.Errorf("%w: tool user", ErrInvalid)
	}

	return c.dropRole(ctx, database, user)
}

func (c *PGClient) exec(ctx context.Context, step, stmt string) error {
	if _, err := c.db.ExecContext(ctx, stmt); err != nil {
		return classifyPG(step, err)
	}

	return nil
}

func (c *PGClient) undo(ctx context.Context, stmt string) {
	_ = c.exec(context.WithoutCancel(ctx), "undo", stmt)
}

// scramIterations is PostgreSQL's own default (scram_iterations).
const scramIterations = 4096

// scramVerifier returns the SCRAM-SHA-256 verifier PostgreSQL stores for
// password (RFC 5802, RFC 7677). The passwords here are printable ASCII, for
// which SASLprep changes nothing. The result holds only base64 and "$:",
// so it is safe in a string literal.
func scramVerifier(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}

	return scramVerifierWith(password, salt, scramIterations)
}

func scramVerifierWith(password string, salt []byte, iterations int) (string, error) {
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		return "", err
	}

	mac := func(key []byte, msg string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(msg))

		return h.Sum(nil)
	}

	stored := sha256.Sum256(mac(salted, "Client Key"))
	server := mac(salted, "Server Key")
	b64 := base64.StdEncoding.EncodeToString

	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iterations, b64(salt), b64(stored[:]), b64(server)), nil
}

// SQLSTATE codes classifyPG maps.
const (
	pgDuplicateDatabase = "42P04"
	pgDuplicateObject   = "42710"
	pgInsufficientPriv  = "42501"
	pgInvalidPassword   = "28P01"
	pgInvalidAuth       = "28000"
	pgUndefinedObject   = "42704"
	pgInvalidCatalog    = "3D000"
)

// classifyPG maps a pgx error like classify; it keeps only the SQLSTATE,
// never the server's message.
func classifyPG(step string, err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case pgDuplicateDatabase, pgDuplicateObject:
			return fmt.Errorf("%w (%s, SQLSTATE %s)", ErrExists, step, pe.Code)
		case pgInsufficientPriv, pgInvalidPassword, pgInvalidAuth:
			return fmt.Errorf("%w (%s, SQLSTATE %s)", ErrAccess, step, pe.Code)
		case pgUndefinedObject, pgInvalidCatalog:
			return fmt.Errorf("%w (%s, SQLSTATE %s)", ErrNotFound, step, pe.Code)
		default:
			return fmt.Errorf("dbserver: %s: SQLSTATE %s", step, pe.Code)
		}
	}

	var ce *tls.CertificateVerificationError
	if errors.As(err, &ce) {
		return fmt.Errorf("%w (%s): TLS certificate not trusted", ErrUnavailable, step)
	}

	var (
		ne  net.Error
		cne *pgconn.ConnectError
	)

	if errors.As(err, &ne) || errors.As(err, &cne) || errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.EHOSTUNREACH) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w (%s)", ErrUnavailable, step)
	}

	return fmt.Errorf("dbserver: %s: %w", step, err)
}
