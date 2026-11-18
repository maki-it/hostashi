// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agent is the privileged side: hostashi agent runs as root, has no
// network listener and only executes the typed operations in agentapi.
package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/sysuser"
)

// DefaultStateDir holds the agent's own state (the site registry). It is a
// sibling of the web process's /var/lib/hostashi, not a child: the web
// process owns that directory and could replace anything below it.
const DefaultStateDir = "/var/lib/hostashi-agent"

// Config is the agent configuration.
type Config struct {
	Socket    string // socket path, its directory must exist with mode 0750 or stricter
	ServeUser string // only this user (and root) may call the agent
	StateDir  string // root-owned, mode 0700; default DefaultStateDir
	Version   string
	Log       *slog.Logger

	// Sites serves the site operations. Run builds it from StateDir if nil;
	// NewServer registers no site operations without it.
	Sites *sites.Manager
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

	if cfg.Sites != nil {
		registerSites(s, cfg.Sites)
	}

	return s
}

// registerSites adds the site operations. Only the errors listed here reach
// the caller with a code; everything else becomes "internal error".
func registerSites(s *agentrpc.Server, m *sites.Manager) {
	agentrpc.Handle(s, agentapi.SiteCreate, func(ctx context.Context, _ agentrpc.Caller, req agentapi.SiteCreateRequest) (agentapi.SiteInfo, error) {
		site, err := m.Create(ctx, req.User, req.Pool)
		if err != nil {
			return agentapi.SiteInfo{}, mapSiteError(err)
		}

		return siteInfo(site), nil
	})

	agentrpc.Handle(s, agentapi.SiteDelete, func(ctx context.Context, _ agentrpc.Caller, req agentapi.SiteDeleteRequest) (agentapi.SiteDeleteResponse, error) {
		if err := m.Delete(ctx, req.User); err != nil {
			return agentapi.SiteDeleteResponse{}, mapSiteError(err)
		}

		return agentapi.SiteDeleteResponse{}, nil
	})

	agentrpc.Handle(s, agentapi.SiteList, func(_ context.Context, _ agentrpc.Caller, _ agentapi.SiteListRequest) (agentapi.SiteListResponse, error) {
		all := m.List()
		if len(all) > agentapi.MaxSites {
			all = all[:agentapi.MaxSites]
		}

		out := make([]agentapi.SiteInfo, 0, len(all))
		for _, site := range all {
			out = append(out, siteInfo(site))
		}

		return agentapi.SiteListResponse{Sites: out}, nil
	})
}

func siteInfo(s sites.Site) agentapi.SiteInfo {
	return agentapi.SiteInfo{User: s.User, UID: s.UID, GID: s.GID, Pool: s.Pool}
}

// mapSiteError turns known failures into coded errors with fixed messages.
// Unknown errors are returned as they are: the server logs them and tells
// the caller only "internal error".
func mapSiteError(err error) error {
	switch {
	case errors.Is(err, sites.ErrNotFound):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site"}
	case errors.Is(err, sites.ErrExists):
		return &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "site or system account already exists"}
	case errors.Is(err, sites.ErrInUse):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "site user still has running processes"}
	case errors.Is(err, sites.ErrPoolUnavailable):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "storage pool unavailable"}
	case errors.Is(err, sites.ErrHomeUnsafe), errors.Is(err, sites.ErrHomeExists), errors.Is(err, sites.ErrAccountMismatch):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "site directory or account failed safety checks"}
	default:
		return err
	}
}

// openSites loads the registry from stateDir and builds the production manager.
func openSites(stateDir string, log *slog.Logger) (*sites.Manager, error) {
	if stateDir == "" {
		stateDir = DefaultStateDir
	}

	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("state directory: %w", err)
	}

	reg, err := sites.OpenRegistry(stateDir, 0)
	if err != nil {
		return nil, err
	}

	return sites.NewManager(reg, sysuser.New(log), sites.Options{Log: log}), nil
}

// Run starts the agent and blocks until ctx is canceled.
func Run(ctx context.Context, cfg Config) error {
	uid, gid, err := serveIdentity(cfg.ServeUser)
	if err != nil {
		return err
	}

	if cfg.Sites == nil {
		cfg.Sites, err = openSites(cfg.StateDir, cfg.Log)
		if err != nil {
			return err
		}
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
