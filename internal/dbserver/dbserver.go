// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dbserver manages the sites' databases and database users on an
// external MariaDB or MySQL server over TCP (ADR-0041). It runs in the panel
// process, which has network access; the agent keeps talking only to the
// local server over its socket (internal/mariadb).
//
// As in internal/mariadb there is no general SQL interface. Every statement
// is built here from names that passed the agentapi validators, and the
// password rule makes string literals safe without escaping, because
// CREATE USER and GRANT take no placeholders.
//
// A Client never logs or returns a statement or the server's message text,
// because both can contain a password.
package dbserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// TLSMode says how the connection to a server is protected.
type TLSMode string

// TLS modes (ADR-0041).
const (
	// TLSVerify requires TLS and checks the certificate against the system
	// roots and the host name. The default.
	TLSVerify TLSMode = "verify"
	// TLSVerifyCA requires TLS and checks the certificate against an
	// uploaded CA and the host name, for servers with a private CA.
	TLSVerifyCA TLSMode = "verify-ca"
	// TLSNone sends everything in plain text, for a trusted private
	// network only.
	TLSNone TLSMode = "none"
)

// Engines a server can run.
const (
	EngineMariaDB = "MariaDB"
	EngineMySQL   = "MySQL"
)

// Errors a caller can act on.
var (
	ErrNotFound    = errors.New("dbserver: unknown database or user")
	ErrExists      = errors.New("dbserver: database or user already exists")
	ErrUnavailable = errors.New("dbserver: server not reachable")
	ErrInvalid     = errors.New("dbserver: invalid configuration, name or password")
	ErrAccess      = errors.New("dbserver: access denied")
	ErrUnsupported = errors.New("dbserver: unsupported server version")
)

// Limits of the configuration fields.
const (
	MaxHostLen     = 253
	MaxAdminUser   = 80
	MaxAdminPwLen  = 256
	MaxCAPEMLen    = 64 << 10
	DefaultPort    = 3306
	connectTimeout = 5 * time.Second
	ioTimeout      = 30 * time.Second
)

// hostRe accepts a DNS name; IP addresses are checked with net.ParseIP.
var hostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// Config describes how to reach one server as its administrator.
type Config struct {
	Host      string
	Port      int
	AdminUser string
	// AdminPassword never reaches a statement: the driver sends it in the
	// handshake.
	AdminPassword string
	TLS           TLSMode
	// CAPEM is the CA for TLSVerifyCA, empty otherwise.
	CAPEM string

	// OnCertificate, if set, sees the server's leaf certificate of every
	// TLS connection after it passed the checks; a probe uses it to note
	// when the certificate expires.
	OnCertificate func(*x509.Certificate) `json:"-"`
}

// Validate checks the configuration without connecting.
func (c Config) Validate() error {
	if err := ValidateHost(c.Host); err != nil {
		return err
	}

	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("%w: port must be 1 to 65535", ErrInvalid)
	}

	if c.AdminUser == "" || len(c.AdminUser) > MaxAdminUser || !printable(c.AdminUser) {
		return fmt.Errorf("%w: admin user must have 1 to %d printable characters", ErrInvalid, MaxAdminUser)
	}

	// Printable ASCII, so the password also fits the option file that
	// mariadb-dump reads for backups (ClientOptions).
	if c.AdminPassword == "" || len(c.AdminPassword) > MaxAdminPwLen || !printable(c.AdminPassword) {
		return fmt.Errorf("%w: admin password must have 1 to %d printable ASCII characters", ErrInvalid, MaxAdminPwLen)
	}

	switch c.TLS {
	case TLSVerify, TLSNone:
		if c.CAPEM != "" {
			return fmt.Errorf("%w: a CA certificate needs TLS mode %q", ErrInvalid, TLSVerifyCA)
		}
	case TLSVerifyCA:
		if _, err := caPool(c.CAPEM); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: TLS mode must be %q, %q or %q", ErrInvalid, TLSVerify, TLSVerifyCA, TLSNone)
	}

	return nil
}

// ValidateHost accepts a lower-case DNS name or an IP address.
func ValidateHost(host string) error {
	if host == "" || len(host) > MaxHostLen {
		return fmt.Errorf("%w: host must have 1 to %d characters", ErrInvalid, MaxHostLen)
	}

	if net.ParseIP(host) != nil || hostRe.MatchString(host) {
		return nil
	}

	return fmt.Errorf("%w: host must be a lower-case DNS name or an IP address", ErrInvalid)
}

func printable(s string) bool {
	for i := range len(s) {
		if s[i] < 0x20 || s[i] > 0x7e {
			return false
		}
	}

	return true
}

func caPool(pemData string) (*x509.CertPool, error) {
	if pemData == "" || len(pemData) > MaxCAPEMLen {
		return nil, fmt.Errorf("%w: TLS mode %q needs a PEM CA certificate of at most %d bytes", ErrInvalid, TLSVerifyCA, MaxCAPEMLen)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(pemData)) {
		return nil, fmt.Errorf("%w: the CA certificate is not valid PEM", ErrInvalid)
	}

	return pool, nil
}

// tlsConfig returns the client TLS configuration, nil for TLSNone.
func (c Config) tlsConfig() (*tls.Config, error) {
	var tc *tls.Config

	switch c.TLS {
	case TLSNone:
		return nil, nil //nolint:nilnil // nil means plain text to the driver
	case TLSVerify:
		tc = &tls.Config{ServerName: c.Host, MinVersion: tls.VersionTLS12}
	case TLSVerifyCA:
		pool, err := caPool(c.CAPEM)
		if err != nil {
			return nil, err
		}

		tc = &tls.Config{ServerName: c.Host, RootCAs: pool, MinVersion: tls.VersionTLS12}
	default:
		return nil, fmt.Errorf("%w: unknown TLS mode", ErrInvalid)
	}

	if seen := c.OnCertificate; seen != nil {
		// Runs after the normal verification, which stays on.
		tc.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) > 0 {
				seen(cs.PeerCertificates[0])
			}

			return nil
		}
	}

	return tc, nil
}

// Open returns a small pool for the server. It does not connect yet.
func Open(c Config) (*sql.DB, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}

	tc, err := c.tlsConfig()
	if err != nil {
		return nil, err
	}

	mc := mysql.NewConfig()
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
	mc.User = c.AdminUser
	mc.Passwd = c.AdminPassword
	mc.Timeout = connectTimeout
	mc.ReadTimeout = ioTimeout
	mc.WriteTimeout = ioTimeout
	mc.TLS = tc

	// Without TLS, MySQL 8's caching_sha2_password makes the driver fetch
	// the server's RSA key for a full login. The path is trusted by
	// definition then (ADR-0041), so that adds no new risk.

	conn, err := mysql.NewConnector(mc)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	db := sql.OpenDB(conn)
	db.SetMaxOpenConns(2)
	db.SetConnMaxIdleTime(time.Minute)

	return db, nil
}

// Execer runs one statement. *sql.DB implements it; tests use a fake.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// Querier reads one row. *sql.DB implements it.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Client runs the typed operations on one server. It holds no state: which
// databases belong to Hostashi is the panel store's business.
type Client struct {
	db Execer
	// host is the address the server sees for this host, used as the host
	// part of every database user (ADR-0041: not "%").
	host string
}

// NewClient uses db and creates database users for clientHost, the address
// the server sees for this host (Info.ClientHost).
func NewClient(db Execer, clientHost string) (*Client, error) {
	if err := ValidateClientHost(clientHost); err != nil {
		return nil, err
	}

	return &Client{db: db, host: clientHost}, nil
}

// ValidateClientHost accepts the host part of a database user: an IP
// address or a DNS name. "%" and "_" are wildcards there, so neither may
// appear.
func ValidateClientHost(host string) error {
	if host == "" || len(host) > MaxHostLen {
		return fmt.Errorf("%w: client host must have 1 to %d characters", ErrInvalid, MaxHostLen)
	}

	if net.ParseIP(host) != nil {
		return nil
	}

	if hostRe.MatchString(strings.ToLower(host)) {
		return nil
	}

	return fmt.Errorf("%w: client host must be an IP address or a DNS name", ErrInvalid)
}

// Info describes a server as Probe saw it.
type Info struct {
	Engine  string // EngineMariaDB or EngineMySQL
	Version string // for example "11.8.6"
	// ClientHost is the address the server sees for this host.
	ClientHost string
	// TLS reports whether the connection is encrypted.
	TLS bool
	// CertExpires is when the server certificate ends, if the caller set
	// Config.OnCertificate; zero otherwise.
	CertExpires time.Time
}

// Display returns for example "MariaDB 11.8.6".
func (i Info) Display() string { return i.Engine + " " + i.Version }

// Probe connects and reads the engine, version, this host's address as the
// server sees it, and whether the connection is encrypted. It rejects MySQL
// before 8.0 and MariaDB before 10.5.
func Probe(ctx context.Context, db Querier) (Info, error) {
	var version, user string
	if err := db.QueryRowContext(ctx, "SELECT VERSION(), USER()").Scan(&version, &user); err != nil {
		return Info{}, classify("probe", err)
	}

	engine, ver, err := ParseVersion(version)
	if err != nil {
		return Info{}, err
	}

	host := user
	if i := strings.LastIndexByte(user, '@'); i >= 0 {
		host = user[i+1:]
	}

	if err := ValidateClientHost(host); err != nil {
		return Info{}, err
	}

	var name, cipher string

	err = db.QueryRowContext(ctx, "SHOW SESSION STATUS LIKE 'Ssl_cipher'").Scan(&name, &cipher)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Info{}, classify("tls status", err)
	}

	return Info{Engine: engine, Version: ver, ClientHost: host, TLS: cipher != ""}, nil
}

// ParseVersion splits VERSION() into engine and version and checks the
// minimum: MariaDB 10.5, MySQL 8.0.
func ParseVersion(v string) (engine, version string, err error) {
	num, rest, _ := strings.Cut(v, "-")

	parts := strings.Split(num, ".")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("%w: cannot read version %q", ErrUnsupported, v)
	}

	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])

	if err1 != nil || err2 != nil {
		return "", "", fmt.Errorf("%w: cannot read version %q", ErrUnsupported, v)
	}

	if strings.Contains(rest, "MariaDB") {
		if major < 10 || (major == 10 && minor < 5) {
			return "", "", fmt.Errorf("%w: MariaDB %s is older than 10.5", ErrUnsupported, num)
		}

		return EngineMariaDB, num, nil
	}

	if major < 8 {
		return "", "", fmt.Errorf("%w: MySQL %s is older than 8.0", ErrUnsupported, num)
	}

	// mariadb-dump, which the backup uses, takes a server version of 10.3
	// or later for MariaDB and asks for Oracle-mode packages, which MySQL
	// 26 and later rejects, so the dump fails [V: mariadb-dump 11.8.6
	// against MySQL 26.10.0, 2026-11-21].
	if major >= 10 {
		return "", "", fmt.Errorf("%w: MySQL %s cannot be backed up with mariadb-dump yet; MySQL 8.0 to 9.x are supported", ErrUnsupported, num)
	}

	return EngineMySQL, num, nil
}

// probePrefix names the database and user CheckPrivileges creates. Site
// databases cannot start with "hostashi" (agentapi), so they never collide.
const probePrefix = "hostashi_probe_"

// CheckPrivileges creates a database and a user with all privileges on it
// and drops both again, which is exactly what Hostashi will do later. A
// failure names the step that failed.
func (c *Client) CheckPrivileges(ctx context.Context) error {
	suffix := strings.ToLower(agentapi.GenerateDBPassword()[:8])
	name := probePrefix + suffix
	db := "`" + name + "`"
	grant := strings.ReplaceAll(db, "_", `\_`)
	acct := "'" + name[:min(len(name), 32)] + "'@'" + c.host + "'"
	pw := "'" + agentapi.GenerateDBPassword() + "'"

	if err := c.exec(ctx, "create database", "CREATE DATABASE "+db+" CHARACTER SET utf8mb4"); err != nil {
		return err
	}

	defer c.undo(ctx, "DROP DATABASE IF EXISTS "+db)

	if err := c.exec(ctx, "create user", "CREATE USER "+acct+" IDENTIFIED BY "+pw); err != nil {
		return err
	}

	defer c.undo(ctx, "DROP USER IF EXISTS "+acct)

	if err := c.exec(ctx, "grant", "GRANT ALL PRIVILEGES ON "+grant+".* TO "+acct); err != nil {
		return err
	}

	if err := c.exec(ctx, "drop user", "DROP USER "+acct); err != nil {
		return err
	}

	return c.exec(ctx, "drop database", "DROP DATABASE "+db)
}

// CreateDatabase creates an empty database. An existing one is ErrExists,
// never adopted.
func (c *Client) CreateDatabase(ctx context.Context, name string) error {
	ident, err := quoteName(name)
	if err != nil {
		return err
	}

	return c.exec(ctx, "create database", "CREATE DATABASE "+ident+" CHARACTER SET utf8mb4")
}

// DropDatabase drops the database; a missing one is not an error.
func (c *Client) DropDatabase(ctx context.Context, name string) error {
	ident, err := quoteName(name)
	if err != nil {
		return err
	}

	return c.exec(ctx, "drop database", "DROP DATABASE IF EXISTS "+ident)
}

// CreateUser creates a user with all privileges on database and nothing
// else, who can log in only from this host. An existing account is
// ErrExists.
func (c *Client) CreateUser(ctx context.Context, database, user, password string) error {
	if err := (agentapi.DBUserCreateRequest{Database: database, User: user, Password: password}).Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return c.createUser(ctx, database, user, password)
}

// CreateJobUser creates the temporary user of an import or export
// (ADR-0041 addendum 1), with the same rights as CreateUser.
func (c *Client) CreateJobUser(ctx context.Context, database, user, password string) error {
	if err := agentapi.ValidateDBName(database); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := agentapi.ValidateRemoteJobUser(user, password); err != nil || password == "" {
		return fmt.Errorf("%w: job user", ErrInvalid)
	}

	return c.createUser(ctx, database, user, password)
}

// DropJobUser drops the temporary user of a job; a missing one is not an
// error.
func (c *Client) DropJobUser(ctx context.Context, user string) error {
	if err := agentapi.ValidateRemoteJobUser(user, ""); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return c.exec(ctx, "drop user", "DROP USER IF EXISTS "+c.account(user))
}

// CreateToolUser creates the temporary user of a web database tool session
// (ADR-0041 addendum 1): the rights of CreateUser and at most
// agentapi.DBToolMaxConnections connections, as on the local server.
func (c *Client) CreateToolUser(ctx context.Context, database, user, password string) error {
	if err := agentapi.ValidateDBName(database); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if !agentapi.ValidDBToolUser(user) || !agentapi.ValidRemotePassword(password) {
		return fmt.Errorf("%w: tool user", ErrInvalid)
	}

	return c.createUserWith(ctx, database, user, password, fmt.Sprintf(" WITH MAX_USER_CONNECTIONS %d", agentapi.DBToolMaxConnections))
}

// DropToolUser drops the temporary user of a tool session; a missing one
// is not an error. On MariaDB its open connections end too; MySQL has no
// statement for that, there a running query ends on its own.
func (c *Client) DropToolUser(ctx context.Context, user string) error {
	if !agentapi.ValidDBToolUser(user) {
		return fmt.Errorf("%w: tool user", ErrInvalid)
	}

	if err := c.exec(ctx, "drop user", "DROP USER IF EXISTS "+c.account(user)); err != nil {
		return err
	}

	// KILL ... USER is MariaDB only; MySQL refuses it as a syntax error.
	c.undo(ctx, "KILL HARD CONNECTION USER "+c.account(user))

	return nil
}

func (c *Client) createUser(ctx context.Context, database, user, password string) error {
	return c.createUserWith(ctx, database, user, password, "")
}

func (c *Client) createUserWith(ctx context.Context, database, user, password, with string) error {
	acct := c.account(user)
	grantOn := strings.ReplaceAll("`"+database+"`", "_", `\_`)

	if err := c.exec(ctx, "create user", "CREATE USER "+acct+" IDENTIFIED BY '"+password+"'"+with); err != nil {
		return err
	}

	if err := c.exec(ctx, "grant", "GRANT ALL PRIVILEGES ON "+grantOn+".* TO "+acct); err != nil {
		c.undo(ctx, "DROP USER IF EXISTS "+acct)

		return err
	}

	return nil
}

// DropUser drops a user; a missing one is not an error.
func (c *Client) DropUser(ctx context.Context, user string) error {
	if err := agentapi.ValidateDBUser(user); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return c.exec(ctx, "drop user", "DROP USER IF EXISTS "+c.account(user))
}

// SetPassword replaces a user's password.
func (c *Client) SetPassword(ctx context.Context, user, password string) error {
	if err := (agentapi.DBUserPasswordRequest{User: user, Password: password}).Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return c.exec(ctx, "set password", "ALTER USER "+c.account(user)+" IDENTIFIED BY '"+password+"'")
}

// account returns 'user'@'host' for a validated user.
func (c *Client) account(user string) string {
	return "'" + user + "'@'" + c.host + "'"
}

func quoteName(name string) (string, error) {
	if err := agentapi.ValidateDBName(name); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return "`" + name + "`", nil
}

func (c *Client) exec(ctx context.Context, step, stmt string) error {
	if _, err := c.db.ExecContext(ctx, stmt); err != nil {
		return classify(step, err)
	}

	return nil
}

// undo runs a compensating statement; its error is dropped because the
// caller already returns the first one.
func (c *Client) undo(ctx context.Context, stmt string) {
	_ = c.exec(context.WithoutCancel(ctx), "undo", stmt)
}

// Server error numbers, the same in MariaDB and MySQL.
const (
	errDBCreateExists = 1007 // ER_DB_CREATE_EXISTS
	errCannotUser     = 1396 // ER_CANNOT_USER
	errDBAccess       = 1044 // ER_DBACCESS_DENIED_ERROR
	errAccessDenied   = 1045 // ER_ACCESS_DENIED_ERROR
	errSpecificAccess = 1227 // ER_SPECIFIC_ACCESS_DENIED_ERROR
	errTableAccess    = 1142 // ER_TABLEACCESS_DENIED_ERROR
	errNoGrantOption  = 1410 // ER_CANT_CREATE_USER_WITH_GRANT
)

func classify(step string, err error) error {
	var me *mysql.MySQLError
	if errors.As(err, &me) {
		switch me.Number {
		case errDBCreateExists, errCannotUser:
			return fmt.Errorf("%w (%s, server error %d)", ErrExists, step, me.Number)
		case errDBAccess, errAccessDenied, errSpecificAccess, errTableAccess, errNoGrantOption:
			return fmt.Errorf("%w (%s, server error %d)", ErrAccess, step, me.Number)
		default:
			return fmt.Errorf("dbserver: %s: server error %d", step, me.Number)
		}
	}

	var ce *tls.CertificateVerificationError
	if errors.As(err, &ce) {
		return fmt.Errorf("%w (%s): TLS certificate not trusted", ErrUnavailable, step)
	}

	var ne net.Error
	if errors.As(err, &ne) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EHOSTUNREACH) ||
		errors.Is(err, mysql.ErrInvalidConn) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w (%s)", ErrUnavailable, step)
	}

	return fmt.Errorf("dbserver: %s: %w", step, err)
}

// SystemCAFile is the CA bundle of Debian and Ubuntu, which TLSVerify uses
// for mariadb-dump; the Go client uses the same system roots.
const SystemCAFile = "/etc/ssl/certs/ca-certificates.crt"

// ClientOptions returns a [client] section for mariadb-dump's
// --defaults-extra-file with the connection and the password, so neither
// appears on a command line. caFile holds CAPEM for TLSVerifyCA; it is
// ignored otherwise. The caller writes the text to a file of mode 0600.
func (c Config) ClientOptions(caFile string) (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}

	var b strings.Builder

	b.WriteString("[client]\n")
	b.WriteString("host=" + c.Host + "\n")
	b.WriteString("port=" + strconv.Itoa(c.Port) + "\n")
	b.WriteString("user=" + optionValue(c.AdminUser) + "\n")
	b.WriteString("password=" + optionValue(c.AdminPassword) + "\n")

	switch c.TLS {
	case TLSNone:
		b.WriteString("skip-ssl\n")
	case TLSVerify:
		b.WriteString("ssl\nssl-verify-server-cert\nssl-ca=" + SystemCAFile + "\n")
	case TLSVerifyCA:
		if caFile == "" || !filepath.IsAbs(caFile) || strings.ContainsAny(caFile, "\n\"'\\#") {
			return "", fmt.Errorf("%w: CA file path", ErrInvalid)
		}

		b.WriteString("ssl\nssl-verify-server-cert\nssl-ca=" + caFile + "\n")
	}

	return b.String(), nil
}

// optionValue quotes a printable value for an option file: inside double
// quotes, backslash and double quote are escaped; "#" and spaces are then
// part of the value.
func optionValue(v string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)

	return `"` + r.Replace(v) + `"`
}
