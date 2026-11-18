// SPDX-License-Identifier: AGPL-3.0-or-later

// Package serve is the unprivileged web process. It never runs as root and
// can only ask the agent to do things through agentapi.
package serve

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Config is the web process configuration.
type Config struct {
	Listen  string // host:port, loopback only until TLS exists
	Socket  string // agent socket
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

// pinger is the part of the agent client the handlers need.
type pinger func(ctx context.Context) (agentapi.PingResponse, error)

// Handler returns the HTTP routes of the web process.
func Handler(cfg Config, ping pinger) http.Handler {
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/health", func(w http.ResponseWriter, r *http.Request) {
		type health struct {
			Status  string `json:"status"`
			Agent   string `json:"agent"`
			Version string `json:"version"`
		}
		h := health{Status: "ok", Agent: "ok", Version: cfg.Version}
		code := http.StatusOK
		ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		if _, err := ping(ctx); err != nil {
			// Details go to the log, not to the client.
			log.Warn("agent ping failed", "err", err)
			h.Status, h.Agent = "degraded", "unavailable"
			code = http.StatusServiceUnavailable
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(h)
	})
	return mux
}

// Run starts the web process and blocks until ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if err := CheckListen(cfg.Listen); err != nil {
		return err
	}
	client := agentrpc.NewClient(cfg.Socket)
	ping := func(ctx context.Context) (agentapi.PingResponse, error) {
		return agentapi.Ping.Call(ctx, client, agentapi.PingRequest{})
	}
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           Handler(cfg, ping),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	if cfg.Log != nil {
		cfg.Log.Info("serve listening", "addr", ln.Addr().String(), "agentSocket", cfg.Socket)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
