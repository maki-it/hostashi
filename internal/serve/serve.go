// SPDX-License-Identifier: AGPL-3.0-or-later

// Package serve is the unprivileged web process. It never runs as root and
// can only ask the agent to do things through agentapi.
package serve

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/acme"
	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/cpmigrate"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/mail"
	"github.com/maki-it/hostashi/internal/metrics"
	"github.com/maki-it/hostashi/internal/notify"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/reqid"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/servermon"
	"github.com/maki-it/hostashi/internal/store"
	"github.com/maki-it/hostashi/internal/ui"
)

// Config is the web process configuration.
type Config struct {
	Listen  string // host:port, loopback only until TLS exists
	Socket  string // agent socket
	DBPath  string // SQLite database, directory must not be group or world writable
	KeyPath string // encryption key for stored secrets, created on first start
	// PublicURL is the address users open the panel at. Passkeys are bound
	// to its host name and are off while it is empty.
	PublicURL string
	Version   string
	Log       *slog.Logger

	// ACMEDirectory is the CA for site certificates; empty turns them off.
	ACMEDirectory string
	// ACMECAFile is an extra root CA for the ACME server (tests).
	ACMECAFile string
	// ACMEChallengeDir receives HTTP-01 challenge files for nginx.
	ACMEChallengeDir string

	// MetricsListen is a loopback IP and port that serves /metrics without
	// a token (ADR-0022); empty turns that listener off.
	MetricsListen string

	// DBToolPort is the HTTPS port of the web database tool (ADR-0038),
	// served for the panel's host name; 0 turns the tool off.
	DBToolPort int

	// PagesDir is where the agent keeps the maintenance and parking pages,
	// read for previews; empty means pages.DefaultDir.
	PagesDir string
}

// DefaultChallengeDir is where nginx looks for HTTP-01 challenge files
// (agent.ACMERoot plus the well-known path).
const DefaultChallengeDir = "/var/lib/hostashi-acme/.well-known/acme-challenge"

// ErrRoot is returned when serve is started as root.
var ErrRoot = errors.New("serve must not run as root")

// CheckEUID refuses to start the web process as root.
func CheckEUID(euid int) error {
	if euid == 0 {
		return ErrRoot
	}

	return nil
}

// CheckListen accepts only loopback addresses. There is no TLS yet, so the
// panel must not be reachable from the network in plain HTTP.
func CheckListen(addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("listen address %q: %w", addr, err)
	}

	if port == "" {
		return fmt.Errorf("listen address %q: missing port", addr)
	}

	if host == "localhost" {
		return nil
	}

	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen address %q: only loopback is allowed until TLS is implemented", addr)
	}

	return nil
}

// Deps are the backends the handlers talk to. Tests replace them.
type Deps struct {
	PingAgent func(ctx context.Context) error
	PingDB    func(ctx context.Context) error
	Auth      *auth.Service    // nil disables the auth routes (tests)
	Sites     *hosting.Service // nil disables the site routes; needs Auth
	Backups   Backups          // nil disables the backup routes; needs Auth
	// CloudPanel keeps uploaded scan reports (ADR-0017); nil disables the
	// migration routes. Needs Auth.
	CloudPanel *cpmigrate.Service
	// CloudPanelSource is the old server of the migration (ADR-0026); nil
	// disables its routes. Needs Auth and Settings (for the audit log).
	CloudPanelSource CloudPanelSource
	// Metrics counts requests and serves /metrics (ADR-0022); nil turns both
	// off. The endpoint needs Auth and Settings.
	Metrics  *metrics.Metrics
	Settings Settings
	// Doctor runs the checks of hostashi doctor; nil disables its route.
	// Needs Auth.
	Doctor Doctor
	// Firewall is the panel's part of the firewall (ADR-0034); nil
	// disables its routes. Needs Auth and Settings.
	Firewall Firewall
	// Pools lists the storage pools (ADR-0016); nil disables its route.
	// Needs Auth.
	Pools Pools
	// PoolAdmin and PoolSites rename pools and pick the default one
	// (ADR-0016 addendum 2); nil disables those routes. Need Auth, Pools
	// and Settings.
	PoolAdmin PoolAdmin
	PoolSites PoolSites
	// PHP installs more PHP versions (ADR-0036); nil disables its routes.
	// Needs Auth and Settings.
	PHP PHPInstaller
	// PGClient installs the PostgreSQL client tools (ADR-0051); nil
	// disables its routes. Needs Auth and Settings.
	PGClient PGClient
	// Node installs the pinned Node.js builds (ADR-0054); nil disables its
	// routes. Needs Auth and Settings.
	Node NodeInstaller
	// Python installs the pinned CPython builds (ADR-0061); nil disables
	// its routes. Needs Auth and Settings.
	Python PythonInstaller
	// Mail is the SMTP relay (ADR-0044); nil disables its routes. Needs
	// Auth, Settings and MailSend.
	Mail     MailRelay
	MailSend MailSender
	// Outbox queues email; nil disables the account address routes. Needs
	// Auth and Mail.
	Outbox MailOutbox

	// Files starts the sites' file manager sockets (ADR-0040); nil
	// disables the file routes. Needs Auth, Sites and Settings.
	Files FilesOpener

	// Logs reads the sites' logs and sets their retention (ADR-0053); nil
	// disables the log routes. Needs Auth and Settings.
	Logs SiteLogs

	// NotifyPrefs keeps the operations email switches; nil disables their
	// routes. Needs Auth and Mail.
	NotifyPrefs NotifyPrefs
	// MailHistory adds the email check to the doctor report; nil leaves it
	// out. Needs Mail.
	MailHistory MailHistory

	// Monitor serves service management and the dashboard (ADR-0052); nil
	// disables their routes. Needs Auth and Settings (for the audit log).
	Monitor ServerMonitor

	// System serves the time zone, hostname and reboot (ADR-0060); nil
	// disables their routes. Needs Auth and Settings (for the audit log).
	System SystemAgent

	// AuditLog reads the audit log for its page and export; nil disables
	// its routes. Needs Auth.
	AuditLog AuditLog
}

// Handler returns the HTTP routes of the web process.
func Handler(cfg Config, deps Deps) http.Handler {
	h, _ := handler(cfg, deps)

	return h
}

// handler also returns the registered route patterns, for the route
// matrix test (ADR-0035).
func handler(cfg Config, deps Deps) (http.Handler, *routeMux) {
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	mux := newRouteMux()
	mux.Op("GET /api/v1/health", openapi.Operation{
		ID: "getHealth", Tag: "system", Summary: "Liveness of the panel, its agent and its database", Out: healthJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		h := healthJSON{Status: "ok", Agent: "ok", DB: "ok", Version: cfg.Version}
		code := http.StatusOK

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		// Details go to the log, not to the client.
		if err := deps.PingAgent(ctx); err != nil {
			log.WarnContext(ctx, "agent ping failed", "err", err)

			h.Status, h.Agent = "degraded", "unavailable"
			code = http.StatusServiceUnavailable
		}

		if err := deps.PingDB(ctx); err != nil {
			log.ErrorContext(ctx, "database ping failed", "err", err)

			h.Status, h.DB = "degraded", "unavailable"
			code = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(h)
	})

	if deps.Auth != nil {
		var m *mailer
		if deps.Mail != nil && deps.Outbox != nil {
			m = &mailer{relay: deps.Mail, outbox: deps.Outbox, publicURL: cfg.PublicURL, log: log, who: deps.Auth.ConfirmedEmail}
			deps.Auth.OnAccountEvent(m.accountEvent)
		}

		registerAuth(mux, deps.Auth, m, log)
		registerOpenAPI(mux, authn{svc: deps.Auth, fail: newFail(log)})
		registerDevice(mux, authn{svc: deps.Auth, fail: newFail(log)}, cfg.PublicURL)

		if deps.Sites != nil {
			registerSites(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerApps(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerPythonSites(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerSiteType(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerCerts(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerDatabases(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerDBServers(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerPages(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerPagePreview(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, newPreviews(cfg.PagesDir), log)
			registerParked(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerCron(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerGit(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerDeploys(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerSSH(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerSnippets(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerPHPSettings(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerHeaders(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerRealIP(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerPageCache(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerLimits(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerObjectCache(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)

			if deps.Files != nil && deps.Settings != nil {
				registerFiles(mux, authn{svc: deps.Auth, fail: newFail(log)}, newFilesProxy(deps.Files, deps.Sites, deps.Settings, log))
			}
		}

		if deps.Logs != nil && deps.Settings != nil {
			registerLogs(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Logs, deps.Settings, log)
		}

		if deps.Backups != nil {
			var (
				dbs  SiteDatabases
				jobs DatabaseJobs
			)

			if deps.Sites != nil {
				dbs, jobs = deps.Sites.Databases, deps.Sites
			}

			registerBackups(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Backups, dbs, jobs, log)
		}

		if deps.CloudPanel != nil {
			registerMigrations(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.CloudPanel, log)

			if deps.Sites != nil {
				registerSnippetDraft(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.CloudPanel, deps.Sites, log)
			}
		}

		if deps.CloudPanelSource != nil && deps.Settings != nil {
			registerCloudPanelSource(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.CloudPanelSource, deps.Settings, log)
		}

		if deps.Firewall != nil && deps.Settings != nil {
			registerFirewall(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Firewall, deps.Settings, log)
		}

		if deps.PHP != nil && deps.Settings != nil {
			var sites func(context.Context) ([]store.Site, error)
			if deps.Sites != nil {
				sites = deps.Sites.List
			}

			registerPHPInstall(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.PHP, deps.Settings, sites, log)
		}

		if deps.Node != nil && deps.Settings != nil {
			registerNode(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Node, deps.Settings, log)
		}

		if deps.Python != nil && deps.Settings != nil {
			registerPython(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Python, deps.Settings, log)
		}

		if deps.PGClient != nil && deps.Settings != nil {
			registerPGClient(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.PGClient, deps.Settings, log)
		}

		if deps.Mail != nil && deps.MailSend != nil && deps.Settings != nil {
			registerMail(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Mail, deps.MailSend, deps.Settings, log)
		}

		if deps.Mail != nil && deps.Outbox != nil {
			registerAccountEmail(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Mail, deps.Outbox, cfg.PublicURL, log)
		}

		if deps.Mail != nil && deps.NotifyPrefs != nil {
			registerNotifications(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Mail, deps.NotifyPrefs)
		}

		if deps.Pools != nil {
			registerPools(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Pools, log)

			if deps.PoolAdmin != nil && deps.PoolSites != nil && deps.Settings != nil {
				registerPoolAdmin(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Pools, deps.PoolAdmin, deps.PoolSites, deps.Settings, log)
			}
		}

		if deps.Doctor != nil {
			var panel []PanelChecks
			if deps.Sites != nil {
				panel = append(panel, deps.Sites.DBServerDoctor, deps.Sites.DatabaseDoctor)

				if deps.PGClient != nil {
					panel = append(panel, pgClientCheck(deps.Sites.DBServerList, deps.PGClient))
				}
			}

			if deps.Mail != nil && deps.MailHistory != nil {
				panel = append(panel, mailCheck(deps.Mail, deps.MailHistory))
			}

			registerDoctor(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Doctor, allPanelChecks(panel), log)
		}

		if deps.Monitor != nil && deps.Settings != nil {
			registerServices(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Monitor, deps.Settings, log)
		}

		if deps.System != nil && deps.Settings != nil {
			registerSystem(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.System, deps.Settings, log)
		}

		if deps.AuditLog != nil {
			registerAudit(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.AuditLog, log)
		}

		if deps.Metrics != nil && deps.Settings != nil {
			registerMetrics(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Metrics, deps.Settings, log)
		}
	}

	h := secure(withUI(mux, ui.Handler()))
	if deps.Metrics != nil {
		h = deps.Metrics.Instrument(h)
	}

	// Both wrappers copy the request, so they stay outside Instrument, which
	// reads the route pattern the mux sets on the request it sees.
	return withRequestID(withAuditIP(h)), mux
}

// RequestIDHeader names the response header with the request's ID.
const RequestIDHeader = "X-Request-ID"

// withRequestID gives every request a new random ID (ADR-0022). It goes into
// the response header, error bodies, log lines and agent calls. An ID sent
// by the client is ignored, so nobody can make unrelated log lines look
// connected.
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := reqid.New()
		w.Header().Set(RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(reqid.With(r.Context(), id)))
	})
}

// withAuditIP puts the client's address into the request's context, so
// that every audit entry written while serving it records the address.
func withAuditIP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(store.WithClientIP(r.Context(), clientIP(r))))
	})
}

// withUI sends /api and everything below it, and /metrics, to the API
// routes, so their 404 and 405 answers stay as they are, and every other path
// to the frontend.
func withUI(api, web http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == MetricsPath {
			api.ServeHTTP(w, r)

			return
		}

		web.ServeHTTP(w, r)
	})
}

// SetupTokenPath is where the one-time setup token lives, next to the database.
func SetupTokenPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "setup-token")
}

// PanelCopyPath is where the panel keeps the copy of its database that the
// backup reads (ADR-0008): /var/lib/hostashi/backup/panel.db on a server.
func PanelCopyPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "backup", "panel.db")
}

// copyEvery is how often the panel refreshes its database copy, so a
// scheduled backup is never more than this behind.
const copyEvery = time.Hour

// dbServerLoop checks the external database servers every
// hosting.DBServerCheckEvery, so the list shows their version and state and
// doctor knows when one is gone (ADR-0041).
func dbServerLoop(ctx context.Context, svc *hosting.Service) {
	t := time.NewTicker(hosting.DBServerCheckEvery)
	defer t.Stop()

	for {
		svc.RefreshDBServers(ctx)

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func copyLoop(ctx context.Context, copyDB func(context.Context) error, log *slog.Logger) {
	t := time.NewTicker(copyEvery)
	defer t.Stop()

	for {
		if err := copyDB(ctx); err != nil && ctx.Err() == nil && log != nil {
			log.ErrorContext(ctx, "panel database copy for the backup failed", "err", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Run starts the web process and blocks until ctx is canceled.
func Run(ctx context.Context, cfg Config) error {
	if err := CheckListen(cfg.Listen); err != nil {
		return err
	}

	if cfg.MetricsListen != "" {
		if err := CheckMetricsListen(cfg.MetricsListen); err != nil {
			return err
		}
	}

	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		return err
	}

	defer func() { _ = st.Close() }()

	tokenPath := SetupTokenPath(cfg.DBPath)
	if err := auth.EnsureSetupToken(ctx, st, tokenPath, time.Now()); err != nil {
		return err
	}

	box, err := secrets.LoadOrCreate(cfg.KeyPath)
	if err != nil {
		return err
	}

	m := metrics.New()

	svc := auth.NewService(auth.Config{Store: st, SetupTokenPath: tokenPath, Box: box, Observer: m, FailLog: cfg.Log})
	if err := svc.EnablePasskeys(auth.PasskeyConfig{PublicURL: cfg.PublicURL}); err != nil {
		return err
	}

	go cleanupLoop(ctx, svc, cfg.Log)

	copyPath := PanelCopyPath(cfg.DBPath)
	panelCopy := func(ctx context.Context) error { return st.CopyTo(ctx, copyPath) }

	if err := os.MkdirAll(filepath.Dir(copyPath), 0o700); err != nil {
		return fmt.Errorf("panel copy directory: %w", err)
	}

	go copyLoop(ctx, panelCopy, cfg.Log)

	client := agentrpc.NewClient(cfg.Socket)
	client.Observe(m.AgentOp)

	// Outgoing email (ADR-0044): nothing is queued while no relay is set.
	relay := mail.NewStore(st, box)
	outbox := mail.NewOutbox(st, relay, box, cfg.Log)

	go outbox.Run(ctx)

	deps := Deps{
		PingAgent: func(ctx context.Context) error {
			_, err := agentapi.Ping.Call(ctx, client, agentapi.PingRequest{})
			return err
		},
		PingDB:      st.Ping,
		Auth:        svc,
		Sites:       hosting.New(st, hosting.RPCAgent{C: client}, cfg.Log),
		Backups:     RPCBackups{C: client, BeforeRun: panelCopy},
		Metrics:     m,
		Settings:    st,
		Doctor:      RPCDoctor(client),
		Pools:       RPCPools(client),
		PoolAdmin:   RPCPoolAdmin{C: client},
		PoolSites:   st,
		Firewall:    RPCFirewall{C: client},
		PHP:         RPCPHPInstaller{C: client},
		PGClient:    RPCPGClient{C: client},
		Node:        RPCNodeInstaller{C: client},
		Python:      RPCPythonInstaller{C: client},
		Mail:        relay,
		Outbox:      outbox,
		NotifyPrefs: st,
		MailHistory: st,
		MailSend:    func(ctx context.Context, c mail.Config, m mail.Message) error { return mail.Send(ctx, c, m) },
		Files:       RPCFilesOpener(client),
		AuditLog:    st,
		Logs:        RPCSiteLogs{C: client},
	}

	go SyncLogRetention(ctx, deps.Logs, st, 30, 10*time.Second, cfg.Log)

	deps.CloudPanel = cpmigrate.New(st, box, migrationTarget(deps.Sites), cfg.Log)
	deps.CloudPanel.Connect(siteCreator(deps.Sites, cfg.Log), RPCCloudPanelImports{C: client})
	deps.CloudPanel.ConnectPools(cpmigrate.PoolLister(deps.Pools))
	deps.Sites.ResolveDefaultPool(func(ctx context.Context) (string, error) {
		pools, err := deps.Pools(ctx)

		return DefaultPoolOf(pools), err
	})
	deps.CloudPanel.EnableLoads(ctx, deps.Sites, 0)
	deps.CloudPanel.EnableCutover(deps.Sites, resolvConf("/etc/resolv.conf"), serverAddrs(client))

	go deps.CloudPanel.RunCutover(ctx, 0)

	deps.CloudPanelSource = RPCCloudPanelSource{C: client}

	deps.Sites.EnableCron(hosting.RPCAgent{C: client})
	deps.Sites.EnableSSH(hosting.RPCAgent{C: client})
	deps.Sites.EnableDBServers(hosting.RemoteDBServers{Box: box}, filepath.Dir(copyPath))
	deps.Sites.EnablePGClient(RPCPGClient{C: client})

	if err := deps.Sites.WriteDBManifest(ctx); err != nil {
		cfg.Log.ErrorContext(ctx, "write the external database list for the backup", "err", err)
	}

	go dbServerLoop(ctx, deps.Sites)

	deps.Sites.EnableLimits(hosting.RPCAgent{C: client})
	deps.Sites.EnableDBSizes(hosting.RPCAgent{C: client})

	// Limits a migration changed or an earlier apply missed (ADR-0057).
	go deps.Sites.ApplyPendingLimits(ctx)

	deps.Sites.EnableObjectCache(hosting.RPCAgent{C: client})
	deps.Sites.EnableApps(hosting.RPCAgent{C: client})
	deps.Sites.EnableAppEnv(box)
	deps.Sites.EnableGit(box)
	deps.Sites.EnableDeploys(hosting.RPCAgent{C: client})

	// Deploy runs are followed in the panel; a restart picks them up.
	go deps.Sites.RunDeploys(ctx)

	deps.Sites.ObserveApply(m.WebApplied)
	m.RegisterCerts(certList(deps.Sites), cfg.Log)
	m.RegisterSites(siteStats(client), cfg.Log)
	m.RegisterTokenExpiry(svc.MetricsTokenExpiry, cfg.Log)

	// Operations emails (ADR-0044) for the accounts that switched them on.
	notifier := &notify.Notifier{
		St: st, Outbox: outbox, Host: panelHost(cfg.PublicURL), Log: cfg.Log,
		Link: func(f string) string { return panelLink(cfg.PublicURL, f) },
	}
	watcher := &notify.Watcher{
		Notifier: notifier,
		Backups:  deps.Backups.Status,
		Certs:    notifyCerts(st, deps.Sites, PanelDomain(cfg.PublicURL)),
		Doctor:   withDatabaseChecks(deps.Doctor, deps.Sites.DatabaseDoctor),
		Now:      time.Now,
		Log:      cfg.Log,
	}

	go watcher.Run(ctx)

	// Service management and the dashboard (ADR-0052).
	// Service outages are emailed too (ADR-0055).
	mon := &servermon.Monitor{
		Agent: RPCServerAgent{C: client}, Store: st, Log: cfg.Log,
		Alerts: &notify.ServiceAlerts{Notifier: notifier, Log: cfg.Log},
	}
	deps.Monitor = mon
	deps.System = RPCSystemAgent{C: client}

	go mon.Run(ctx)

	if iss, err := newIssuer(cfg, st, box); err != nil {
		return err
	} else if iss != nil {
		deps.Sites.EnableTLS(iss)

		go deps.Sites.RunTLS(ctx)
	}

	if d := PanelDomain(cfg.PublicURL); d != "" {
		// A site or parked domain that already has the name keeps it, and
		// nginx serves no panel there: the agent would refuse every web
		// state with both (ADR-0042). The admin picks another name.
		if owner, err := panelNameOwner(ctx, deps.Sites, d); err != nil {
			return err
		} else if owner != "" {
			if cfg.Log != nil {
				cfg.Log.ErrorContext(ctx, "the panel's host name belongs to "+owner+"; nginx does not serve the panel there, choose another --panel-domain", "domain", d)
			}
		} else if err := deps.Sites.EnablePanel(d); err != nil {
			return err
		}
	}

	if cfg.DBToolPort != 0 {
		if err := deps.Sites.EnableDBTool(hosting.RPCAgent{C: client}, cfg.DBToolPort); err != nil {
			return err
		}
	}

	// After an upgrade the new release's nginx config reaches the server
	// now, not with the next change (ADR-0042 panel on 443 needed this).
	go deps.Sites.SyncAtStart(ctx)

	h := Handler(cfg, deps)
	srv := newServer(h)

	// nginx's way in (ADR-0023); without socket activation only TCP.
	proxyLn, err := systemdListener()
	if err != nil {
		return err
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return err
	}

	if cfg.Log != nil {
		cfg.Log.InfoContext(ctx, "serve listening", "addr", ln.Addr().String(), "agentSocket", cfg.Socket, "db", cfg.DBPath, "setupTokenFile", tokenPath)
	}

	// The token-free metrics listener (ADR-0022), opened before anything
	// serves so a failure here leaves nothing running.
	var mln net.Listener
	if cfg.MetricsListen != "" {
		if mln, err = lc.Listen(ctx, "tcp", cfg.MetricsListen); err != nil {
			_ = ln.Close()

			return err
		}
	}

	errc := make(chan error, 3)
	go func() { errc <- srv.Serve(ln) }()

	servers := []*http.Server{srv}

	if proxyLn != nil {
		psrv := newServer(fromProxy(h))
		servers = append(servers, psrv)

		if cfg.Log != nil {
			cfg.Log.InfoContext(ctx, "serve listening for nginx", "addr", proxyLn.Addr().String())
		}

		go func() { errc <- psrv.Serve(proxyLn) }()
	}

	if mln != nil {
		msrv := newServer(LocalMetricsHandler(deps.Metrics, deps.Settings, cfg.Log))
		servers = append(servers, msrv)

		if cfg.Log != nil {
			cfg.Log.InfoContext(ctx, "serve listening for local metrics scrapes", "addr", mln.Addr().String())
		}

		go func() { errc <- msrv.Serve(mln) }()
	}

	var runErr error

	select {
	case runErr = <-errc:
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	for _, s := range servers {
		runErr = errors.Join(runErr, s.Shutdown(shutdownCtx))
	}

	return runErr
}

// certList feeds the certificate series from the site service.
func certList(svc *hosting.Service) func(context.Context) ([]metrics.Cert, error) {
	return func(ctx context.Context) ([]metrics.Cert, error) {
		list, err := svc.CertExpiries(ctx)
		if err != nil {
			return nil, err
		}

		out := make([]metrics.Cert, 0, len(list))
		for _, c := range list {
			out = append(out, metrics.Cert{Site: c.Site, Cert: c.Cert, NotAfter: c.NotAfter})
		}

		return out, nil
	}
}

// notifyCerts lists the certificates of sites with TLS and of the panel's
// own address for the operations emails.
func notifyCerts(st *store.Store, svc *hosting.Service, panelDomain string) func(context.Context) ([]notify.Cert, error) {
	return func(ctx context.Context) ([]notify.Cert, error) {
		sites, err := st.Sites(ctx)
		if err != nil {
			return nil, err
		}

		out := []notify.Cert{}

		for _, s := range sites {
			if s.TLS && !s.Disabled {
				out = append(out, notify.Cert{Site: s.Name, Name: s.Name, NotAfter: s.TLSNotAfter, Error: s.TLSError, FailedAt: s.TLSFailedAt})
			}
		}

		if panelDomain != "" {
			p, err := svc.PanelTLS(ctx)
			if err != nil {
				return out, err
			}

			out = append(out, notify.Cert{Name: panelDomain, NotAfter: p.NotAfter, Error: p.Error, FailedAt: p.FailedAt})
		}

		return out, nil
	}
}

// siteStats feeds the per-site series from the agent (ADR-0022).
func siteStats(c *agentrpc.Client) func(context.Context) ([]metrics.Site, error) {
	return func(ctx context.Context) ([]metrics.Site, error) {
		res, err := agentapi.MetricsSnapshot.Call(ctx, c, agentapi.MetricsSnapshotRequest{})
		if err != nil {
			return nil, err
		}

		out := make([]metrics.Site, 0, len(res.Sites))
		for _, s := range res.Sites {
			out = append(out, metrics.Site{
				Name: s.Name, PHPVersion: s.PHPVersion, PHPUp: s.PHPUp,
				PHPMemoryBytes: s.PHPMemoryBytes, PHPMemoryKnown: s.PHPMemoryKnown,
				DiskUsedBytes: s.DiskUsedBytes, DiskMeasuredAt: s.DiskMeasuredAt,
				CacheMB: s.CacheMB, CacheUp: s.CacheUp, CacheMemoryBytes: s.CacheMemoryBytes, CacheMemoryKnown: s.CacheMemoryKnown,
			})
		}

		return out, nil
	}
}

func newServer(h http.Handler) *http.Server {
	return &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
}

// newIssuer builds the ACME client, or nil when certificates are off or the
// challenge directory is missing (no installer step yet).
func newIssuer(cfg Config, st *store.Store, box *secrets.Box) (*acme.Client, error) {
	if cfg.ACMEDirectory == "" {
		return nil, nil //nolint:nilnil // certificates are off
	}

	if st, err := os.Stat(cfg.ACMEChallengeDir); err != nil || !st.IsDir() {
		if cfg.Log != nil {
			cfg.Log.Warn("no ACME challenge directory, site certificates are off", "dir", cfg.ACMEChallengeDir)
		}

		return nil, nil //nolint:nilnil // certificates are off
	}

	if cfg.Log != nil {
		acme.SetLogger(cfg.Log.With("component", "acme"))
	}

	c := &acme.Client{
		Directory:    cfg.ACMEDirectory,
		ChallengeDir: cfg.ACMEChallengeDir,
		Keys: acme.SettingsKeys{
			Get: st.Setting, Set: st.SetSetting,
			Seal: box.Seal, Open: box.Open,
		},
	}

	if cfg.ACMECAFile != "" {
		pem, err := os.ReadFile(cfg.ACMECAFile)
		if err != nil {
			return nil, fmt.Errorf("acme ca: %w", err)
		}

		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}

		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("acme ca: no certificate in %s", cfg.ACMECAFile)
		}

		c.HTTPClient = &http.Client{
			Timeout:   30 * time.Second,
			Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}},
		}
	}

	return c, nil
}

// cleanupLoop removes expired sessions every ten minutes.
func cleanupLoop(ctx context.Context, svc *auth.Service, log *slog.Logger) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := svc.Cleanup(ctx); err != nil && log != nil {
				log.WarnContext(ctx, "session cleanup failed", "err", err)
			}
		}
	}
}
