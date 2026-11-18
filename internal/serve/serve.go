// SPDX-License-Identifier: AGPL-3.0-or-later

// Package serve is the unprivileged web process. It never runs as root and
// can only ask the agent to do things through agentapi.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

// Config is the web process configuration.
type Config struct {
	Listen  string // host:port, loopback only until TLS exists
	Socket  string // agent socket
	DBPath  string // SQLite database, directory must not be group or world writable
	KeyPath string // encryption key for stored secrets, created on first start
	Version string
	Log     *slog.Logger
}

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
	Auth      *auth.Service // nil disables the auth routes (tests)
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
	}

	return secure(mux)
}

// SetupTokenPath is where the one-time setup token lives, next to the database.
func SetupTokenPath(dbPath string) string {
	return filepath.Join(filepath.Dir(dbPath), "setup-token")
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

	go cleanupLoop(ctx, svc, cfg.Log)

	client := agentrpc.NewClient(cfg.Socket)
	deps := Deps{
		PingAgent: func(ctx context.Context) error {
			_, err := agentapi.Ping.Call(ctx, client, agentapi.PingRequest{})
			return err
		},
		PingDB: st.Ping,
		Auth:   svc,
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           Handler(cfg, deps),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	var lc net.ListenConfig

	ln, err := lc.Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return err
	}

	if cfg.Log != nil {
		cfg.Log.Info("serve listening", "addr", ln.Addr().String(), "agentSocket", cfg.Socket, "db", cfg.DBPath, "setupTokenFile", tokenPath)
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	return srv.Shutdown(shutdownCtx)
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
