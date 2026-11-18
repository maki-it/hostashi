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
	"path/filepath"
	"strconv"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/php"
	"github.com/maki-it/hostashi/internal/phpctl"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/sysuser"
	"github.com/maki-it/hostashi/internal/web"
	"github.com/maki-it/hostashi/internal/webctl"
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

	// Web serves web.apply. Run builds it from StateDir if nginx is
	// installed; without it the operation answers failed_precondition.
	Web *web.Reconciler

	// PHP runs the sites' PHP-FPM pools for web.apply. Run builds it from
	// StateDir; without it a site with PHP is refused.
	PHP *php.Manager
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
		log := cfg.Log
		if log == nil {
			log = slog.New(slog.DiscardHandler)
		}

		h := &siteOps{m: cfg.Sites, web: cfg.Web, php: cfg.PHP, log: log}
		h.register(s)
	}

	return s
}

// siteOps serves the site and web operations. mu orders site.delete against
// web.apply, so a site cannot lose its home while a virtual host or a PHP
// pool is being built for it.
type siteOps struct {
	m   *sites.Manager
	web *web.Reconciler
	php *php.Manager
	log *slog.Logger
	mu  sync.Mutex
}

// register adds the operations. Only the errors listed in the map functions
// reach the caller with a code; everything else becomes "internal error".
func (h *siteOps) register(s *agentrpc.Server) {
	m := h.m

	agentrpc.Handle(s, agentapi.SiteCreate, func(ctx context.Context, _ agentrpc.Caller, req agentapi.SiteCreateRequest) (agentapi.SiteInfo, error) {
		site, err := m.Create(ctx, req.User, req.Pool)
		if err != nil {
			return agentapi.SiteInfo{}, mapSiteError(err)
		}

		return siteInfo(site), nil
	})

	agentrpc.Handle(s, agentapi.SiteDelete, func(ctx context.Context, _ agentrpc.Caller, req agentapi.SiteDeleteRequest) (agentapi.SiteDeleteResponse, error) {
		h.mu.Lock()
		defer h.mu.Unlock()

		if h.web != nil {
			cur, _, err := h.web.Current()
			if err != nil {
				return agentapi.SiteDeleteResponse{}, err
			}

			if cur.Users()[req.User] {
				return agentapi.SiteDeleteResponse{}, &agentrpc.Error{
					Code: agentrpc.CodePrecondition, Message: "site still has a virtual host, remove it with web.apply first",
				}
			}
		}

		if h.php != nil {
			pools, err := h.php.Current()
			if err != nil {
				return agentapi.SiteDeleteResponse{}, err
			}

			if _, ok := pools[req.User]; ok {
				return agentapi.SiteDeleteResponse{}, &agentrpc.Error{
					Code: agentrpc.CodePrecondition, Message: "site still has a PHP pool, remove it with web.apply first",
				}
			}
		}

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

	agentrpc.Handle(s, agentapi.WebApply, h.webApply)

	agentrpc.Handle(s, agentapi.PHPVersionsList, func(_ context.Context, _ agentrpc.Caller, _ agentapi.PHPVersionsRequest) (agentapi.PHPVersionsResponse, error) {
		if h.php == nil || h.php.Installed == nil {
			return agentapi.PHPVersionsResponse{Installed: []string{}}, nil
		}

		return agentapi.PHPVersionsResponse{Installed: h.php.Installed()}, nil
	})
}

func (h *siteOps) webApply(ctx context.Context, _ agentrpc.Caller, req agentapi.WebApplyRequest) (agentapi.WebApplyResponse, error) {
	if h.web == nil {
		return agentapi.WebApplyResponse{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "nginx is not installed"}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	d := web.Desired{Sites: make([]web.Site, 0, len(req.Sites))}
	pools := []php.Pool{}
	keep := map[string]bool{}

	for _, s := range req.Sites {
		home, ok := h.m.Home(s.User)
		if !ok {
			return agentapi.WebApplyResponse{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site " + s.User}
		}

		site := web.Site{User: s.User, Domains: s.Domains, Root: home + "/" + sites.WebRootName}

		if s.PHP != "" {
			if h.php == nil {
				return agentapi.WebApplyResponse{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "PHP is not available on this server"}
			}

			pools = append(pools, php.Pool{User: s.User, Version: s.PHP, Home: home})
			keep[s.User] = true
			site.PHPSocket = h.php.Socket(s.User)
		}

		d.Sites = append(d.Sites, site)
	}

	// New and changed pools first, so nginx never points at a socket that
	// does not exist yet; pools of sites without PHP only after nginx no
	// longer uses them.
	if h.php != nil {
		if err := h.php.Ensure(ctx, pools); err != nil {
			return agentapi.WebApplyResponse{}, mapPHPError(err)
		}
	}

	res, err := h.web.Apply(ctx, d)
	if err != nil {
		return agentapi.WebApplyResponse{Generation: res.Generation}, mapWebError(err)
	}

	if h.php != nil {
		// The web server change is live; a pool that could not be stopped
		// is only reported. site.delete refuses while it exists, and the
		// next web.apply tries again.
		if err := h.php.Prune(ctx, keep); err != nil {
			h.log.Error("stop php pools of sites without PHP", "err", err)
		}
	}

	return agentapi.WebApplyResponse{Generation: res.Generation, Changed: res.Changed}, nil
}

// mapPHPError gives the caller a fixed message; the details (php-fpm and
// systemctl output) stay in the agent log.
func mapPHPError(err error) error {
	switch {
	case errors.Is(err, php.ErrInvalid):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "invalid PHP state"}
	case errors.Is(err, php.ErrNotInstalled):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the requested PHP version is not installed"}
	case errors.Is(err, php.ErrValidation):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "php-fpm rejected the pool configuration, nothing was changed"}
	case errors.Is(err, php.ErrStartFailed):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the PHP pool did not start, the previous state was restored"}
	default:
		return err
	}
}

// mapWebError gives the caller a fixed message; the details (nginx output)
// stay in the agent log.
func mapWebError(err error) error {
	switch {
	case errors.Is(err, web.ErrInvalidDesired):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "invalid web server state"}
	case errors.Is(err, web.ErrValidation):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "nginx rejected the configuration, nothing was changed"}
	case errors.Is(err, web.ErrApplyFailed):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the new configuration did not go live, the previous one was restored"}
	case errors.Is(err, web.ErrBusy):
		return &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "another web server change is running"}
	default:
		return err
	}
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

// prepareSocketDir gives the socket directory to the serve user's group, so
// the web process can reach the socket. systemd creates it as root:root 0750
// (RuntimeDirectory=hostashi); the agent then only changes the group, on the
// open directory. A directory owned by anyone but root, or open to others,
// is refused.
func prepareSocketDir(dir string, gid int) error {
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("socket directory %s: %w", dir, err)
	}

	defer unix.Close(fd) // read-only fd, nothing to flush

	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("socket directory %s: %w", dir, err)
	}

	if st.Uid != 0 || st.Mode&0o027 != 0 {
		return fmt.Errorf("%w: %s must be owned by root with mode 0750", agentrpc.ErrInsecureSocketDir, dir)
	}

	if int(st.Gid) == gid {
		return nil
	}

	if err := unix.Fchown(fd, -1, gid); err != nil {
		return fmt.Errorf("socket directory %s: %w", dir, err)
	}

	return nil
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

	return sites.NewManager(reg, sysuser.New(log), sites.Options{Log: log, WebGID: webGID(log)}), nil
}

// WebUser is the distribution's nginx worker user and group.
const WebUser = "www-data"

// lookupGroup is replaced in tests.
var lookupGroup = user.LookupGroup

func webGID(log *slog.Logger) uint32 {
	g, err := lookupGroup(WebUser)
	if err == nil {
		if id, perr := strconv.ParseUint(g.Gid, 10, 32); perr == nil && id != 0 {
			return uint32(id)
		}
	}

	if log != nil {
		log.Warn("no web server group, new site homes keep the site's own group", "group", WebUser)
	}

	return 0
}

// openWeb builds the production reconciler, or nil when nginx is missing.
func openWeb(stateDir string, log *slog.Logger) *web.Reconciler {
	if _, err := os.Stat(webctl.Nginx); err != nil {
		if log != nil {
			log.Warn("nginx not installed, web operations disabled", "path", webctl.Nginx)
		}

		return nil
	}

	if stateDir == "" {
		stateDir = DefaultStateDir
	}

	brotli := true

	for _, m := range []string{"50-mod-http-brotli-filter.conf", "50-mod-http-brotli-static.conf"} {
		if _, err := os.Stat(filepath.Join("/etc/nginx/modules-enabled", m)); err != nil {
			brotli = false
		}
	}

	listen := []string{"80"}
	if ipv6Available() {
		listen = append(listen, "[::]:80")
	}

	return &web.Reconciler{
		Base: filepath.Join(stateDir, "web"),
		Log:  log,
		Driver: &web.Nginx{
			Ctl:           webctl.Controller{},
			Listen:        listen,
			ControlSocket: WebControlSocket,
			User:          WebUser,
			PIDFile:       "/run/nginx.pid",
			ErrorLog:      "/var/log/nginx/error.log",
			AccessLog:     "/var/log/nginx/access.log",
			Modules:       "/etc/nginx/modules-enabled/*.conf",
			MimeTypes:     "/etc/nginx/mime.types",
			Brotli:        brotli,
		},
	}
}

// ipv6Available reports whether the kernel can create IPv6 sockets. nginx
// refuses the whole config if a listen address has an unsupported family.
func ipv6Available() bool {
	fd, err := unix.Socket(unix.AF_INET6, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return false
	}

	_ = unix.Close(fd)

	return true
}

// PHPSocketDir holds one directory per site with its FPM socket, created by
// the site's unit (RuntimeDirectory=hostashi-php/%i).
const PHPSocketDir = "/run/hostashi-php"

// openPHP builds the production PHP manager. Without systemctl (not a
// systemd host) PHP is unavailable.
func openPHP(stateDir string, log *slog.Logger) *php.Manager {
	if _, err := os.Stat(phpctl.Systemctl); err != nil {
		if log != nil {
			log.Warn("systemctl not found, PHP disabled", "path", phpctl.Systemctl)
		}

		return nil
	}

	if stateDir == "" {
		stateDir = DefaultStateDir
	}

	return &php.Manager{
		Dir:       filepath.Join(stateDir, "php"),
		SocketDir: PHPSocketDir,
		WebGroup:  WebUser,
		Ctl:       phpctl.Controller{},
		Installed: phpctl.Installed,
		Log:       log,
	}
}

// WebControlSocket is where nginx answers the agent's checks. Its directory
// is the RuntimeDirectory of the nginx unit drop-in (root, 0700).
const WebControlSocket = "/run/hostashi-web/control.sock"

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

	if cfg.Web == nil {
		cfg.Web = openWeb(cfg.StateDir, cfg.Log)
	}

	if cfg.PHP == nil {
		cfg.PHP = openPHP(cfg.StateDir, cfg.Log)
	}

	if cfg.PHP != nil {
		// Like the web server: a failure is logged, web.apply retries.
		if err := cfg.PHP.Recover(ctx); err != nil && cfg.Log != nil {
			cfg.Log.Error("php pool recovery failed", "err", err)
		}
	}

	if cfg.Web != nil {
		// A failure here must not keep the agent down: web.apply retries.
		if res, err := cfg.Web.Recover(ctx); err != nil {
			if cfg.Log != nil {
				cfg.Log.Error("web server recovery failed", "err", err)
			}
		} else if cfg.Log != nil {
			cfg.Log.Info("web server on generation", "generation", res.Generation)
		}
	}

	if err := prepareSocketDir(filepath.Dir(cfg.Socket), gid); err != nil {
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
