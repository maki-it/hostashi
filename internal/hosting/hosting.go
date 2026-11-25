// SPDX-License-Identifier: AGPL-3.0-or-later

// Package hosting is the panel side of sites: it keeps the desired state in
// the database and makes the agent follow it. Every change sends the agent
// the complete web server state built from the database (web.apply), so a
// failed or interrupted change is repaired by the next one.
package hosting

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

// Agent is the part of the agent this package uses. The production one wraps
// an agentrpc.Client; tests use a fake.
type Agent interface {
	SiteCreate(ctx context.Context, req agentapi.SiteCreateRequest) (agentapi.SiteInfo, error)
	SiteDelete(ctx context.Context, req agentapi.SiteDeleteRequest) error
	WebApply(ctx context.Context, req agentapi.WebApplyRequest) (agentapi.WebApplyResponse, error)
	PHPVersions(ctx context.Context) ([]string, error)
	TLSCSR(ctx context.Context, user string, domains []string) (string, error)
	TLSInstall(ctx context.Context, user, chain string) (agentapi.TLSCertInfo, error)
	// Own certificates (ADR-0039).
	TLSCustomCSR(ctx context.Context, user string, domains []string) (string, error)
	TLSUpload(ctx context.Context, req agentapi.TLSUploadRequest) (agentapi.TLSCertInfo, error)
	TLSInfo(ctx context.Context, user string) (agentapi.TLSInfoResponse, error)
	TLSDelete(ctx context.Context, user string) error
	DBCreate(ctx context.Context, site, name string) error
	DBDelete(ctx context.Context, name string) error
	// DBUserCreate creates a user with MAX_USER_CONNECTIONS maxConn, 0:
	// unlimited (ADR-0057).
	DBUserCreate(ctx context.Context, database, user, password string, maxConn uint32) error
	DBUserDelete(ctx context.Context, user string) error
	DBUserPassword(ctx context.Context, user, password string) error
	DBImport(ctx context.Context, database, path string) (agentapi.DBJob, error)
	// DBLoad imports as the database's owner account with the dump's
	// DEFINER clauses stripped (ADR-0030).
	DBLoad(ctx context.Context, database, path string) (agentapi.DBJob, error)
	DBExport(ctx context.Context, database, path string) (agentapi.DBJob, error)
	DBJob(ctx context.Context, id string) (agentapi.DBJob, error)
	// Jobs on external servers (ADR-0041 addendum 1).
	DBRemoteStart(ctx context.Context, req agentapi.DBRemoteJobRequest) (agentapi.DBJob, error)
	DBRemoteJob(ctx context.Context, id string) (agentapi.DBJob, error)
	PageList(ctx context.Context) ([]agentapi.PageVersion, error)
	// PageUpload sends a page in chunks and commits it under digest.
	PageUpload(ctx context.Context, id string, files map[string][]byte, digest string) error
	PageDelete(ctx context.Context, id string) error
	// SnippetCheck checks custom snippets of a site (ADR-0029).
	SnippetCheck(ctx context.Context, req agentapi.SnippetCheckRequest) (agentapi.SnippetCheckResponse, error)
	// PageCachePurge empties the page cache of a site (ADR-0046).
	PageCachePurge(ctx context.Context, user string) error
}

// Issuer turns a certificate signing request into a certificate chain. The
// production one is package acme.
type Issuer interface {
	Obtain(ctx context.Context, csrPEM []byte) ([]byte, error)
}

// RPCAgent calls the real agent.
type RPCAgent struct{ C *agentrpc.Client }

// SiteCreate calls site.create.
func (a RPCAgent) SiteCreate(ctx context.Context, req agentapi.SiteCreateRequest) (agentapi.SiteInfo, error) {
	return agentapi.SiteCreate.Call(ctx, a.C, req)
}

// SiteDelete calls site.delete.
func (a RPCAgent) SiteDelete(ctx context.Context, req agentapi.SiteDeleteRequest) error {
	_, err := agentapi.SiteDelete.Call(ctx, a.C, req)

	return err
}

// WebApply calls web.apply.
func (a RPCAgent) WebApply(ctx context.Context, req agentapi.WebApplyRequest) (agentapi.WebApplyResponse, error) {
	return agentapi.WebApply.Call(ctx, a.C, req)
}

// PHPVersions calls php.versions.
func (a RPCAgent) PHPVersions(ctx context.Context) ([]string, error) {
	res, err := agentapi.PHPVersionsList.Call(ctx, a.C, agentapi.PHPVersionsRequest{})

	return res.Installed, err
}

// TLSCSR calls tls.csr.
func (a RPCAgent) TLSCSR(ctx context.Context, user string, domains []string) (string, error) {
	res, err := agentapi.TLSCSR.Call(ctx, a.C, agentapi.TLSCSRRequest{User: user, Domains: domains})

	return res.CSR, err
}

// TLSInstall calls tls.install.
func (a RPCAgent) TLSInstall(ctx context.Context, user, chain string) (agentapi.TLSCertInfo, error) {
	return agentapi.TLSInstall.Call(ctx, a.C, agentapi.TLSInstallRequest{User: user, Chain: chain})
}

// TLSCustomCSR calls tls.csr for an own certificate.
func (a RPCAgent) TLSCustomCSR(ctx context.Context, user string, domains []string) (string, error) {
	res, err := agentapi.TLSCSR.Call(ctx, a.C, agentapi.TLSCSRRequest{User: user, Domains: domains, Custom: true})

	return res.CSR, err
}

// TLSUpload calls tls.upload.
func (a RPCAgent) TLSUpload(ctx context.Context, req agentapi.TLSUploadRequest) (agentapi.TLSCertInfo, error) {
	return agentapi.TLSUpload.Call(ctx, a.C, req)
}

// TLSInfo calls tls.info.
func (a RPCAgent) TLSInfo(ctx context.Context, user string) (agentapi.TLSInfoResponse, error) {
	return agentapi.TLSInfo.Call(ctx, a.C, agentapi.TLSNameRequest{User: user})
}

// TLSDelete calls tls.delete.
func (a RPCAgent) TLSDelete(ctx context.Context, user string) error {
	_, err := agentapi.TLSDelete.Call(ctx, a.C, agentapi.TLSNameRequest{User: user})

	return err
}

// Errors a caller can act on. Errors of the agent come back as
// *agentrpc.Error with its code.
var (
	ErrInvalid     = errors.New("hosting: invalid input")
	ErrExists      = errors.New("hosting: a site with this name exists")
	ErrDomainTaken = errors.New("hosting: domain belongs to another site")
	// ErrDomainReserved: the domain is the panel's own address (ADR-0042).
	ErrDomainReserved = errors.New("hosting: domain is the panel's address")
	ErrNotFound       = errors.New("hosting: unknown site")
	ErrState          = errors.New("hosting: the site is being created or deleted")
	// ErrSiteType: the change does not fit the site's type (ADR-0054).
	ErrSiteType = store.ErrSiteType
	// ErrNoTLS means the panel has no ACME client configured.
	ErrNoTLS = errors.New("hosting: certificates are not available")
)

// Service manages sites. Changes are serialized: each one ends with the
// complete web state, which must not interleave with another change.
type Service struct {
	st  *store.Store
	ag  Agent
	log *slog.Logger
	now func() time.Time
	mu  sync.Mutex

	issuer     Issuer
	kick       chan struct{}
	cron       CronAgent     // nil: no cron jobs
	limits     LimitsAgent   // nil: no resource limits (ADR-0015)
	ssh        SSHAgent      // nil: no SSH keys (ADR-0032)
	cache      CacheAgent    // nil: no object caches (ADR-0045)
	apps       AppAgent      // nil: no Node.js apps (ADR-0054)
	envBox     *secrets.Box  // nil: apps have no environment variables
	gitBox     *secrets.Box  // nil: no Git deploy (ADR-0062)
	deploys    DeployAgent   // nil: no deploy runs (ADR-0062)
	deployKick chan struct{} // wakes RunDeploys
	scans      ScanAgent     // nil: no malware scans (ADR-0064)
	scanKick   chan struct{} // wakes RunScans
	// rtCursor and rtPending belong to RunScans: where the real-time log
	// was read to (0 after a start: the agent's log is read again, known
	// findings are not reported twice) and the findings waiting for an
	// email.
	rtCursor  int64
	rtPending map[string]*realtimeMail
	// deployAgain holds the sites whose webhook fired while a run was
	// active; RunDeploys starts one more run when it ends. In memory only.
	deployAgainMu sync.Mutex
	deployAgain   map[string]bool
	dbs           DBServers // nil: only the local database server (ADR-0041)

	dbSizer DBSizeAgent // nil: database sizes are not measured (ADR-0015)
	dbSizes dbSizeCache

	resolver     Resolver       // nil: net.DefaultResolver
	hostKeyScan  HostKeyScanner // nil: backup.FetchHostKeyAt
	remoteJobsMu sync.Mutex     // serializes dropping the users of remote jobs

	// defaultPool names the pool of a site whose request names none
	// (ADR-0016 addendum 2); nil or an empty answer: agentapi.DefaultPool.
	defaultPool func(ctx context.Context) (string, error)

	dbTool     DBToolAgent // nil: no web database tool (ADR-0038)
	dbToolPort int

	// The tool's external servers (ADR-0041 addendum 1): the list last
	// sent to the agent, under remoteToolsMu, and how the end of a
	// session is scheduled (nil: time.AfterFunc).
	remoteToolsMu sync.Mutex
	toolServers   map[int64]agentapi.DBToolServer
	afterFunc     func(d time.Duration, f func())

	dbManifestDir string        // extdb list for the backup; empty: none
	pgClient      PGClientTools // nil: no PostgreSQL client installs (tests)
	manifestMu    sync.Mutex    // serializes the manifest writes

	panelDomain string // ADR-0023; empty: nginx serves no panel address
	panelServed string // panel domain of the last accepted web state; under mu

	onApply func(ok bool, generation int) // nil: nobody listens
	// onDeployFailed: see ObserveFailedDeploys; nil: nobody listens.
	onDeployFailed func(ctx context.Context, site string, d store.SiteDeploy)
	// onMalware: see ObserveMalware; nil: nobody listens.
	onMalware func(ctx context.Context, site string, run store.ScanRun, found []store.ScanFinding)

	// localAddrs are the server's own addresses, which no trusted proxy
	// range may hold (ADR-0059); nil reads the interfaces.
	localAddrs func() ([]netip.Addr, error)

	// purged holds the last page cache purge per site (ADR-0046).
	purgeMu sync.Mutex
	purged  map[string]time.Time
}

// New builds a Service.
func New(st *store.Store, ag Agent, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Service{st: st, ag: ag, log: log, now: time.Now, kick: make(chan struct{}, 1)}
}

// ResolveDefaultPool sets how Create finds the default pool (ADR-0016
// addendum 2), normally by asking the agent.
func (s *Service) ResolveDefaultPool(f func(ctx context.Context) (string, error)) { s.defaultPool = f }

// EnableTLS sets the ACME client. Without one, sites cannot get HTTPS.
func (s *Service) EnableTLS(i Issuer) { s.issuer = i }

// CreateRequest describes a new site.
type CreateRequest struct {
	Name string
	// Title is the display name (ADR-0072), empty for none.
	Title   string
	Pool    string // empty: the default pool
	PHP     string // PHP version, empty: no PHP
	Domains []string
	// TLS asks for a certificate right away (see SetTLS); HSTS is sent once
	// the site is on HTTPS.
	TLS  bool
	HSTS bool
	// PHPPrograms lets the site's PHP start programs (ADR-0033). Off for
	// new sites; an import turns it on, so the old site keeps working.
	PHPPrograms bool
	// Usage is what the site's files take on the old server, for an
	// import: the site's disk limits are raised to fit it (ImportLimits).
	Usage *Usage
	// Headers are the site's first security headers (ADR-0056); nil sends
	// none. The panel and the API pass agentapi.DefaultWebHeaders unless
	// told otherwise, an import passes nil.
	Headers *agentapi.WebHeaders
	// Redirect makes the site a redirect without a Linux account
	// (ADR-0054); Status 0 is 301. PHP, programs and Usage must be unset.
	Redirect *store.SiteRedirect
	// Proxy makes the site a reverse proxy without a Linux account
	// (ADR-0054), with the same rules as Redirect.
	Proxy *store.SiteProxy
	// App makes the site a Node.js site (ADR-0054): an account and files,
	// no PHP, its app on a loopback port the store hands out. Its Node.js
	// line must be installed. Port and Stopped are ignored. An App without
	// Node makes an app site, which runs a program of its own instead
	// (addendum 2).
	App *store.SiteApp
}

// checkRedirect normalizes and checks the redirect of a site with these
// domains.
func checkRedirect(r store.SiteRedirect, domains []string) (store.SiteRedirect, error) {
	if r.Status == 0 {
		r.Status = agentapi.RedirectStatuses[0]
	}

	if err := (&agentapi.WebRedirect{Target: r.Target, Status: r.Status, KeepPath: r.KeepPath}).Validate(domains); err != nil {
		return store.SiteRedirect{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return r, nil
}

// checkPHP accepts a version from agentapi.PHPVersions or "" (no PHP).
// Whether it is installed is the agent's answer.
func checkPHP(v string) error {
	if v == "" {
		return nil
	}

	if err := agentapi.ValidatePHPVersion(v); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

// NormalizeDomains lower-cases and trims the list and checks it. The order
// is kept: the first domain is the primary one.
func NormalizeDomains(in []string) ([]string, error) {
	if len(in) == 0 || len(in) > agentapi.MaxDomainsPerSite {
		return nil, fmt.Errorf("%w: between 1 and %d domains required", ErrInvalid, agentapi.MaxDomainsPerSite)
	}

	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))

	for _, d := range in {
		d = strings.ToLower(strings.TrimSpace(d))
		if err := agentapi.ValidateDomain(d); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		if seen[d] {
			return nil, fmt.Errorf("%w: domain %s is listed twice", ErrInvalid, d)
		}

		seen[d] = true
		out = append(out, d)
	}

	return out, nil
}

// List returns all sites.
func (s *Service) List(ctx context.Context) ([]store.Site, error) {
	return s.st.Sites(ctx)
}

// Get returns one site.
func (s *Service) Get(ctx context.Context, name string) (store.Site, error) {
	site, err := s.st.SiteByName(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return store.Site{}, ErrNotFound
	}

	return site, err
}

// Create adds the database row, the site account (agent) and the virtual
// host (agent). If the virtual host cannot go live, the account and the row
// are removed again.
func (s *Service) Create(ctx context.Context, actor int64, req CreateRequest) (store.Site, error) {
	if req.Pool == "" && s.defaultPool != nil {
		name, err := s.defaultPool(ctx)
		if err != nil {
			return store.Site{}, fmt.Errorf("find the default storage pool: %w", err)
		}

		req.Pool = name
	}

	if req.Pool == "" {
		req.Pool = agentapi.DefaultPool
	}

	if err := (agentapi.SiteCreateRequest{User: req.Name, Pool: req.Pool}).Validate(); err != nil {
		return store.Site{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	domains, err := NormalizeDomains(req.Domains)
	if err != nil {
		return store.Site{}, err
	}

	if err := checkPHP(req.PHP); err != nil {
		return store.Site{}, err
	}

	if req.TLS && s.issuer == nil {
		return store.Site{}, ErrNoTLS
	}

	if req.PHPPrograms && req.PHP == "" {
		return store.Site{}, fmt.Errorf("%w: programs need a PHP version", ErrInvalid)
	}

	if req.Redirect != nil || req.Proxy != nil {
		if req.PHP != "" || req.Usage != nil || req.PHPPrograms || req.Redirect != nil && req.Proxy != nil {
			return store.Site{}, fmt.Errorf("%w: a redirect or reverse proxy site has no PHP and no files", ErrInvalid)
		}
	}

	if req.App != nil {
		if req.PHP != "" || req.PHPPrograms || req.Redirect != nil || req.Proxy != nil {
			return store.Site{}, fmt.Errorf("%w: a Node.js, app or Python site has no PHP and is no redirect or reverse proxy", ErrInvalid)
		}

		typ := appTypeOf(*req.App)

		a, err := checkApp(*req.App, typ)
		if err != nil {
			return store.Site{}, err
		}

		if err := s.checkRuntime(ctx, typ, a); err != nil {
			return store.Site{}, err
		}

		a.Port, a.Stopped = 0, false
		req.App = &a
	}

	if req.Proxy != nil {
		if err := s.checkProxy(ctx, *req.Proxy, domains); err != nil {
			return store.Site{}, err
		}
	}

	if req.Redirect != nil {
		r, err := checkRedirect(*req.Redirect, domains)
		if err != nil {
			return store.Site{}, err
		}

		req.Redirect = &r
	}

	if err := s.checkReserved(domains); err != nil {
		return store.Site{}, err
	}

	if req.Headers != nil {
		if err := req.Headers.Validate(); err != nil {
			return store.Site{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}

	title, err := NormalizeTitle(req.Title)
	if err != nil {
		return store.Site{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// A parked domain becomes part of the site; it is parked again if the
	// site does not come up (ADR-0048).
	parked, err := s.parkedAmong(ctx, domains)
	if err != nil {
		return store.Site{}, err
	}

	var created store.Site

	switch {
	case req.Redirect != nil:
		created, err = s.st.CreateRedirectSite(ctx, req.Name, req.Pool, *req.Redirect, domains, s.now())
	case req.Proxy != nil:
		created, err = s.st.CreateProxySite(ctx, req.Name, req.Pool, *req.Proxy, domains, s.now())
	case req.App != nil && appTypeOf(*req.App) == store.SiteTypePython:
		created, err = s.st.CreatePythonSite(ctx, req.Name, req.Pool, *req.App, domains, s.now())
	case req.App != nil && appTypeOf(*req.App) == store.SiteTypeApp:
		created, err = s.st.CreateAppSite(ctx, req.Name, req.Pool, *req.App, domains, s.now())
	case req.App != nil:
		created, err = s.st.CreateNodeSite(ctx, req.Name, req.Pool, *req.App, domains, s.now())
	default:
		created, err = s.st.CreateSite(ctx, req.Name, req.Pool, req.PHP, domains, s.now())
	}

	switch {
	case errors.Is(err, store.ErrSiteExists):
		return store.Site{}, ErrExists
	case errors.Is(err, store.ErrDomainTaken):
		return store.Site{}, fmt.Errorf("%w: %w", ErrDomainTaken, err)
	case err != nil:
		return store.Site{}, err
	}

	// Cleanup must run even when the request was canceled meanwhile.
	undo := context.WithoutCancel(ctx)

	if title != "" {
		if _, err := s.st.SetSiteTitle(ctx, req.Name, title, s.now()); err != nil {
			if derr := s.st.DeleteSite(undo, req.Name); derr != nil {
				s.log.ErrorContext(ctx, "remove site row after failed create", "site", req.Name, "err", derr)
			}

			s.parkAgain(undo, actor, req.Name, parked)

			return store.Site{}, err
		}

		created.Title = title
	}

	if err := s.storeTLSChoice(ctx, req, created.ID); err != nil {
		if derr := s.st.DeleteSite(undo, req.Name); derr != nil {
			s.log.ErrorContext(ctx, "remove site row after failed create", "site", req.Name, "err", derr)
		}

		s.parkAgain(undo, actor, req.Name, parked)

		return store.Site{}, err
	}

	// Redirect and reverse proxy sites get no account (ADR-0054).
	if created.Account {
		_, err = s.ag.SiteCreate(ctx, agentapi.SiteCreateRequest{User: req.Name, Pool: req.Pool})
	}

	if err != nil {
		if derr := s.st.DeleteSite(undo, req.Name); derr != nil {
			s.log.ErrorContext(ctx, "remove site row after failed create", "site", req.Name, "err", derr)
		}

		s.parkAgain(undo, actor, req.Name, parked)

		return store.Site{}, err
	}

	if err := s.st.SetSiteState(ctx, req.Name, store.SiteActive, s.now()); err != nil {
		return store.Site{}, err
	}

	// Stored before the web state, so the PHP pool starts with the
	// preset's workers. A site without an account has no limits.
	var (
		limits    store.Limits
		caches    store.PresetCaches
		hasLimits bool
	)

	if created.Account {
		limits, caches, hasLimits = s.storeDefaultLimits(ctx, created, req.Usage)
	}

	// The app is set up before nginx passes requests to its port.
	if created.App != nil {
		created.State = store.SiteActive
		if _, err := s.applyApp(ctx, created); err != nil {
			s.removeAfterFailure(undo, req.Name)
			s.parkAgain(undo, actor, req.Name, parked)

			return store.Site{}, err
		}
	}

	if err := s.applyWeb(ctx); err != nil {
		s.removeAfterFailure(undo, req.Name)
		s.parkAgain(undo, actor, req.Name, parked)

		return store.Site{}, err
	}

	detail := map[string]string{
		"type": created.Type, "pool": req.Pool, "php": req.PHP, "domains": strings.Join(domains, " "),
		"tls": strconv.FormatBool(req.TLS), "hsts": strconv.FormatBool(req.HSTS), "phpPrograms": strconv.FormatBool(req.PHPPrograms),
	}
	if r := req.Redirect; r != nil {
		detail["redirectTarget"], detail["redirectStatus"], detail["redirectKeepPath"] = r.Target, strconv.Itoa(r.Status), strconv.FormatBool(r.KeepPath)
	}

	if p := req.Proxy; p != nil {
		detail["proxyTarget"], detail["proxyTargetHost"], detail["proxyInsecure"] = p.Target, strconv.FormatBool(p.TargetHost), strconv.FormatBool(p.Insecure)
	}

	if a := created.App; a != nil {
		maps.Copy(detail, appAudit(*a))
	}

	s.audit(ctx, actor, "site.create", req.Name, detail)

	if req.TLS {
		s.Kick()
	}

	site, err := s.st.SiteByName(ctx, req.Name)
	if err != nil {
		return store.Site{}, err
	}

	if hasLimits {
		if err := s.applyLimits(ctx, site, limits); err != nil {
			s.log.WarnContext(ctx, "apply limits of a new site", "site", site.Name, "err", err)
		}

		// After the limits, so the cache starts inside the site's slice.
		s.presetCaches(ctx, actor, site, caches)
	}

	return site, nil
}

// storeTLSChoice keeps the HTTPS settings, the PHP programs switch and the
// security headers of a new site, before its first web state. HTTPS
// changes nothing there before the first certificate.
func (s *Service) storeTLSChoice(ctx context.Context, req CreateRequest, siteID int64) error {
	if req.TLS {
		if _, err := s.st.SetSiteTLS(ctx, req.Name, true, s.now()); err != nil {
			return err
		}
	}

	if req.HSTS {
		if _, err := s.st.SetSiteHSTS(ctx, req.Name, true, s.now()); err != nil {
			return err
		}
	}

	if req.PHPPrograms {
		if _, err := s.st.SetSitePHPPrograms(ctx, req.Name, true, s.now()); err != nil {
			return err
		}
	}

	if h := req.Headers; h != nil && !h.IsZero() {
		if _, err := s.st.SetSiteHeaders(ctx, store.SiteHeaders{SiteID: siteID, Headers: *h}, s.now()); err != nil {
			return err
		}
	}

	return nil
}

// parkedAmong returns the parked domains among domains.
func (s *Service) parkedAmong(ctx context.Context, domains []string) ([]store.Parked, error) {
	var out []store.Parked

	for _, d := range domains {
		p, err := s.st.ParkedDomain(ctx, d)

		switch {
		case err == nil:
			out = append(out, p)
		case !errors.Is(err, store.ErrNotFound):
			return nil, err
		}
	}

	return out, nil
}

// parkAgain parks domains again that a failed site change had taken over,
// with their ids, pages and certificates (ADR-0048). The agent still serves
// them from the last accepted web state, so no new web state is needed.
func (s *Service) parkAgain(ctx context.Context, actor int64, site string, parked []store.Parked) {
	if len(parked) == 0 {
		return
	}

	domains := make([]string, len(parked))
	for i, p := range parked {
		domains[i] = p.Domain
	}

	if err := s.st.ReinsertParked(ctx, parked); err != nil {
		s.log.ErrorContext(ctx, "park domains again after a failed site change", "site", site, "domains", domains, "err", err)

		return
	}

	s.audit(ctx, actor, "parked.restore", site, map[string]string{"domains": strings.Join(domains, " ")})
}

// removeAfterFailure takes a just created site away again. If the account
// cannot be removed, the row stays in state deleting so a delete can finish
// the job.
func (s *Service) removeAfterFailure(ctx context.Context, name string) {
	if err := s.st.SetSiteState(ctx, name, store.SiteDeleting, s.now()); err != nil {
		s.log.ErrorContext(ctx, "mark site deleting after failed create", "site", name, "err", err)

		return
	}

	if err := s.ag.SiteDelete(ctx, agentapi.SiteDeleteRequest{User: name}); err != nil && !isCode(err, agentrpc.CodeNotFound) {
		s.log.ErrorContext(ctx, "remove site account after failed create", "site", name, "err", err)

		return
	}

	if err := s.st.DeleteSite(ctx, name); err != nil {
		s.log.ErrorContext(ctx, "remove site row after failed create", "site", name, "err", err)
	}
}

// SetPHP sets the PHP version of an active site; "" turns PHP off. If the
// change cannot go live, the previous version is stored again.
func (s *Service) SetPHP(ctx context.Context, actor int64, name, version string) (store.Site, error) {
	if err := checkPHP(version); err != nil {
		return store.Site{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, ErrState
	}

	old, err := s.st.SetSitePHP(ctx, name, version, s.now())
	if err != nil {
		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSitePHP(context.WithoutCancel(ctx), name, old, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore php version after failed change", "site", name, "err", rerr)
		}

		return store.Site{}, err
	}

	s.auditChange(ctx, actor, "site.php", name, map[string]string{"php": version, "previous": old},
		map[string]string{"php": old}, map[string]string{"php": version})

	site.PHP = version
	if site.Type == store.SiteTypeStatic || site.Type == store.SiteTypePHP {
		site.Type = store.SiteTypeStatic
		if version != "" {
			site.Type = store.SiteTypePHP
		}
	}

	return site, nil
}

// PHPVersions returns the PHP versions installed on the server.
func (s *Service) PHPVersions(ctx context.Context) ([]string, error) {
	return s.ag.PHPVersions(ctx)
}

// Delete takes the site out of the web server, removes account and files
// (agent) and then the row. A delete that stops halfway leaves the site in
// state deleting and can be repeated.
func (s *Service) Delete(ctx context.Context, actor int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return err
	}

	if err := s.st.SetSiteState(ctx, name, store.SiteDeleting, s.now()); err != nil {
		return err
	}

	if err := s.applyWeb(ctx); err != nil {
		if site.State == store.SiteActive {
			if rerr := s.st.SetSiteState(context.WithoutCancel(ctx), name, store.SiteActive, s.now()); rerr != nil {
				s.log.ErrorContext(ctx, "restore site state after failed delete", "site", name, "err", rerr)
			}
		}

		return err
	}

	if err := s.removeOfflineSite(ctx, actor, name); err != nil {
		return err
	}

	s.audit(ctx, actor, "site.delete", name, nil)

	return nil
}

// removeOfflineSite removes the databases, the account with its files and
// the row of a site that the web server no longer serves.
func (s *Service) removeOfflineSite(ctx context.Context, actor int64, name string) error {
	// Its databases go before the account.
	if err := s.deleteSiteDatabases(ctx, actor, name); err != nil {
		return err
	}

	if err := s.ag.SiteDelete(ctx, agentapi.SiteDeleteRequest{User: name}); err != nil && !isCode(err, agentrpc.CodeNotFound) {
		return err
	}

	return s.st.DeleteSite(ctx, name)
}

// Sync sends the current web state to the agent again, for example after a
// change that failed because the agent was unavailable.
func (s *Service) Sync(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.applyWeb(ctx); err != nil {
		return err
	}

	s.syncApps(ctx)

	return nil
}

// SyncAtStart sends the stored web state once when the panel starts, so
// that what a new release renders differently (nginx templates, app units)
// reaches the server after an upgrade instead of waiting for the next
// change. The agent skips the reload when nothing changed. While the agent
// is unavailable it tries again with a growing pause, up to syncAtStartTries
// times.
func (s *Service) SyncAtStart(ctx context.Context) {
	pause := syncAtStartPause

	for try := 1; ; try++ {
		err := s.Sync(ctx)
		if err == nil || ctx.Err() != nil {
			return
		}

		if try == syncAtStartTries {
			s.log.ErrorContext(ctx, "web state not sent at start; it goes out with the next change", "err", err)

			return
		}

		s.log.WarnContext(ctx, "web state not sent at start, trying again", "err", err, "in", pause)

		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}

		pause = min(2*pause, time.Minute)
	}
}

// Tries and first pause of SyncAtStart; variables for the tests.
var (
	syncAtStartTries = 10
	syncAtStartPause = 2 * time.Second
)

// applyWeb sends the complete web state: every active site with its domains,
// PHP version and HTTPS when its certificate covers all its domains. A
// disabled site is sent without PHP, so its pool stops.
func (s *Service) applyWeb(ctx context.Context) error {
	sites, err := s.st.Sites(ctx)
	if err != nil {
		return err
	}

	req := agentapi.WebApplyRequest{Sites: []agentapi.WebSite{}}
	live := []string{}

	parked, err := s.st.ParkedDomains(ctx)
	if err != nil {
		return err
	}

	onAgent, err := s.agentPages(ctx, sites, len(parked))
	if err != nil {
		return err
	}

	snippets, err := s.st.AllSiteSnippets(ctx)
	if err != nil {
		return err
	}

	phpSettings, err := s.st.AllSitePHPSettings(ctx)
	if err != nil {
		return err
	}

	headers, err := s.st.AllSiteHeaders(ctx)
	if err != nil {
		return err
	}

	realips, err := s.st.AllSiteRealIP(ctx)
	if err != nil {
		return err
	}

	access, err := s.st.AllSiteAccess(ctx)
	if err != nil {
		return err
	}

	authUsers, err := s.st.AllAuthUsers(ctx)
	if err != nil {
		return err
	}

	workers, err := s.st.PHPWorkers(ctx)
	if err != nil {
		return err
	}

	caches, err := s.st.AllPageCaches(ctx)
	if err != nil {
		return err
	}

	profiles, err := s.st.AllSiteProfiles(ctx)
	if err != nil {
		return err
	}

	for _, site := range sites {
		if site.State == store.SiteActive && len(site.Domains) > 0 {
			tls := site.TLS && s.certCovers(site, 0)
			if tls {
				live = append(live, site.Name)
			}

			php := site.PHP
			if site.Disabled {
				php = ""
			}

			ws := agentapi.WebSite{
				User: site.Name, Domains: site.AllDomains(), PHP: php, TLS: tls, HSTS: site.HSTS, Disabled: site.Disabled,
				Headers: webHeaders(headers, site), RealIP: webRealIP(realips, site),
				Access: webAccess(access, authUsers, site),
			}

			// A redirect site sends every name to its target anyway; it
			// keeps the setting for a later type change (ADR-0065).
			if site.Type != store.SiteTypeRedirect {
				ws.WWWRedirect = site.WWWRedirect
				ws.RedirectDomains = site.RedirectDomains
			}

			// Redirect and reverse proxy sites send only their target: no
			// files, PHP, caches or snippets (ADR-0054).
			if noFiles(site.Type) {
				ws.PHP = ""
				if r := site.Redirect; r != nil && site.Type == store.SiteTypeRedirect {
					ws.Redirect = &agentapi.WebRedirect{Target: r.Target, Status: r.Status, KeepPath: r.KeepPath}
				}

				if site.Type == store.SiteTypeProxy {
					ws.Proxy, ws.ProxyAddrs = s.proxyWeb(ctx, site)
				}

				if err := s.addLifecycleWeb(ctx, &ws, site, onAgent); err != nil {
					return err
				}

				req.Sites = append(req.Sites, ws)

				continue
			}

			// A Node.js or app site passes its requests to its app; it
			// keeps its root and snippets, nothing of PHP or a static site.
			if hasApp(site) {
				ws.PHP = ""
				ws.App = &agentapi.WebApp{Port: site.App.Port}
				ws.Snippets = webSnippets(snippets, site)

				if err := s.addLifecycleWeb(ctx, &ws, site, onAgent); err != nil {
					return err
				}

				req.Sites = append(req.Sites, ws)

				continue
			}

			// SPA is kept while the site runs PHP and only sent for a
			// static site; the asset cache serves static and PHP sites
			// (ADR-0054 addenda 1 and 4).
			ws.WebDir = site.WebDir
			if site.Type == store.SiteTypeStatic {
				ws.SPA = site.SPA
			}

			ws.Hardening = webHardening(profiles, site, php)

			if site.AssetCache && (site.Type == store.SiteTypeStatic || site.Type == store.SiteTypePHP) {
				ws.AssetCache, ws.AssetCacheSeconds, ws.AssetCacheExts = true, site.AssetCacheSeconds, site.AssetCacheExts
			}

			if php != "" {
				ws.PHPWorkers = phpWorkers(workers, site.ID)
				ws.PHPPrograms = site.PHPPrograms
				ws.PHPSettings = webPHPSettings(phpSettings, site)
			}

			// Sent in maintenance as well, so the agent keeps the cache's
			// directory; it purges it when the site is served again.
			ws.PageCache = webPageCache(caches, site, php)

			// The agent leaves them out while the site is disabled or in
			// maintenance; they are sent anyway so the state stays complete.
			ws.Snippets = webSnippets(snippets, site)

			if err := s.addLifecycleWeb(ctx, &ws, site, onAgent); err != nil {
				return err
			}

			req.Sites = append(req.Sites, ws)
		}
	}

	liveParked := []int64{}

	for _, p := range parked {
		id := ParkedPageID(p)

		digest, err := s.pageOnAgent(ctx, id, p.Page, onAgent)
		if err != nil {
			return err
		}

		tls := p.TLS && s.parkCovers(p, 0)
		if tls {
			liveParked = append(liveParked, p.ID)
		}

		req.Parked = append(req.Parked, agentapi.WebParked{Name: agentapi.ParkName(p.ID), Domain: p.Domain, Page: id, Digest: digest, TLS: tls})
	}

	if req.Panel, err = s.panelWeb(ctx); err != nil {
		return err
	}

	res, err := s.ag.WebApply(ctx, req)
	if s.onApply != nil {
		s.onApply(err == nil, res.Generation)
	}

	if err != nil {
		return err
	}

	s.panelApplied(context.WithoutCancel(ctx), req.Panel)

	// Only now is the site served over HTTPS (or no longer). A failure to
	// record it leaves the API one change behind, nothing more.
	if err := s.st.SetTLSLive(context.WithoutCancel(ctx), live); err != nil {
		s.log.ErrorContext(ctx, "record https state", "err", err)
	}

	if err := s.st.SetParkedTLSLive(context.WithoutCancel(ctx), liveParked); err != nil {
		s.log.ErrorContext(ctx, "record https state of parked domains", "err", err)
	}

	if res.Changed {
		s.log.InfoContext(ctx, "web server state applied", "generation", res.Generation, "sites", len(req.Sites))
	}

	return nil
}

// addLifecycleWeb adds the maintenance page or the page of a disabled site.
func (s *Service) addLifecycleWeb(ctx context.Context, ws *agentapi.WebSite, site store.Site, onAgent map[string]bool) error {
	var err error

	if site.Maintenance && !site.Disabled {
		if ws.Maintenance, err = s.maintenanceWeb(ctx, site, onAgent); err != nil {
			return err
		}
	}

	if site.Disabled {
		if ws.DisabledPage, err = s.disabledWeb(ctx, site, onAgent); err != nil {
			return err
		}
	}

	return nil
}

func (s *Service) audit(ctx context.Context, actor int64, action, target string, detail map[string]string) {
	s.auditChange(ctx, actor, action, target, detail, nil, nil)
}

// auditChange writes an entry for an update with the fields that differ
// between before and after (store.DiffOf). Neither may hold secrets.
func (s *Service) auditChange(ctx context.Context, actor int64, action, target string, detail, before, after map[string]string) {
	// Actor 0 is no user, such as a webhook.
	var by *int64
	if actor != 0 {
		by = &actor
	}

	if err := s.st.AuditChange(ctx, by, action, target, detail, store.DiffOf(before, after)); err != nil {
		s.log.ErrorContext(ctx, "audit log", "action", action, "err", err)
	}
}

func isCode(err error, code string) bool {
	var e *agentrpc.Error

	return errors.As(err, &e) && e.Code == code
}
