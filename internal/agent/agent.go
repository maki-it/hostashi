// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agent is the privileged side: hostashi agent runs as root, has no
// network listener and only executes the typed operations in agentapi.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/user"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Config is the agent configuration.
type Config struct {
	Socket    string // socket path, its directory must exist with mode 0750 or stricter
	ServeUser string // only this user (and root) may call the agent
	Version   string
	Log       *slog.Logger
}

// ErrNotRoot is returned when the agent is started without root.
var ErrNotRoot = errors.New("agent must run as root")

// CheckEUID refuses to start the agent unless it runs as root.
func CheckEUID(euid int) error {
	if euid != 0 {
		return ErrNotRoot
	}
	return nil
}

// lookupUser is replaced in tests.
var lookupUser = user.Lookup

// serveIdentity resolves the uid that may call the agent and the gid that
// owns the socket.
func serveIdentity(name string) (uid uint32, gid int, err error) {
	if name == "" || name == "root" {
		return 0, 0, fmt.Errorf("serve user must be a dedicated unprivileged user, got %q", name)
	}
	u, err := lookupUser(name)
	if err != nil {
		return 0, 0, fmt.Errorf("serve user %q: %w", name, err)
	}
	id, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return 0, 0, fmt.Errorf("serve user %q: bad uid %q", name, u.Uid)
	}
	if id == 0 {
		return 0, 0, fmt.Errorf("serve user %q has uid 0", name)
	}
	g, err := strconv.ParseUint(u.Gid, 10, 31)
	if err != nil {
		return 0, 0, fmt.Errorf("serve user %q: bad gid %q", name, u.Gid)
	}
	return uint32(id), int(g), nil
}

// NewServer builds the RPC server with all operations registered.
// Callers allowed: root (for the local CLI) and the serve user.
func NewServer(cfg Config, serveUID uint32) *agentrpc.Server {
	s := agentrpc.NewServer(agentrpc.AllowUIDs(0, serveUID), agentrpc.Options{Log: cfg.Log})
	agentrpc.Handle(s, agentapi.Ping, func(_ context.Context, c agentrpc.Caller, _ agentapi.PingRequest) (agentapi.PingResponse, error) {
		return agentapi.PingResponse{CallerUID: c.UID, Version: cfg.Version}, nil
	})
	return s
}

// Run starts the agent and blocks until ctx is canceled.
func Run(ctx context.Context, cfg Config) error {
	uid, gid, err := serveIdentity(cfg.ServeUser)
	if err != nil {
		return err
	}
	s := NewServer(cfg, uid)
	if err := s.Listen(ctx, cfg.Socket, gid); err != nil {
		return err
	}
	if cfg.Log != nil {
		cfg.Log.Info("agent listening", "socket", cfg.Socket, "serveUser", cfg.ServeUser, "serveUid", uid)
	}
	return s.Serve(ctx)
}
