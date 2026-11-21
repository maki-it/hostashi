// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agent is the privileged side: hostashi agent runs as root, has no
// network listener and only executes the typed operations in agentapi.
package agent

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/backup"
	"github.com/maki-it/hostashi/internal/certs"
	"github.com/maki-it/hostashi/internal/cpimport"
	"github.com/maki-it/hostashi/internal/cpsource"
	"github.com/maki-it/hostashi/internal/cronjobs"
	"github.com/maki-it/hostashi/internal/dbjob"
	"github.com/maki-it/hostashi/internal/filesunit"
	"github.com/maki-it/hostashi/internal/firewall"
	"github.com/maki-it/hostashi/internal/mariadb"
	"github.com/maki-it/hostashi/internal/netaddrs"
	"github.com/maki-it/hostashi/internal/objcache"
	"github.com/maki-it/hostashi/internal/pagecache"
	"github.com/maki-it/hostashi/internal/pages"
	"github.com/maki-it/hostashi/internal/php"
	"github.com/maki-it/hostashi/internal/phpctl"
	"github.com/maki-it/hostashi/internal/phpinstall"
	"github.com/maki-it/hostashi/internal/resticctl"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/siteslice"
	"github.com/maki-it/hostashi/internal/sitestats"
	"github.com/maki-it/hostashi/internal/snippet"
	"github.com/maki-it/hostashi/internal/sshaccess"
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

	// Certs keeps the sites' TLS keys and certificates. Run builds it from
	// StateDir; without it tls.* and sites with TLS are refused.
	Certs *certs.Store

	// PanelPort is the HTTPS port of the panel's own address (ADR-0023);
	// 0 means DefaultPanelPort, 443 shares the sites' port (ADR-0042).
	PanelPort int

	// DBToolPort is the HTTPS port of the web database tool (ADR-0038);
	// 0 serves no tool.
	DBToolPort int

	// DB manages the sites' MariaDB databases. Run builds it from StateDir;
	// without it db.* answer failed_precondition.
	DB *mariadb.Manager

	// DBToolDir is the ticket directory of the web database tool
	// (ADR-0038); empty means agentapi.DBToolTicketDir.
	DBToolDir string
	// DBToolUnits restarts the tool's pool for the external database
	// servers; nil: none (ADR-0041 addendum 1).
	DBToolUnits DBToolUnits

	// Backup serves backup.*. Run builds it with the fixed paths of a
	// server; without it the operations answer failed_precondition.
	Backup *backup.Agent

	// CloudPanel keeps the migration source (ADR-0026). Run builds it below
	// StateDir; without it cloudpanel.* answer failed_precondition.
	CloudPanel *cpsource.Source
	// CloudPanelImport starts the import jobs (ADR-0026). Run builds it
	// below StateDir with CloudPanel; without it cloudpanel.import answers
	// failed_precondition.
	CloudPanelImport *cpimport.Agent

	// DBJobs imports and exports databases on external servers (ADR-0041
	// addendum 1). Run builds it below StateDir; without it db.remote.*
	// answer failed_precondition.
	DBJobs *dbjob.Agent

	// Pages keeps the maintenance and parking pages (ADR-0027). Run builds
	// it at PagesDir; without it page.* answer failed_precondition.
	Pages *pages.Store

	// Cron keeps the sites' cron jobs as systemd units (ADR-0028). Run
	// builds it; without it cron.* answer failed_precondition.
	Cron *cronjobs.Manager

	// SSH keeps the sites' SSH keys and login shells (ADR-0032). Run builds
	// it; without it ssh.apply answers failed_precondition.
	SSH *sshaccess.Manager

	// Slices keeps the sites' slices with their limits (ADR-0015). Run
	// builds it on /etc/systemd/system; without it site.limits.* answer
	// failed_precondition.
	Slices *siteslice.Manager

	// Disk caches the sites' disk usage for metrics.snapshot; Run fills it
	// every agentapi.DiskEvery. Nil: an empty cache.
	Disk *sitestats.DiskCache
	// CgroupRoot is where cgroup v2 is mounted; empty means
	// sitestats.DefaultCgroupRoot.
	CgroupRoot string

	// Quota sets and reads the sites' disk limits (ADR-0015). Nil: the
	// running kernel (KernelQuota).
	Quota QuotaFS

	// Firewall keeps the panel's firewall rules (ADR-0034). Run builds it
	// with the fixed paths; without it firewall.* answer
	// failed_precondition.
	Firewall *firewall.Manager

	// panelACL hands the panel allowlist to nginx on port 443 (ADR-0042);
	// Run sets it up.
	panelACL *panelACL

	// PHPInstall installs more PHP versions through the fixed install unit
	// (ADR-0036). Run builds it; without it php.install answers
	// failed_precondition.
	PHPInstall *phpinstall.Manager

	// PageCache keeps the sites' page cache directories (ADR-0046). Run
	// builds it for nginx's worker user; without it a page cache is
	// refused.
	PageCache *pagecache.Dirs
	// ObjectCache runs the sites' object caches (ADR-0045). Run builds it below
	// StateDir; without it cache.* answer failed_precondition.
	ObjectCache *objcache.Manager

	// Files starts the sites' file manager sockets (ADR-0040). Run builds
	// it; without it files.open answers failed_precondition.
	Files *filesunit.Manager

	// Doctor overrides what doctor.run reads, for tests. The zero value
	// reads the server.
	Doctor DoctorEnv

	// adminUID is the caller that may add and remove storage pools: root,
	// except in unprivileged tests.
	adminUID uint32
}

// DefaultPanelPort is the panel's HTTPS port (ADR-0023).
const DefaultPanelPort = 8443

// PanelSocket is the panel's Unix socket that nginx proxies to. systemd
// creates it (hostashi-panel.socket) for hostashi and www-data only.
const PanelSocket = "/run/hostashi-panel.sock"

// DBToolSocket is the FastCGI socket of the web database tool's PHP-FPM
// (packaging/adminer), DBToolRoot its files (ADR-0038).
const (
	DBToolSocket = "/run/hostashi-adminer-fpm/fpm.sock"
	DBToolRoot   = "/usr/share/hostashi/adminer"
)

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
	agentrpc.Handle(s, agentapi.ServerAddrs, func(context.Context, agentrpc.Caller, agentapi.ServerAddrsRequest) (agentapi.ServerAddrsResponse, error) {
		addrs, err := netaddrs.Public()
		if err != nil {
			return agentapi.ServerAddrsResponse{}, err
		}

		out := agentapi.ServerAddrsResponse{Addrs: make([]string, 0, len(addrs))}
		for _, a := range addrs {
			out.Addrs = append(out.Addrs, a.String())
		}

		return out, nil
	})

	var h *siteOps

	if cfg.Sites != nil {
		log := cfg.Log
		if log == nil {
			log = slog.New(slog.DiscardHandler)
		}

		h = &siteOps{m: cfg.Sites, web: cfg.Web, php: cfg.PHP, certs: cfg.Certs, db: cfg.DB, pages: cfg.Pages, cron: cfg.Cron, ssh: cfg.SSH, quota: cfg.Quota, slices: cfg.Slices, phpi: cfg.PHPInstall, cache: cfg.PageCache, objc: cfg.ObjectCache, files: cfg.Files, log: log}
		h.acl = cfg.panelACL

		if cfg.PHP != nil && cfg.PHP.Hidden == nil {
			cfg.PHP.Hidden = cfg.Sites.HiddenPaths
		}

		h.register(s)
		h.registerPools(s, cfg.adminUID)

		if cfg.Backup != nil && cfg.Backup.Space == nil {
			cfg.Backup.Space = h.freeSpace
		}

		disk, root := cfg.Disk, cfg.CgroupRoot
		if disk == nil {
			disk = &sitestats.DiskCache{}
		}

		if root == "" {
			root = sitestats.DefaultCgroupRoot
		}

		(&statsOps{h: h, disk: disk, cgroupRoot: root}).register(s)
	}

	toolLog := cfg.Log
	if toolLog == nil {
		toolLog = slog.New(slog.DiscardHandler)
	}

	tool := newDBTool(cfg.DB, cfg.DBToolDir, toolLog)
	tool.units = cfg.DBToolUnits
	tool.register(s)

	registerDoctor(s, cfg.Doctor, cfg.StateDir, h, cfg.Firewall, cfg.DBToolPort > 0)
	registerBackup(s, cfg.Backup)
	registerCloudPanel(s, cfg.CloudPanel, cfg.CloudPanelImport)
	registerDBJobs(s, cfg.DBJobs)
	registerFirewall(s, cfg.Firewall)
	registerPHPInstall(s, cfg.PHPInstall)

	return s
}

// siteOps serves the site and web operations. mu orders site.delete against
// web.apply, so a site cannot lose its home while a virtual host or a PHP
// pool is being built for it.
type siteOps struct {
	m      *sites.Manager
	web    *web.Reconciler
	php    *php.Manager
	phpi   *phpinstall.Manager
	certs  *certs.Store
	db     *mariadb.Manager
	pages  *pages.Store
	cron   *cronjobs.Manager
	ssh    *sshaccess.Manager
	quota  QuotaFS
	slices *siteslice.Manager
	cache  *pagecache.Dirs
	objc   *objcache.Manager
	files  *filesunit.Manager
	log    *slog.Logger
	mu     sync.Mutex
	// acl hands the panel allowlist to nginx on 443; nil for none.
	acl *panelACL
	// sweepMu keeps the background deletes of purged caches apart.
	sweepMu sync.Mutex
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

		site, home, siteErr := h.site(req.User)

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

		if h.db != nil && h.db.SiteHasDatabases(req.User) {
			return agentapi.SiteDeleteResponse{}, &agentrpc.Error{
				Code: agentrpc.CodePrecondition, Message: "site still has databases, remove them with db.delete first",
			}
		}

		// The helper runs as the site user; it must be gone before the
		// user is. Opening the files again starts it anew.
		if h.files != nil {
			if err := h.files.Remove(ctx, req.User); err != nil {
				return agentapi.SiteDeleteResponse{}, fmt.Errorf("stop file manager of site %s: %w", req.User, err)
			}
		}

		if err := m.Delete(ctx, req.User); err != nil {
			return agentapi.SiteDeleteResponse{}, mapSiteError(err)
		}

		if siteErr == nil {
			h.clearQuota(ctx, poolOf(home), site.UID)
		}

		h.removeCache(ctx, req.User)

		if h.slices != nil {
			uid := uint32(0) // not known: only the site's slice goes
			if siteErr == nil {
				uid = site.UID
			}

			if err := h.slices.Remove(ctx, req.User, uid); err != nil {
				h.log.ErrorContext(ctx, "remove slice of deleted site", "site", req.User, "err", err)
			}
		}

		if h.cron != nil {
			if err := h.cron.Remove(ctx, req.User); err != nil {
				h.log.ErrorContext(ctx, "remove cron jobs of deleted site", "site", req.User, "err", err)
			}
		}

		if h.objc != nil {
			if err := h.objc.Remove(ctx, req.User); err != nil {
				h.log.ErrorContext(ctx, "remove object cache of deleted site", "site", req.User, "err", err)
			}
		}

		if h.ssh != nil {
			if err := h.ssh.Remove(ctx, req.User); err != nil {
				h.log.ErrorContext(ctx, "remove ssh keys of deleted site", "site", req.User, "err", err)
			}
		}

		if h.certs != nil {
			if err := h.certs.Remove(req.User); err != nil {
				h.log.ErrorContext(ctx, "remove certificate of deleted site", "site", req.User, "err", err)
			}
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

	agentrpc.Handle(s, agentapi.WebApply, func(ctx context.Context, c agentrpc.Caller, req agentapi.WebApplyRequest) (agentapi.WebApplyResponse, error) {
		resp, err := h.webApply(ctx, c, req)
		if err == nil {
			h.cronDisabled(ctx, req)
			h.sshDisabled(ctx, req)
			h.cacheDisabled(ctx, req)
		}

		return resp, err
	})

	h.registerCron(s)

	h.registerCache(s)
	h.registerFiles(s)

	h.registerSSH(s)

	h.registerPages(s)

	h.registerQuota(s)

	h.registerLimits(s)

	h.registerDB(s)

	agentrpc.Handle(s, agentapi.TLSCSR, h.tlsCSR)
	agentrpc.Handle(s, agentapi.TLSInstall, h.tlsInstall)
	agentrpc.Handle(s, agentapi.TLSUpload, h.tlsUpload)
	agentrpc.Handle(s, agentapi.TLSInfo, h.tlsInfo)
	agentrpc.Handle(s, agentapi.TLSDelete, h.tlsDelete)

	agentrpc.Handle(s, agentapi.SnippetCheck, h.snippetCheck)

	agentrpc.Handle(s, agentapi.PageCachePurge, h.pageCachePurge)

	agentrpc.Handle(s, agentapi.PHPVersionsList, func(_ context.Context, _ agentrpc.Caller, _ agentapi.PHPVersionsRequest) (agentapi.PHPVersionsResponse, error) {
		if h.php == nil || h.php.Installed == nil {
			return agentapi.PHPVersionsResponse{Installed: []string{}, Newer: []string{}}, nil
		}

		res := agentapi.PHPVersionsResponse{Installed: h.php.Installed(), Newer: []string{}}
		if h.phpi != nil {
			res.Repository, res.Newer = h.phpi.Repository(), h.phpi.Newer()
		}

		return res, nil
	})
}

func (h *siteOps) webApply(ctx context.Context, _ agentrpc.Caller, req agentapi.WebApplyRequest) (agentapi.WebApplyResponse, error) {
	if h.web == nil {
		return agentapi.WebApplyResponse{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "nginx is not installed"}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.acl != nil {
		h.acl.mu.Lock()
		defer h.acl.mu.Unlock()
	}

	d := web.Desired{Sites: make([]web.Site, 0, len(req.Sites))}
	pools := []php.Pool{}
	keep := map[string]bool{}

	for _, s := range req.Sites {
		home, ok := h.m.Home(s.User)
		if !ok {
			return agentapi.WebApplyResponse{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site " + s.User}
		}

		site := web.Site{User: s.User, Domains: s.Domains, Root: home + "/" + sites.WebRootName, Disabled: s.Disabled}

		if s.TLS {
			if err := h.tlsFor(&site, s.Domains); err != nil {
				return agentapi.WebApplyResponse{}, err
			}

			site.HSTS = s.HSTS
		}

		if s.DisabledPage != nil && s.Disabled {
			m, err := h.maintenanceFor(&agentapi.WebMaintenance{Page: s.DisabledPage.Page, Digest: s.DisabledPage.Digest})
			if err != nil {
				return agentapi.WebApplyResponse{}, err
			}

			site.DisabledPage = &web.PageRef{Page: m.Page, Digest: m.Digest, Dir: m.Dir}
		}

		if s.Maintenance != nil && !s.Disabled {
			m, err := h.maintenanceFor(s.Maintenance)
			if err != nil {
				return agentapi.WebApplyResponse{}, err
			}

			site.Maintenance = m
		}

		if s.PHP != "" {
			if h.php == nil {
				return agentapi.WebApplyResponse{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "PHP is not available on this server"}
			}

			pools = append(pools, php.Pool{User: s.User, Version: s.PHP, Home: home, Workers: s.PHPWorkers, Programs: s.PHPPrograms, Settings: s.PHPSettings})
			keep[s.User] = true
			site.PHPSocket = h.php.Socket(s.User)

			// nginx follows the PHP settings (ADR-0049).
			var ps agentapi.PHPSettings
			if s.PHPSettings != nil {
				ps = *s.PHPSettings
			}

			site.BodyMax = ps.ClientMaxBodySize()
			site.FastCGIReadTimeout = ps.FastCGIReadTimeout()
		}

		if s.PageCache != nil {
			c, err := h.pageCacheFor(s.User, home, *s.PageCache)
			if err != nil {
				return agentapi.WebApplyResponse{}, err
			}

			site.PageCache = c
		}

		// Snippets are checked again by package web (ADR-0029); a disabled
		// site or one in maintenance does not render them.
		if sn := s.Snippets; sn != nil && !site.Disabled && site.Maintenance == nil {
			site.Home = home
			site.Snippets = web.Snippets{Server: sn.Server, Root: sn.Root}
		}

		d.Sites = append(d.Sites, site)
	}

	for _, p := range req.Parked {
		parked, err := h.parkedFor(p)
		if err != nil {
			return agentapi.WebApplyResponse{}, err
		}

		d.Parked = append(d.Parked, parked)
	}

	if req.Panel != nil {
		panel, err := h.panelFor(*req.Panel)
		if err != nil {
			return agentapi.WebApplyResponse{}, err
		}

		if h.acl != nil {
			h.acl.fill(panel)
		}

		d.Panel = panel
	}

	// New and changed pools first, so nginx never points at a socket that
	// does not exist yet; pools of sites without PHP only after nginx no
	// longer uses them.
	if h.php != nil {
		if err := h.php.Ensure(ctx, pools); err != nil {
			return agentapi.WebApplyResponse{}, mapPHPError(err)
		}
	}

	prev, _, err := h.web.Current()
	if err != nil {
		return agentapi.WebApplyResponse{}, err
	}

	res, err := h.web.Apply(ctx, d)
	if err != nil {
		return agentapi.WebApplyResponse{Generation: res.Generation}, mapWebError(err)
	}

	h.applyCacheChanges(ctx, prev, d)

	if h.php != nil {
		// The web server change is live; a pool that could not be stopped
		// is only reported. site.delete refuses while it exists, and the
		// next web.apply tries again.
		if err := h.php.Prune(ctx, keep); err != nil {
			h.log.ErrorContext(ctx, "stop php pools of sites without PHP", "err", err)
		}
	}

	if h.pages != nil {
		if err := h.pages.Prune(usedPages(d)); err != nil {
			h.log.ErrorContext(ctx, "prune old page versions", "err", err)
		}
	}

	h.pruneParkCerts(ctx, d)

	return agentapi.WebApplyResponse{Generation: res.Generation, Changed: res.Changed}, nil
}

// tlsFor puts the live certificate of a site into site, after checking that
// it is valid and names every domain.
func (h *siteOps) tlsFor(site *web.Site, domains []string) error {
	if h.certs == nil || len(h.web.Driver.ListenTLS) == 0 {
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "HTTPS is not available on this server"}
	}

	ok, err := h.certs.Covers(site.User, domains)
	if err != nil {
		return err
	}

	if !ok {
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "site " + site.User + " has no valid certificate for all its domains"}
	}

	site.Cert, site.Key = h.certs.CertPath(site.User), h.certs.KeyPath(site.User)

	return nil
}

// panelFor builds the panel's web state; with TLS its certificate must be
// valid and name the domain.
func (h *siteOps) panelFor(p agentapi.WebPanel) (*web.Panel, error) {
	if len(h.web.Driver.PanelListen) == 0 {
		return nil, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the panel address is not available on this server"}
	}

	panel := &web.Panel{Domain: p.Domain}
	if !p.TLS {
		return panel, nil
	}

	if h.certs == nil || len(h.web.Driver.ListenTLS) == 0 {
		return nil, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "HTTPS is not available on this server"}
	}

	ok, err := h.certs.Covers(agentapi.PanelCert, []string{p.Domain})
	if err != nil {
		return nil, err
	}

	if !ok {
		return nil, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the panel has no valid certificate for " + p.Domain}
	}

	panel.Cert, panel.Key = h.certs.CertPath(agentapi.PanelCert), h.certs.KeyPath(agentapi.PanelCert)

	if p.DBTool {
		if len(h.web.Driver.DBToolListen) == 0 {
			return nil, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the database tool is not installed on this server"}
		}

		panel.DBTool = true
	}

	return panel, nil
}

// knownCertName accepts the panel's certificate, a parked domain's or a
// registered site.
func (h *siteOps) knownCertName(name string) bool {
	if name == agentapi.PanelCert || agentapi.ValidateParkName(name) == nil {
		return true
	}

	_, ok := h.m.Home(name)

	return ok
}

func (h *siteOps) tlsCSR(_ context.Context, _ agentrpc.Caller, req agentapi.TLSCSRRequest) (agentapi.TLSCSRResponse, error) {
	if h.certs == nil {
		return agentapi.TLSCSRResponse{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "HTTPS is not available on this server"}
	}

	if !h.knownCertName(req.User) {
		return agentapi.TLSCSRResponse{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site"}
	}

	create := h.certs.CSR
	if req.Custom {
		create = h.certs.CustomCSR
	}

	csr, err := create(req.User, req.Domains)
	if err != nil {
		return agentapi.TLSCSRResponse{}, mapCertError(err)
	}

	return agentapi.TLSCSRResponse{CSR: string(csr)}, nil
}

func (h *siteOps) tlsInstall(_ context.Context, _ agentrpc.Caller, req agentapi.TLSInstallRequest) (agentapi.TLSCertInfo, error) {
	if h.certs == nil {
		return agentapi.TLSCertInfo{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "HTTPS is not available on this server"}
	}

	if !h.knownCertName(req.User) {
		return agentapi.TLSCertInfo{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site"}
	}

	// The live pair changes; web.apply reads it under the same lock.
	h.mu.Lock()
	defer h.mu.Unlock()

	info, err := h.certs.Install(req.User, []byte(req.Chain))
	if err != nil {
		return agentapi.TLSCertInfo{}, mapCertError(err)
	}

	return agentapi.TLSCertInfo{Domains: info.Domains, NotAfter: info.NotAfter}, nil
}

func (h *siteOps) tlsUpload(ctx context.Context, _ agentrpc.Caller, req agentapi.TLSUploadRequest) (agentapi.TLSCertInfo, error) {
	if h.certs == nil {
		return agentapi.TLSCertInfo{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "HTTPS is not available on this server"}
	}

	if !h.knownCertName(req.User) {
		return agentapi.TLSCertInfo{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site"}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	info, err := h.certs.Upload(req.User, certs.Upload{
		Chain: []byte(req.Chain), Key: []byte(req.Key), Domains: req.Domains, AllowUntrusted: req.AllowUntrusted,
	})
	if err != nil {
		return agentapi.TLSCertInfo{}, mapCertError(err)
	}

	h.log.InfoContext(ctx, "own certificate installed", "name", req.User, "fingerprint", info.Fingerprint, "trusted", info.Trusted)

	return certInfo(info), nil
}

func (h *siteOps) tlsInfo(_ context.Context, _ agentrpc.Caller, req agentapi.TLSNameRequest) (agentapi.TLSInfoResponse, error) {
	if h.certs == nil {
		return agentapi.TLSInfoResponse{}, nil
	}

	var res agentapi.TLSInfoResponse

	info, err := h.certs.Details(req.User)

	switch {
	case err == nil:
		ci := certInfo(info)
		res.Cert = &ci
	case !errors.Is(err, certs.ErrNotFound):
		return agentapi.TLSInfoResponse{}, mapCertError(err)
	}

	csr, err := h.certs.PendingCSR(req.User)
	if err != nil {
		return agentapi.TLSInfoResponse{}, mapCertError(err)
	}

	res.CSR = string(csr)

	return res, nil
}

func (h *siteOps) tlsDelete(ctx context.Context, _ agentrpc.Caller, req agentapi.TLSNameRequest) (agentapi.TLSDeleteResponse, error) {
	if h.certs == nil {
		return agentapi.TLSDeleteResponse{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "HTTPS is not available on this server"}
	}

	// web.apply reads the pair under the same lock.
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.web != nil {
		cur, _, err := h.web.Current()
		if err != nil {
			return agentapi.TLSDeleteResponse{}, err
		}

		if servesTLS(cur, req.User) {
			return agentapi.TLSDeleteResponse{}, &agentrpc.Error{
				Code: agentrpc.CodePrecondition, Message: "the certificate is still served, turn HTTPS off with web.apply first",
			}
		}
	}

	if err := h.certs.Delete(req.User); err != nil {
		if errors.Is(err, certs.ErrNotFound) {
			return agentapi.TLSDeleteResponse{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "no certificate"}
		}

		return agentapi.TLSDeleteResponse{}, mapCertError(err)
	}

	h.log.InfoContext(ctx, "certificate deleted", "name", req.User)

	return agentapi.TLSDeleteResponse{}, nil
}

// servesTLS reports whether the web state d serves the certificate name.
func servesTLS(d web.Desired, name string) bool {
	if name == agentapi.PanelCert {
		return d.Panel != nil && d.Panel.Cert != ""
	}

	for _, p := range d.Parked {
		if p.Name == name && p.Cert != "" {
			return true
		}
	}

	for _, s := range d.Sites {
		if s.User == name && s.Cert != "" {
			return true
		}
	}

	return false
}

func certInfo(i certs.Info) agentapi.TLSCertInfo {
	return agentapi.TLSCertInfo{
		Domains: i.Domains, NotAfter: i.NotAfter, NotBefore: i.NotBefore,
		Issuer: i.Issuer, Fingerprint: i.Fingerprint, Trusted: i.Trusted,
	}
}

func mapCertError(err error) error {
	var (
		missing   *certs.MissingNamesError
		untrusted *certs.UntrustedError
	)

	switch {
	case errors.As(err, &missing):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the certificate does not cover " + strings.Join(missing.Names, ", ")}
	case errors.As(err, &untrusted):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the chain is not publicly trusted: " + untrusted.Reason}
	case errors.Is(err, certs.ErrKeyType), errors.Is(err, certs.ErrKeyMismatch), errors.Is(err, certs.ErrEncrypted),
		errors.Is(err, certs.ErrValidity), errors.Is(err, certs.ErrUsage):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: strings.TrimPrefix(err.Error(), "certs: ")}
	case errors.Is(err, certs.ErrNoCustomKey):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "no key given and no pending CSR"}
	case errors.Is(err, certs.ErrInvalid):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "invalid certificate or request"}
	case errors.Is(err, certs.ErrNoPendingKey):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the certificate does not belong to the pending key of this site"}
	default:
		return err
	}
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
func openWeb(ctx context.Context, stateDir string, panelPort, dbToolPort int, log *slog.Logger) *web.Reconciler {
	if _, err := os.Stat(webctl.Nginx); err != nil {
		if log != nil {
			log.WarnContext(ctx, "nginx not installed, web operations disabled", "path", webctl.Nginx)
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

	// HTTP/3 where nginx has it (Debian 13 and Ubuntu 26.04 do).
	vctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	http3, err := webctl.Controller{}.HasHTTP3(vctx)

	cancel()

	if err != nil && log != nil {
		log.WarnContext(ctx, "cannot tell whether nginx has HTTP/3, leaving it off", "err", err)
	}

	if panelPort == 0 {
		panelPort = DefaultPanelPort
	}

	pp := strconv.Itoa(panelPort)

	listen, listenTLS, listenPanel := []string{"80"}, []string{"443"}, []string{pp}

	var listenTool []string
	if dbToolPort != 0 {
		listenTool = []string{strconv.Itoa(dbToolPort)}
	}

	if ipv6Available() {
		listen, listenTLS, listenPanel = append(listen, "[::]:80"), append(listenTLS, "[::]:443"), append(listenPanel, "[::]:"+pp)
		if dbToolPort != 0 {
			listenTool = append(listenTool, "[::]:"+strconv.Itoa(dbToolPort))
		}
	}

	// On 443 the panel shares the sites' listeners by host name (ADR-0042).
	if panelPort == 443 {
		listenPanel = listenTLS
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
			ListenTLS:     listenTLS,
			HTTP3:         http3,
			ACMERoot:      ACMERoot,
			PanelListen:   listenPanel,
			PanelUpstream: PanelSocket,
			DBToolListen:  listenTool,
			DBToolSocket:  DBToolSocket,
			DBToolRoot:    DBToolRoot,
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

// ACMERoot is where the panel puts HTTP-01 challenge files
// (.well-known/acme-challenge/<token>); nginx serves them on port 80 for
// every site. The installer creates it (hostashi writes, www-data reads).
const ACMERoot = "/var/lib/hostashi-acme"

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
		Units:     phpctl.Controller{},
		UnitDir:   siteslice.DefaultDir,
		Slice:     siteslice.Name,
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
		cfg.Web = openWeb(ctx, cfg.StateDir, cfg.PanelPort, cfg.DBToolPort, cfg.Log)
	}

	if cfg.PHP == nil {
		cfg.PHP = openPHP(cfg.StateDir, cfg.Log)
	}

	if cfg.Certs == nil {
		dir := cfg.StateDir
		if dir == "" {
			dir = DefaultStateDir
		}

		cfg.Certs = &certs.Store{Dir: filepath.Join(dir, "tls")}
	}

	if cfg.DB == nil {
		cfg.DB, err = openDB(ctx, cfg.StateDir, cfg.Sites, cfg.Log)
		if err != nil {
			return err
		}
	}

	// Tickets of an earlier run name users that openDB drops.
	if err := clearTickets(cfg.DBToolDir); err != nil && cfg.Log != nil {
		cfg.Log.WarnContext(ctx, "remove leftover database tool tickets", "err", err)
	}

	if cfg.Backup == nil {
		cfg.Backup = openBackup()
	}

	if cfg.Pages == nil {
		cfg.Pages = &pages.Store{Dir: PagesDir}
	}

	if cfg.Slices == nil {
		cfg.Slices = &siteslice.Manager{Dir: siteslice.DefaultDir, Ctl: phpctl.Controller{}, ControlDir: siteslice.DefaultControlDir}
	}

	if cfg.SSH == nil {
		cfg.SSH = &sshaccess.Manager{
			StateFile: filepath.Join(cmp.Or(cfg.StateDir, DefaultStateDir), "ssh.json"),
			Shells:    sysuser.New(cfg.Log), Log: cfg.Log,
		}
	}

	if cfg.Cron == nil {
		cfg.Cron = &cronjobs.Manager{
			StateFile: filepath.Join(cmp.Or(cfg.StateDir, DefaultStateDir), "cron.json"),
			Ctl:       phpctl.CronController{}, Log: cfg.Log,
		}
	}

	cpDir := filepath.Join(cmp.Or(cfg.StateDir, DefaultStateDir), "cloudpanel")

	if cfg.CloudPanel == nil {
		cfg.CloudPanel = &cpsource.Source{Dir: cpDir}
	}

	if cfg.CloudPanelImport == nil {
		cfg.CloudPanelImport = &cpimport.Agent{
			Dir: filepath.Join(cpDir, "imports"), StateDir: cmp.Or(cfg.StateDir, DefaultStateDir),
			Source: cfg.CloudPanel, Units: resticctl.Units{},
		}
	}

	if cfg.DBToolUnits == nil && cfg.DBToolPort > 0 {
		cfg.DBToolUnits = resticctl.Units{}
	}

	if cfg.DBJobs == nil {
		cfg.DBJobs = &dbjob.Agent{
			Dir: filepath.Join(cmp.Or(cfg.StateDir, DefaultStateDir), "dbjobs"), StateDir: cmp.Or(cfg.StateDir, DefaultStateDir),
			Units: resticctl.Units{},
		}
	}

	if cfg.ObjectCache == nil {
		cfg.ObjectCache = &objcache.Manager{
			Dir:   filepath.Join(cmp.Or(cfg.StateDir, DefaultStateDir), "valkey"),
			Slice: siteslice.Name, Ctl: phpctl.CacheController{}, Log: cfg.Log,
		}

		// Like PHP: a failure is logged, cache.apply retries.
		if err := cfg.ObjectCache.Recover(ctx); err != nil && cfg.Log != nil {
			cfg.Log.ErrorContext(ctx, "object cache recovery failed", "err", err)
		}
	}

	if cfg.Files == nil {
		cfg.Files = &filesunit.Manager{UnitDir: filesunit.DefaultUnitDir, Ctl: phpctl.FilesController{}, Log: cfg.Log}
	}

	if cfg.PHPInstall == nil {
		cfg.PHPInstall = &phpinstall.Manager{Units: phpctl.InstallUnits{}, Installed: phpctl.Installed}
	}

	if cfg.Firewall == nil {
		cfg.Firewall = &firewall.Manager{
			Dir: filepath.Join(cmp.Or(cfg.StateDir, DefaultStateDir), "firewall"), Base: firewall.BaseFile,
			Reload: phpctl.ReloadFirewall, Log: cfg.Log,
		}
	}

	// A pending change whose deadline passed while the agent was down is
	// rolled back here.
	if err := cfg.Firewall.Recover(ctx); err != nil && cfg.Log != nil {
		cfg.Log.ErrorContext(ctx, "firewall recovery failed", "err", err)
	}

	if cfg.PHP != nil {
		// Like the web server: a failure is logged, web.apply retries.
		if err := cfg.PHP.Recover(ctx); err != nil && cfg.Log != nil {
			cfg.Log.ErrorContext(ctx, "php pool recovery failed", "err", err)
		}
	}

	if cfg.Web != nil {
		// A failure here must not keep the agent down: web.apply retries.
		if res, err := cfg.Web.Recover(ctx); err != nil {
			if cfg.Log != nil {
				cfg.Log.ErrorContext(ctx, "web server recovery failed", "err", err)
			}
		} else if cfg.Log != nil {
			cfg.Log.InfoContext(ctx, "web server on generation", "generation", res.Generation)
		}
	}

	// After both recoveries: nginx gets the firewall's panel allowlist
	// when the panel is on 443, and every later firewall change with it.
	cfg.panelACL = &panelACL{web: cfg.Web}
	if err := cfg.Firewall.SetPanelHook(ctx, cfg.panelACL.set); err != nil && cfg.Log != nil {
		cfg.Log.ErrorContext(ctx, "panel allowlist in nginx", "err", err)
	}

	if err := prepareSocketDir(filepath.Dir(cfg.Socket), gid); err != nil {
		return err
	}

	if cfg.Disk == nil {
		cfg.Disk = &sitestats.DiskCache{}
	}

	if cfg.PageCache == nil {
		cfg.PageCache = openPageCache()
	}

	s := NewServer(cfg, uid)
	if err := s.Listen(ctx, cfg.Socket, gid); err != nil {
		return err
	}

	go diskLoop(ctx, cfg.Disk, func() map[string]string { return homes(cfg.Sites) }, cfg.Log)

	if cfg.Log != nil {
		cfg.Log.InfoContext(ctx, "agent listening", "socket", cfg.Socket, "serveUser", cfg.ServeUser, "serveUid", uid)
	}

	return s.Serve(ctx)
}

// snippetCheck runs the allowlist check (ADR-0029) with the site's home
// from the registry and reports the problems per line.
func (h *siteOps) snippetCheck(_ context.Context, _ agentrpc.Caller, req agentapi.SnippetCheckRequest) (agentapi.SnippetCheckResponse, error) {
	home, ok := h.m.Home(req.User)
	if !ok {
		return agentapi.SnippetCheckResponse{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site " + req.User}
	}

	site := snippet.Site{Home: home}
	if req.PHP && h.php != nil {
		site.PHPSocket = h.php.Socket(req.User)
	}

	res := agentapi.SnippetCheckResponse{
		Server: []agentapi.SnippetProblem{}, Root: []agentapi.SnippetProblem{},
		Home: home, WebRoot: home + "/" + sites.WebRootName, PHPSocket: site.PHPSocket,
	}

	for _, c := range []struct {
		src  string
		slot snippet.Slot
		out  *[]agentapi.SnippetProblem
	}{
		{req.Snippets.Server, snippet.SlotServer, &res.Server},
		{req.Snippets.Root, snippet.SlotRoot, &res.Root},
	} {
		_, err := snippet.Check(c.src, c.slot, site)
		if err == nil {
			continue
		}

		var se *snippet.Error
		if !errors.As(err, &se) {
			return agentapi.SnippetCheckResponse{}, err
		}

		for _, p := range se.Problems {
			*c.out = append(*c.out, agentapi.SnippetProblem{Line: p.Line, Msg: p.Msg})
		}
	}

	return res, nil
}
