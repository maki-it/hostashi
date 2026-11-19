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
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/secrets"
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
}

// Handler returns the HTTP routes of the web process.
func Handler(cfg Config, deps Deps) http.Handler {
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		type health struct {
			Status  string `json:"status"`
			Agent   string `json:"agent"`
			DB      string `json:"db"`
			Version string `json:"version"`
		}

		h := health{Status: "ok", Agent: "ok", DB: "ok", Version: cfg.Version}
		code := http.StatusOK

		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()

		// Details go to the log, not to the client.
		if err := deps.PingAgent(ctx); err != nil {
			log.Warn("agent ping failed", "err", err)

			h.Status, h.Agent = "degraded", "unavailable"
			code = http.StatusServiceUnavailable
		}

		if err := deps.PingDB(ctx); err != nil {
			log.Error("database ping failed", "err", err)

			h.Status, h.DB = "degraded", "unavailable"
			code = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(h)
	})

	if deps.Auth != nil {
		registerAuth(mux, deps.Auth, log)

		if deps.Sites != nil {
			registerSites(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
			registerDatabases(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Sites, log)
		}

		if deps.Backups != nil {
			var dbs SiteDatabases
			if deps.Sites != nil {
				dbs = deps.Sites.Databases
			}

			registerBackups(mux, authn{svc: deps.Auth, fail: newFail(log)}, deps.Backups, dbs, log)
		}
	}

	return secure(withUI(mux, ui.Handler()))
}

// withUI sends /api and everything below it to the API routes, so their 404
// and 405 answers stay as they are, and every other path to the frontend.
func withUI(api, web http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
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

func copyLoop(ctx context.Context, copyDB func(context.Context) error, log *slog.Logger) {
	t := time.NewTicker(copyEvery)
	defer t.Stop()

	for {
		if err := copyDB(ctx); err != nil && ctx.Err() == nil && log != nil {
			log.Error("panel database copy for the backup failed", "err", err)
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

	svc := auth.NewService(auth.Config{Store: st, SetupTokenPath: tokenPath, Box: box})
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
	deps := Deps{
		PingAgent: func(ctx context.Context) error {
			_, err := agentapi.Ping.Call(ctx, client, agentapi.PingRequest{})
			return err
		},
		PingDB:  st.Ping,
		Auth:    svc,
		Sites:   hosting.New(st, hosting.RPCAgent{C: client}, cfg.Log),
		Backups: RPCBackups{C: client, BeforeRun: panelCopy},
	}

	if iss, err := newIssuer(cfg, st, box); err != nil {
		return err
	} else if iss != nil {
		deps.Sites.EnableTLS(iss)

		go deps.Sites.RunTLS(ctx)
	}

	if d := PanelDomain(cfg.PublicURL); d != "" {
		if err := deps.Sites.EnablePanel(d); err != nil {
			return err
		}
	}

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
		cfg.Log.Info("serve listening", "addr", ln.Addr().String(), "agentSocket", cfg.Socket, "db", cfg.DBPath, "setupTokenFile", tokenPath)
	}

	errc := make(chan error, 2)
	go func() { errc <- srv.Serve(ln) }()

	servers := []*http.Server{srv}

	if proxyLn != nil {
		psrv := newServer(fromProxy(h))
		servers = append(servers, psrv)

		if cfg.Log != nil {
			cfg.Log.Info("serve listening for nginx", "addr", proxyLn.Addr().String())
		}

		go func() { errc <- psrv.Serve(proxyLn) }()
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
				log.Warn("session cleanup failed", "err", err)
			}
		}
	}
}
