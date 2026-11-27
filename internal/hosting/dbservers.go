// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/doctor"
	"github.com/maki-it/hostashi/internal/extdb"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of the database server functions (ADR-0041).
var (
	ErrDBServerNotFound = errors.New("hosting: unknown database server")
	ErrDBServerExists   = errors.New("hosting: a database server with this name exists")
	ErrDBServerInUse    = errors.New("hosting: the database server has databases or is the local server")
	ErrDBServersOff     = errors.New("hosting: external database servers are not available")
	// ErrDBServerLocalOnly: loading a dump as the owner works only on the
	// local server (ADR-0041 addendum 1).
	ErrDBServerLocalOnly = errors.New("hosting: this works only for databases on the local server so far")
	// ErrPostgreSQLNotYet: what does not work for PostgreSQL databases
	// yet comes with later steps of ADR-0051.
	ErrPostgreSQLNotYet = errors.New("hosting: this does not work for PostgreSQL databases yet")
	// ErrDBServerCheck wraps why a server failed the connection check.
	ErrDBServerCheck = errors.New("hosting: the database server failed the check")
)

// RemoteDB is the typed operations on one external server; dbserver.Client
// implements it.
type RemoteDB interface {
	CreateDatabase(ctx context.Context, name string) error
	DropDatabase(ctx context.Context, name string) error
	CreateUser(ctx context.Context, database, user, password string) error
	// DropUser drops a user of database; PostgreSQL needs the database
	// to hand over what the user owned there.
	DropUser(ctx context.Context, database, user string) error
	SetPassword(ctx context.Context, user, password string) error
	// The temporary user of an import or export (ADR-0041 addendum 1).
	CreateJobUser(ctx context.Context, database, user, password string) error
	DropJobUser(ctx context.Context, database, user string) error
	// The temporary user of a web database tool session.
	CreateToolUser(ctx context.Context, database, user, password string, until time.Time) error
	// ExtendToolUser moves the end of a tool user's password (PostgreSQL);
	// a no-op on MariaDB and MySQL.
	ExtendToolUser(ctx context.Context, user string, until time.Time) error
	DropToolUser(ctx context.Context, database, user string) error
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

// EnableDBServers turns on external database servers. manifestDir is where
// the list for the backup goes (extdb); empty writes none, for tests.
func (s *Service) EnableDBServers(d DBServers, manifestDir string) {
	s.dbs, s.dbManifestDir = d, manifestDir
}

// WriteDBManifest writes the external servers and their databases for the
// backup (extdb). The panel calls it at start; every change of a server or
// an external database calls it too.
func (s *Service) WriteDBManifest(ctx context.Context) error {
	if s.dbManifestDir == "" {
		return nil
	}

	s.manifestMu.Lock()
	defer s.manifestMu.Unlock()

	servers, err := s.st.DBServers(ctx)
	if err != nil {
		return err
	}

	dbs, err := s.st.Databases(ctx, "")
	if err != nil {
		return err
	}

	m := extdb.Manifest{}

	for _, srv := range servers {
		if !srv.Local() {
			m.Servers = append(m.Servers, extdb.Server{
				Engine: engineOf(srv), ID: srv.ID, Name: srv.Name, Host: srv.Host, Port: srv.Port, AdminUser: srv.AdminUser,
				SealedPassword: srv.SealedPassword, TLSMode: srv.TLSMode, CAPEM: srv.CAPEM,
			})
		}
	}

	for _, d := range dbs {
		if d.Server != store.LocalDBServer {
			m.Databases = append(m.Databases, extdb.Database{Name: d.Name, Site: d.Site, Server: d.Server})
		}
	}

	return extdb.Write(s.dbManifestDir, m)
}

// dbManifestChanged rewrites the manifest after a change and logs a
// failure: the change itself stands, and the next change or start writes
// the file again. Until then the backup misses the new state.
func (s *Service) dbManifestChanged(ctx context.Context) {
	if err := s.WriteDBManifest(context.WithoutCancel(ctx)); err != nil {
		s.log.ErrorContext(ctx, "write the external database list for the backup", "err", err)
	}
}

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
	// Engine is dbserver.EnginePostgreSQL for PostgreSQL; empty for
	// MariaDB or MySQL, which the check tells apart.
	Engine        string
	Name          string
	Host          string
	Port          int // 0: the engine's default port
	AdminUser     string
	AdminPassword string
	TLS           dbserver.TLSMode // empty: verify
	CAPEM         string
}

// DBServerList returns all servers, the local one first.
func (s *Service) DBServerList(ctx context.Context) ([]store.DBServer, error) {
	return s.st.DBServers(ctx)
}

// PGClientTools installs the PostgreSQL client tools through the agent
// (ADR-0051); serve.RPCPGClient implements it.
type PGClientTools interface {
	Install(ctx context.Context, major string) (agentapi.PGClientStatus, error)
	Status(ctx context.Context) (agentapi.PGClientStatus, error)
}

// EnablePGClient lets adding a PostgreSQL server install the client tools.
func (s *Service) EnablePGClient(p PGClientTools) { s.pgClient = p }

// ErrPGClientTooOld: the server's PostgreSQL is newer than every client
// major this Hostashi release can install, so pg_dump could not back it up.
var ErrPGClientTooOld = errors.New("hosting: the PostgreSQL server is newer than the client tools this Hostashi release installs")

// PGClientInstallWarning is AddDBServer's warning while the client tools
// are missing or older than the new server.
const PGClientInstallWarning = "the PostgreSQL client tools for backups, import and export are being installed; this takes a few minutes"

// checkPGClient refuses a server newer than the newest client major and
// starts installing the newest client when none installed covers the
// server. It returns a warning while that runs. pgx needs no client, so
// the server itself works at once.
func (s *Service) checkPGClient(ctx context.Context, version string) (string, error) {
	major := dbserver.PostgresMajor(version)

	newest, err := strconv.Atoi(agentapi.PGClientNewest())
	if err != nil {
		return "", err
	}

	if major > newest {
		return "", fmt.Errorf("%w: PostgreSQL %d, newest client %d; update Hostashi first", ErrPGClientTooOld, major, newest)
	}

	if s.pgClient == nil {
		return "", nil
	}

	st, err := s.pgClient.Status(ctx)
	if err != nil {
		return "", fmt.Errorf("hosting: read the PostgreSQL client tools: %w", err)
	}

	for _, m := range st.Installed {
		if n, err := strconv.Atoi(m); err == nil && n >= major {
			return "", nil
		}
	}

	if st.State == agentapi.PHPInstallRunning {
		return PGClientInstallWarning, nil
	}

	if _, err := s.pgClient.Install(ctx, ""); err != nil {
		var ae *agentrpc.Error
		// Refused because a run started or finished meanwhile.
		if !errors.As(err, &ae) || ae.Code != agentrpc.CodePrecondition {
			return "", fmt.Errorf("hosting: install the PostgreSQL client tools: %w", err)
		}
	}

	return PGClientInstallWarning, nil
}

// SuperuserWarning is what AddDBServer warns about a PostgreSQL
// administrator with SUPERUSER (ADR-0051).
const SuperuserWarning = "the administrator is a PostgreSQL superuser; a dedicated role with only CREATEDB and CREATEROLE limits the damage if the panel is compromised"

// AddDBServer checks the connection, TLS and privileges and stores the
// server with its sealed admin password. A server that fails the check is
// not stored. The warnings say what works but is not recommended.
func (s *Service) AddDBServer(ctx context.Context, actor int64, req NewDBServer) (store.DBServer, []string, error) {
	d, info, pgWarning, err := s.addDBServer(ctx, actor, req)
	if err != nil {
		return store.DBServer{}, nil, err
	}

	var warnings []string
	if info.Superuser {
		warnings = append(warnings, SuperuserWarning)
	}

	if pgWarning != "" {
		warnings = append(warnings, pgWarning)
	}

	return d, warnings, nil
}

func (s *Service) addDBServer(ctx context.Context, actor int64, req NewDBServer) (store.DBServer, dbserver.Info, string, error) {
	var none dbserver.Info

	if s.dbs == nil {
		return store.DBServer{}, none, "", ErrDBServersOff
	}

	if err := ValidateDBServerName(req.Name); err != nil {
		return store.DBServer{}, none, "", err
	}

	if req.Engine != "" && !dbserver.IsPostgres(req.Engine) {
		return store.DBServer{}, none, "", fmt.Errorf("%w: engine must be empty (MariaDB or MySQL) or %q", ErrInvalid, dbserver.EnginePostgreSQL)
	}

	if req.Port == 0 {
		req.Port = dbserver.DefaultPortFor(req.Engine)
	}

	if req.TLS == "" {
		req.TLS = dbserver.TLSVerify
	}

	cfg := dbserver.Config{
		Engine: req.Engine, Host: req.Host, Port: req.Port, AdminUser: req.AdminUser, AdminPassword: req.AdminPassword,
		TLS: req.TLS, CAPEM: req.CAPEM,
	}
	if err := cfg.Validate(); err != nil {
		return store.DBServer{}, none, "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if _, err := s.st.DBServerByName(ctx, req.Name); err == nil {
		return store.DBServer{}, none, "", ErrDBServerExists
	}

	info, err := s.dbs.Check(ctx, cfg, true)
	if err != nil {
		return store.DBServer{}, none, "", fmt.Errorf("%w: %w", ErrDBServerCheck, err)
	}

	var pgWarning string

	if dbserver.IsPostgres(info.Engine) {
		if pgWarning, err = s.checkPGClient(ctx, info.Version); err != nil {
			return store.DBServer{}, none, "", err
		}
	}

	d, err := s.st.CreateDBServer(ctx, store.NewDBServer{
		Name: req.Name, Host: req.Host, Port: req.Port, AdminUser: req.AdminUser,
		TLSMode: string(req.TLS), CAPEM: req.CAPEM, Status: statusOf(info, nil, s.now()),
		Seal: func(id int64) ([]byte, error) { return s.dbs.Seal(req.AdminPassword, id) },
	}, s.now())
	if errors.Is(err, store.ErrDBServerExists) {
		return store.DBServer{}, none, "", ErrDBServerExists
	}

	if err != nil {
		return store.DBServer{}, none, "", err
	}

	s.dbManifestChanged(ctx)
	s.audit(ctx, actor, "db_server.create", req.Name, map[string]string{
		"host": req.Host, "port": strconv.Itoa(req.Port), "tls": string(req.TLS), "server": info.Display(),
		"superuser": strconv.FormatBool(info.Superuser),
	})

	return d, info, pgWarning, nil
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

	s.dbManifestChanged(ctx)
	s.removeToolServers(ctx)
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
		Engine: info.Engine, Version: info.Version, ClientHost: info.ClientHost, TLSActive: info.TLS,
		CertExpires: info.CertExpires, CheckedAt: now,
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
	case errors.Is(err, dbserver.ErrInvalid):
		// For example a name PostgreSQL keeps for itself.
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	case errors.Is(err, dbserver.ErrPostgresNotYet):
		return ErrPostgreSQLNotYet
	default:
		return err
	}
}

// RemoteDBServers is the real DBServers: it seals passwords with the server
// key and connects with internal/dbserver.
type RemoteDBServers struct {
	Box *secrets.Box
}

func dbServerPurpose(id int64) string { return extdb.PasswordPurpose(id) }

// Check implements DBServers.
func (r RemoteDBServers) Check(ctx context.Context, cfg dbserver.Config, privileges bool) (dbserver.Info, error) {
	var (
		mu       sync.Mutex
		notAfter time.Time
	)

	cfg.OnCertificate = func(c *x509.Certificate) {
		mu.Lock()
		notAfter = c.NotAfter
		mu.Unlock()
	}

	db, err := dbserver.Open(cfg)
	if err != nil {
		return dbserver.Info{}, err
	}

	defer db.Close()

	info, err := probe(ctx, cfg.Engine, db)
	if err != nil {
		return dbserver.Info{}, err
	}

	mu.Lock()
	info.CertExpires = notAfter
	mu.Unlock()

	if !privileges {
		return info, nil
	}

	cfg.OnCertificate = nil

	c, err := newRemoteClient(db, cfg, info.ClientHost)
	if err != nil {
		return dbserver.Info{}, err
	}

	if err := c.CheckPrivileges(ctx); err != nil {
		return dbserver.Info{}, err
	}

	return info, nil
}

// probe reads the server with the probe of its engine.
func probe(ctx context.Context, engine string, db dbserver.Querier) (dbserver.Info, error) {
	if dbserver.IsPostgres(engine) {
		return dbserver.ProbePostgres(ctx, db)
	}

	return dbserver.Probe(ctx, db)
}

// remoteClient is a RemoteDB that can also test the privileges.
type remoteClient interface {
	RemoteDB
	CheckPrivileges(ctx context.Context) error
}

// newRemoteClient returns the client of cfg's engine on db. clientHost is
// the host part of MariaDB and MySQL users; PostgreSQL has none.
func newRemoteClient(db *sql.DB, cfg dbserver.Config, clientHost string) (remoteClient, error) {
	if dbserver.IsPostgres(cfg.Engine) {
		return dbserver.NewPGClient(db, cfg), nil
	}

	return dbserver.NewClient(db, clientHost)
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
		Engine: engineOf(srv), Host: srv.Host, Port: srv.Port, AdminUser: srv.AdminUser, AdminPassword: string(pw),
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
		info, err := probe(ctx, cfg.Engine, db)
		if err != nil {
			return err
		}

		host = info.ClientHost
	}

	c, err := newRemoteClient(db, cfg, host)
	if err != nil {
		return err
	}

	return fn(c)
}

// engineOf returns the engine family of an external server for its
// Config. The column holds what the last successful check saw, and a
// server is stored only after one, so it is never empty for an external
// server; a PostgreSQL server never reports another engine.
func engineOf(srv store.DBServer) string {
	if dbserver.IsPostgres(srv.Status.Engine) {
		return dbserver.EnginePostgreSQL
	}

	return ""
}

// IsPostgres reports whether srv is a PostgreSQL server.
func IsPostgres(srv store.DBServer) bool { return dbserver.IsPostgres(srv.Status.Engine) }

// DBServerCheckEvery is how often the panel checks the external servers
// again (ADR-0041).
const DBServerCheckEvery = 15 * time.Minute

// RefreshDBServers checks every external server once and records what it
// saw. A server that fails is recorded with its error; only a failure to
// record is logged.
func (s *Service) RefreshDBServers(ctx context.Context) {
	if s.dbs == nil {
		return
	}

	list, err := s.st.DBServers(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "list database servers", "err", err)

		return
	}

	for _, srv := range list {
		if srv.Local() || ctx.Err() != nil {
			continue
		}

		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if _, err := s.CheckDBServer(cctx, srv.Name); err != nil {
			s.log.ErrorContext(ctx, "check database server", "server", srv.Name, "err", err)
		}

		cancel()
	}

	s.FinishDBRemoteJobs(ctx)
	s.FinishDBRemoteTools(ctx)
}

// Days before an external server's certificate ends at which doctor warns
// and fails. The server's operator renews it, so the margin is wider than
// for the sites' own certificates.
const (
	DBServerCertWarnDays = 21
	DBServerCertFailDays = 7
)

// DBServerDoctor reports the external servers for doctor from their last
// check: unreachable, unencrypted, certificate ending soon, not checked
// for a while.
func (s *Service) DBServerDoctor(ctx context.Context, now time.Time) ([]agentapi.DoctorCheck, error) {
	list, err := s.st.DBServers(ctx)
	if err != nil {
		return nil, err
	}

	var out []agentapi.DoctorCheck

	for _, srv := range list {
		if srv.Local() {
			continue
		}

		out = append(out, dbServerChecks(srv, now)...)
	}

	return out, nil
}

func dbServerChecks(srv store.DBServer, now time.Time) []agentapi.DoctorCheck {
	name, st := srv.Name, srv.Status

	what := name
	if st.Version != "" {
		what = name + " (" + st.Engine + " " + st.Version + ")"
	}

	var out []agentapi.DoctorCheck

	switch {
	case st.CheckedAt.IsZero():
		out = append(out, doctor.New(agentapi.DoctorWarn, "dbserver.reachable", "dbserver.unchecked", "dbserver.unchecked.hint", doctor.P{"name": name}))
	case st.LastError != "":
		out = append(out, doctor.New(agentapi.DoctorFail, "dbserver.reachable", "dbserver.down", "dbserver.down.hint", doctor.P{"name": what, "error": st.LastError}))
	default:
		out = append(out, doctor.New(agentapi.DoctorOK, "dbserver.reachable", "dbserver.ok", "", doctor.P{"name": what}))
	}

	if !st.CheckedAt.IsZero() && now.Sub(st.CheckedAt) > 4*DBServerCheckEvery {
		out = append(out, doctor.New(agentapi.DoctorWarn, "dbserver.checked", "dbserver.stale", "dbserver.stale.hint", doctor.P{"name": name, "time": st.CheckedAt.UTC().Format(time.RFC3339)}))
	}

	if srv.TLSMode == string(dbserver.TLSNone) {
		out = append(out, doctor.New(agentapi.DoctorWarn, "dbserver.tls", "dbserver.notls", "dbserver.notls.hint", doctor.P{"name": name}))
	}

	if !st.CertExpires.IsZero() {
		days := int(st.CertExpires.Sub(now).Hours() / 24)

		switch {
		case days < DBServerCertFailDays:
			out = append(out, doctor.New(agentapi.DoctorFail, "dbserver.cert", "dbserver.cert", "dbserver.cert.fail.hint", doctor.P{"name": name, "days": strconv.Itoa(days)}))
		case days < DBServerCertWarnDays:
			out = append(out, doctor.New(agentapi.DoctorWarn, "dbserver.cert", "dbserver.cert", "dbserver.cert.warn.hint", doctor.P{"name": name, "days": strconv.Itoa(days)}))
		}
	}

	return out
}
