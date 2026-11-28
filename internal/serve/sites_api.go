// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

type siteJSON struct {
	Name string `json:"name"`
	// Title is the display name (ADR-0072), "" for none; Name stays the
	// system name (Linux user, home folder, API path).
	Title string `json:"title"`
	Pool  string `json:"pool"`
	State string `json:"state"`
	Type  string `json:"type"` // static, php, node, app, proxy, redirect (ADR-0054), python (ADR-0061)
	// Web is the document root folder below htdocs and the static-site
	// options (ADR-0054 addendum 1).
	Web webJSON `json:"web"`
	// Account: the site has a Linux account with files; a redirect site
	// has none (ADR-0054).
	Account bool `json:"account"`
	// Redirect is the target of a redirect site, null otherwise.
	Redirect *redirectJSON `json:"redirect"`
	// Proxy is the target of a reverse proxy site, null otherwise.
	Proxy *proxyJSON `json:"proxy"`
	// Node is the app of a Node.js site, null otherwise.
	Node *nodeAppJSON `json:"node"`
	// App is the program of an app site (ADR-0054 addendum 2), null
	// otherwise.
	App *programAppJSON `json:"app"`
	// Python is the app of a Python site (ADR-0061), null otherwise.
	Python   *pythonAppJSON `json:"python"`
	PHP      string         `json:"php"`      // PHP version, "" for no PHP
	Disabled bool           `json:"disabled"` // answers 503, PHP stopped
	// DisabledPage is the maintenance page shown while disabled (ADR-0047).
	DisabledPage string `json:"disabledPage"`
	// Maintenance: other addresses get the page with 503 (ADR-0027).
	Maintenance maintenanceJSON `json:"maintenance"`
	Domains     []string        `json:"domains"` // the first one is the primary domain
	// RedirectDomains stay on the certificate and answer with 301 to the
	// primary domain (ADR-0068).
	RedirectDomains []string `json:"redirectDomains"`
	// PendingDomains is a domain change waiting for its certificate, null
	// for none (ADR-0068).
	PendingDomains *pendingDomainsJSON `json:"pendingDomains"`
	// ServedElsewhere are names below the site's wildcard domains that
	// other sites, parked domains or the panel serve instead (ADR-0075).
	// Only GET /api/v1/sites/{name} fills it.
	ServedElsewhere []string `json:"servedElsewhere,omitempty"`
	// WildcardsWithoutDNS are wildcard domains of the site that no DNS
	// account serves, so Let's Encrypt cannot issue them (ADR-0075). Only
	// GET /api/v1/sites/{name} fills it.
	WildcardsWithoutDNS []string `json:"wildcardsWithoutDns,omitempty"`
	TLS                 tlsJSON  `json:"tls"`
	// WWW is the www redirect between d and www.d (ADR-0065).
	WWW wwwJSON `json:"www"`
	// PHPPrograms: the site's PHP may start programs (ADR-0033).
	PHPPrograms bool      `json:"phpPrograms"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type redirectJSON struct {
	Target   string `json:"target"`
	Status   int    `json:"status"`
	KeepPath bool   `json:"keepPath"`
}

func toRedirectJSON(s store.Site) *redirectJSON {
	if s.Type != store.SiteTypeRedirect || s.Redirect == nil {
		return nil
	}

	return &redirectJSON{Target: s.Redirect.Target, Status: s.Redirect.Status, KeepPath: s.Redirect.KeepPath}
}

type proxyJSON struct {
	Target     string `json:"target"`
	TargetHost bool   `json:"targetHost"`
	Insecure   bool   `json:"insecure"`
}

func toProxyJSON(s store.Site) *proxyJSON {
	if s.Type != store.SiteTypeProxy || s.Proxy == nil {
		return nil
	}

	return &proxyJSON{Target: s.Proxy.Target, TargetHost: s.Proxy.TargetHost, Insecure: s.Proxy.Insecure}
}

type webJSON struct {
	Dir        string `json:"dir"`
	SPA        bool   `json:"spa"`
	AssetCache bool   `json:"assetCache"`
	// AssetCacheSeconds and AssetCacheExts are the effective values: the
	// defaults when the site keeps them.
	AssetCacheSeconds int      `json:"assetCacheSeconds"`
	AssetCacheExts    []string `json:"assetCacheExts"`
}

func toWebJSON(s store.Site) webJSON {
	w := webJSON{Dir: s.WebDir, SPA: s.SPA, AssetCache: s.AssetCache, AssetCacheSeconds: s.AssetCacheSeconds, AssetCacheExts: s.AssetCacheExts}
	if w.AssetCacheSeconds == 0 {
		w.AssetCacheSeconds = agentapi.DefaultAssetCacheSeconds
	}

	if len(w.AssetCacheExts) == 0 {
		w.AssetCacheExts = agentapi.DefaultAssetCacheExts
	}

	return w
}

// tlsJSON is the HTTPS state of a site: wanted (enabled), served (active),
// HSTS, the live certificate, and the reason of the last failed attempt.
type tlsJSON struct {
	Enabled  bool       `json:"enabled"`
	Active   bool       `json:"active"`
	HSTS     bool       `json:"hsts"`
	Domains  []string   `json:"domains"`
	NotAfter *time.Time `json:"notAfter"`
	Error    string     `json:"error,omitempty"`
	FailedAt *time.Time `json:"failedAt,omitempty"`
	// Source is where the certificate comes from ("letsencrypt" or
	// "custom"); CertSource where the live one came from ("" for none).
	Source     string `json:"source"`
	CertSource string `json:"certSource"`
	// Trusted is false for an own certificate without a public chain.
	Trusted bool `json:"trusted"`
	// RenewAt is the last "renew now"; RenewPending while it runs.
	RenewAt      *time.Time `json:"renewAt,omitempty"`
	RenewPending bool       `json:"renewPending"`
	// Challenge is how Let's Encrypt checks the domains: "http" or
	// "dns" (ADR-0076).
	Challenge string `json:"challenge"`
}

// panelTLSJSON is the HTTPS state of the panel's own address: whether
// nginx serves one (enabled), whether HTTPS is live (active), the live
// certificate and the reason of the last failed attempt.
type panelTLSJSON struct {
	Enabled  bool       `json:"enabled"`
	Domain   string     `json:"domain"`
	Active   bool       `json:"active"`
	Domains  []string   `json:"domains"`
	NotAfter *time.Time `json:"notAfter"`
	Error    string     `json:"error,omitempty"`
	FailedAt *time.Time `json:"failedAt,omitempty"`
	// As for sites (ADR-0039).
	Source       string     `json:"source"`
	CertSource   string     `json:"certSource"`
	Trusted      bool       `json:"trusted"`
	RenewAt      *time.Time `json:"renewAt,omitempty"`
	RenewPending bool       `json:"renewPending"`
	Challenge    string     `json:"challenge"`
}

func toPanelTLSJSON(p hosting.PanelTLS) panelTLSJSON {
	return panelTLSJSON{
		Enabled: p.Domain != "", Domain: p.Domain, Active: p.Live, Domains: p.Domains,
		NotAfter: timeOrNil(p.NotAfter), Error: p.Error, FailedAt: timeOrNil(p.FailedAt),
		Source: p.WantedSource(), CertSource: p.LiveSource(), Trusted: !p.Untrusted,
		RenewAt: timeOrNil(p.RenewAt), RenewPending: p.RenewForced, Challenge: p.WantedChallenge(),
	}
}

func timeOrNil(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}

	return &t
}

type siteListJSON struct {
	Sites []siteJSON `json:"sites"`
	// Marks are shown beside a site in the list, by site name; a site
	// with none is left out.
	Marks map[string]siteMarksJSON `json:"marks"`
}

type siteMarksJSON struct {
	// OpenFindings counts the files the virus scan flagged that nobody
	// acted on yet (ADR-0064).
	OpenFindings int `json:"openFindings"`
	// CloneOf is the site this one was copied from (ADR-0070), "" for
	// none or when that site is gone.
	CloneOf string `json:"cloneOf"`
	// Staging: the copy is a staging site of CloneOf.
	Staging bool `json:"staging"`
}

// enabledRequest switches one setting of a site on or off.
type enabledRequest struct {
	Enabled *bool `json:"enabled"`
}

// phpVersionsJSON are the versions a site can use (ADR-0019, ADR-0036,
// ADR-0037).
type phpVersionsJSON struct {
	Installed     []string          `json:"installed"`
	Supported     []string          `json:"supported"`
	Legacy        []string          `json:"legacy"`
	SecurityUntil map[string]string `json:"securityUntil"`
	ActiveUntil   map[string]string `json:"activeUntil"`
	Distro        string            `json:"distro"`
}

type siteCreateRequest struct {
	Name string `json:"name"`
	// Title is the display name (ADR-0072), optional.
	Title   string   `json:"title"`
	Pool    string   `json:"pool"`
	PHP     string   `json:"php"`
	Domains []string `json:"domains"`
	TLS     bool     `json:"tls"`  // ask for a certificate right away
	HSTS    bool     `json:"hsts"` // sent once the site is on HTTPS
	// Headers are the first security headers (ADR-0056); left out,
	// the site gets agentapi.DefaultWebHeaders, {} sends none.
	Headers *agentapi.WebHeaders `json:"headers"`
	// Type is static or php (from php when left out), redirect
	// with redirect set, proxy with proxy set, node with node set,
	// app with app set (ADR-0054) or python with python set (ADR-0061).
	Type     string          `json:"type"`
	Redirect *redirectJSON   `json:"redirect"`
	Proxy    *proxyJSON      `json:"proxy"`
	Node     *nodeAppJSON    `json:"node"`
	App      *programAppJSON `json:"app"`
	Python   *pythonAppJSON  `json:"python"`
	// Profile is the application profile of a static or PHP site
	// (ADR-0069, GET /api/v1/profiles); left out, the default of the type.
	Profile string `json:"profile"`
}

type siteDomainsRequest struct {
	// Domains are the served domains, the primary one first.
	Domains []string `json:"domains"`
	// RedirectDomains answer with 301 to the primary domain (ADR-0068);
	// left out, the current ones stay except those now served.
	RedirectDomains *[]string `json:"redirectDomains,omitempty"`
	// WaitForCertificate (default true): on a site live on HTTPS with
	// Let's Encrypt, new names wait as pending until their certificate is
	// there (202). false changes at once; the site is served over HTTP
	// until the new certificate is there.
	WaitForCertificate *bool `json:"waitForCertificate,omitempty"`
}

// pendingDomainsJSON is a domain change that waits for its certificate
// (ADR-0068).
type pendingDomainsJSON struct {
	Domains         []string   `json:"domains"`
	RedirectDomains []string   `json:"redirectDomains"`
	RequestedAt     time.Time  `json:"requestedAt"`
	Error           string     `json:"error"`
	FailedAt        *time.Time `json:"failedAt"`
}

func toPendingDomainsJSON(s store.Site) *pendingDomainsJSON {
	c := s.PendingDomains
	if c == nil {
		return nil
	}

	return &pendingDomainsJSON{
		Domains: c.Domains, RedirectDomains: c.Redirect, RequestedAt: c.RequestedAt, Error: c.Error, FailedAt: timeOrNil(c.FailedAt),
	}
}

type sitePHPRequest struct {
	PHP *string `json:"php"`
}

type siteWebRequest struct {
	Dir        *string `json:"dir"`
	SPA        *bool   `json:"spa"`
	AssetCache *bool   `json:"assetCache"`
	// Optional: missing keeps the defaults.
	AssetCacheSeconds int      `json:"assetCacheSeconds"`
	AssetCacheExts    []string `json:"assetCacheExts"`
}

type siteDisabledRequest struct {
	Disabled *bool `json:"disabled"`
	// Page is the maintenance page shown while disabled
	// (ADR-0047); empty: the built-in page.
	Page string `json:"page"`
}

type siteParkRequest struct {
	Domains []string `json:"domains"`
	Page    string   `json:"page"`
	TLS     *bool    `json:"tls"`
	Confirm string   `json:"confirm"`
}

// wwwJSON is the www redirect of a site (ADR-0065).
type wwwJSON struct {
	// Mode is "off", "to-www" or "to-non-www".
	Mode string `json:"mode"`
	// Pairs are the bare and www names that are both domains of the site;
	// the mode acts on these only. A redirect site never uses the mode.
	Pairs []wwwPairJSON `json:"pairs"`
	// Missing are the names whose adding would make a further pair.
	Missing []string `json:"missing"`
}

type wwwPairJSON struct {
	Bare string `json:"bare"`
	WWW  string `json:"www"`
}

// titleRequest sets the display name of a site (ADR-0072).
type titleRequest struct {
	Title *string `json:"title"` // at most 64 characters, "" clears it
}

type wwwRequest struct {
	Mode *string `json:"mode"` // "off", "to-www" or "to-non-www"
}

func toWWWJSON(s store.Site) wwwJSON {
	out := wwwJSON{Mode: "off", Pairs: []wwwPairJSON{}, Missing: agentapi.WWWMissing(s.Domains)}
	if s.WWWRedirect != agentapi.WWWRedirectOff {
		out.Mode = s.WWWRedirect
	}

	for _, p := range agentapi.WWWPairs(s.Domains) {
		out.Pairs = append(out.Pairs, wwwPairJSON(p))
	}

	if out.Missing == nil {
		out.Missing = []string{}
	}

	return out
}

func toSiteJSON(svc *hosting.Service, s store.Site) siteJSON {
	d := s.Domains
	if d == nil {
		d = []string{}
	}

	rd := s.RedirectDomains
	if rd == nil {
		rd = []string{}
	}

	td := s.TLSDomains
	if td == nil {
		td = []string{}
	}

	return siteJSON{
		Name: s.Name, Title: s.Title, Pool: s.Pool, State: s.State, Type: s.Type, Web: toWebJSON(s), Account: s.Account, Redirect: toRedirectJSON(s), Proxy: toProxyJSON(s), Node: toNodeAppJSON(s), App: toProgramAppJSON(s), Python: toPythonAppJSON(s), PHP: s.PHP, Disabled: s.Disabled, DisabledPage: hosting.DisabledPageID(s), Domains: d, RedirectDomains: rd, PendingDomains: toPendingDomainsJSON(s), Maintenance: toMaintenanceJSON(s), WWW: toWWWJSON(s),
		TLS: tlsJSON{
			Enabled: s.TLS, Active: svc.TLSActive(s), HSTS: s.HSTS, Domains: td, NotAfter: timeOrNil(s.TLSNotAfter),
			Error: s.TLSError, FailedAt: timeOrNil(s.TLSFailedAt),
			Source: s.TLSSource, CertSource: s.TLSCertSource, Trusted: s.TLSTrusted,
			RenewAt: timeOrNil(s.TLSRenewAt), RenewPending: s.TLSRenewForced, Challenge: s.TLSChallenge,
		},
		PHPPrograms: s.PHPPrograms,
		CreatedAt:   s.CreatedAt, UpdatedAt: s.UpdatedAt,
	}
}

// agentTimeout bounds one site change: account, web server switch and checks.
const agentTimeout = 25 * time.Second

// siteFail maps service and agent errors. Agent messages are fixed texts
// chosen by the agent (agentrpc.Error), so they may be shown.
func siteFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	var ae *agentrpc.Error

	switch {
	case errors.Is(err, hosting.ErrInvalid):
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())
	case errors.Is(err, hosting.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such site")
	case errors.Is(err, hosting.ErrPageNotFound):
		writeError(w, http.StatusNotFound, "page_not_found", "no such page")
	case errors.Is(err, hosting.ErrExists):
		writeError(w, http.StatusConflict, "site_exists", "a site with this name exists")
	case errors.Is(err, hosting.ErrDomainTaken):
		writeError(w, http.StatusConflict, "domain_taken", err.Error())
	case errors.Is(err, hosting.ErrDomainReserved):
		writeError(w, http.StatusConflict, "domain_reserved", "this domain is the panel's own address")
	case errors.Is(err, hosting.ErrNoTLS):
		writeError(w, http.StatusConflict, "tls_unavailable", "certificates are not available on this panel")
	case errors.Is(err, hosting.ErrNoDNSAccount):
		writeError(w, http.StatusUnprocessableEntity, "no_dns_account", err.Error())
	case errors.Is(err, hosting.ErrDNSAccountInUse):
		writeError(w, http.StatusConflict, "dns_account_in_use", err.Error())
	case errors.Is(err, hosting.ErrDNSAccountsOff):
		writeError(w, http.StatusConflict, "dns_accounts_unavailable", "DNS accounts are not available on this panel")
	case errors.Is(err, hosting.ErrNoPanel):
		writeError(w, http.StatusConflict, "panel_address_missing", "the panel has no address of its own")
	case errors.Is(err, hosting.ErrTooSoon):
		writeError(w, http.StatusTooManyRequests, "renew_too_soon", "renewed less than 10 minutes ago, try again later")
	case errors.Is(err, hosting.ErrCertInUse):
		writeError(w, http.StatusConflict, "certificate_in_use", "turn HTTPS off before deleting the certificate")
	case errors.Is(err, hosting.ErrNotLetsEncrypt):
		writeError(w, http.StatusConflict, "not_letsencrypt", "only a site with HTTPS from Let's Encrypt can be renewed")
	case errors.Is(err, hosting.ErrState):
		writeError(w, http.StatusConflict, "site_busy", "the site is being created or deleted")
	case errors.Is(err, hosting.ErrNodeMissing):
		writeError(w, http.StatusConflict, "node_missing", err.Error())
	case errors.Is(err, hosting.ErrPythonMissing):
		writeError(w, http.StatusConflict, "python_missing", err.Error())
	case errors.Is(err, hosting.ErrNoApps):
		writeError(w, http.StatusConflict, "apps_unavailable", "apps are not available on this panel")
	case errors.Is(err, hosting.ErrNoAppEnv):
		writeError(w, http.StatusConflict, "app_env_unavailable", "environment variables are not available on this panel")
	case errors.Is(err, hosting.ErrSiteType):
		writeError(w, http.StatusConflict, "wrong_site_type", "not possible for this type of site")
	case errors.Is(err, hosting.ErrProxyPrivate):
		writeError(w, http.StatusForbidden, "private_target", "a target in a private network can only be set by an admin or manager")
	case errors.As(err, &ae):
		log.Warn(what+": agent refused", "code", ae.Code, "message", ae.Message)

		switch ae.Code {
		case agentrpc.CodeConflict, agentrpc.CodePrecondition:
			writeError(w, http.StatusConflict, "agent_"+ae.Code, ae.Message)
		case agentrpc.CodeNotFound:
			writeError(w, http.StatusConflict, "agent_not_found", ae.Message)
		case agentrpc.CodeInvalidArgument:
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", ae.Message)
		default:
			writeError(w, http.StatusBadGateway, "agent_error", "the agent could not complete the change")
		}
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "agent_timeout", "the agent did not answer in time")
	default:
		log.Error(what+" failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "the change could not be completed, try again")
	}
}

// registerSites adds the site routes. Who may call which one is in
// internal/authz (ADR-0035).
func registerSites(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/sites", openapi.Operation{
		ID: "listSites", Tag: "sites", Summary: "List the visible sites", Out: siteListJSON{},
	}, a.allow("sites:list", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		sites, err := svc.List(r.Context())
		if err != nil {
			siteFail(log, w, err, "list sites")

			return
		}

		sees, err := a.siteFilter(r, p)
		if err != nil {
			a.fail(w, err, "list sites")

			return
		}

		marks, err := svc.ListMarks(r.Context(), sites)
		if err != nil {
			siteFail(log, w, err, "list sites")

			return
		}

		out := make([]siteJSON, 0, len(sites))
		outMarks := map[string]siteMarksJSON{}

		for _, s := range sites {
			if sees != nil && !sees(s.Name) {
				continue
			}

			out = append(out, toSiteJSON(svc, s))

			if m, ok := marks[s.ID]; ok {
				outMarks[s.Name] = siteMarksJSON{OpenFindings: m.OpenFindings, CloneOf: m.CloneOf, Staging: m.Staging}
			}
		}

		writeJSON(w, http.StatusOK, siteListJSON{Sites: out, Marks: outMarks})
	}))

	mux.Op("GET /api/v1/sites/{name}", openapi.Operation{
		ID: "getSite", Tag: "sites", Summary: "Get a site", Out: siteJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		s, err := svc.Get(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "get site")

			return
		}

		out := toSiteJSON(svc, s)

		if out.ServedElsewhere, err = svc.ServedElsewhere(r.Context(), s); err != nil {
			siteFail(log, w, err, "get site")

			return
		}

		if out.WildcardsWithoutDNS, err = svc.WildcardsWithoutDNS(r.Context(), s); err != nil {
			siteFail(log, w, err, "get site")

			return
		}

		writeJSON(w, http.StatusOK, out)
	}))

	mux.Op("POST /api/v1/sites", openapi.Operation{
		ID: "createSite", Tag: "sites", Summary: "Create a site (static, PHP, Node.js, app, Python, reverse proxy or redirect); reverse proxy and redirect sites also take *.<domain> after the primary domain", In: siteCreateRequest{}, Out: siteJSON{}, Status: http.StatusCreated,
	}, a.allow("sites:create", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in siteCreateRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		var (
			redirect *store.SiteRedirect
			proxy    *store.SiteProxy
			app      *store.SiteApp
		)

		if (in.Node != nil) != (in.Type == store.SiteTypeNode) || in.Node != nil && (in.Redirect != nil || in.Proxy != nil) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "a Node.js site needs type node and node, nothing else")

			return
		}

		if (in.App != nil) != (in.Type == store.SiteTypeApp) || in.App != nil && (in.Redirect != nil || in.Proxy != nil || in.Node != nil) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "an app site needs type app and app, nothing else")

			return
		}

		if (in.Python != nil) != (in.Type == store.SiteTypePython) || in.Python != nil && (in.Redirect != nil || in.Proxy != nil || in.Node != nil || in.App != nil) {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "a Python site needs type python and python, nothing else")

			return
		}

		if in.Python != nil {
			if in.Python.Version == "" {
				writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "a Python site needs python.version")

				return
			}

			a := in.Python.app()
			app = &a
		}

		if in.Node != nil {
			if in.Node.Version == "" {
				writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "a Node.js site needs node.version")

				return
			}

			a := in.Node.app()
			app = &a
		}

		if in.App != nil {
			a := in.App.app()
			app = &a
		}

		switch {
		case in.Type == store.SiteTypeRedirect && in.Redirect != nil && in.Proxy == nil:
			redirect = &store.SiteRedirect{Target: in.Redirect.Target, Status: in.Redirect.Status, KeepPath: in.Redirect.KeepPath}
		case in.Type == store.SiteTypeProxy && in.Proxy != nil && in.Redirect == nil:
			proxy = &store.SiteProxy{Target: in.Proxy.Target, TargetHost: in.Proxy.TargetHost, Insecure: in.Proxy.Insecure}
		case in.Redirect != nil || in.Type == store.SiteTypeRedirect:
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "a redirect site needs type redirect and a redirect")

			return
		case in.Proxy != nil || in.Type == store.SiteTypeProxy:
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "a reverse proxy site needs type proxy and a proxy")

			return
		case in.Type == store.SiteTypeStatic && in.PHP != "", in.Type == store.SiteTypePHP && in.PHP == "":
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "type and php do not fit: static has no PHP version, php needs one")

			return
		case in.Type != "" && in.Type != store.SiteTypeStatic && in.Type != store.SiteTypePHP && in.Type != store.SiteTypeNode && in.Type != store.SiteTypeApp && in.Type != store.SiteTypePython:
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "type must be static, php, node, app, python, proxy or redirect")

			return
		}

		if in.Headers == nil {
			d := agentapi.DefaultWebHeaders
			in.Headers = &d
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.Create(ctx, p.User.ID, hosting.CreateRequest{
			Name: in.Name, Title: in.Title, Pool: in.Pool, PHP: in.PHP, Domains: in.Domains, TLS: in.TLS, HSTS: in.HSTS, Headers: in.Headers, Redirect: redirect, Proxy: proxy, App: app,
			Profile: in.Profile,
		})
		if err != nil {
			headersFail(log, w, err, "create site")

			return
		}

		writeJSON(w, http.StatusCreated, toSiteJSON(svc, s))
	}))

	mux.Op("PUT /api/v1/sites/{name}/redirect", openapi.Operation{
		ID: "setSiteRedirect", Tag: "sites", Summary: "Set the target of a redirect site", In: redirectJSON{}, Out: siteJSON{},
	}, a.allow("sites.redirect:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in redirectJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetRedirect(ctx, p.User.ID, r.PathValue("name"), store.SiteRedirect{Target: in.Target, Status: in.Status, KeepPath: in.KeepPath})
		if err != nil {
			siteFail(log, w, err, "set redirect")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	mux.Op("PUT /api/v1/sites/{name}/proxy", openapi.Operation{
		ID: "setSiteProxy", Tag: "sites", Summary: "Set the target of a reverse proxy site", In: proxyJSON{}, Out: siteJSON{},
	}, a.allow("sites.proxy:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in proxyJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		private := authz.Allowed(authz.Role(p.User.Role), "sites.proxy.private:write")

		s, err := svc.SetProxy(ctx, p.User.ID, r.PathValue("name"), store.SiteProxy{Target: in.Target, TargetHost: in.TargetHost, Insecure: in.Insecure}, private)
		if err != nil {
			siteFail(log, w, err, "set proxy")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	mux.Op("PUT /api/v1/sites/{name}/domains", openapi.Operation{
		ID: "setSiteDomains", Tag: "sites",
		Summary: "Set the site's served and redirect domains; 202 when the change waits for its certificate (ADR-0068). Redirect and reverse proxy sites also take *.<domain> after the primary domain (ADR-0075)",
		In:      siteDomainsRequest{}, Out: siteJSON{}, AlsoStatus: []int{http.StatusAccepted},
	}, a.allow("sites.domains:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in siteDomainsRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		req := hosting.DomainsRequest{Domains: in.Domains, KeepRedirect: in.RedirectDomains == nil}
		if in.RedirectDomains != nil {
			req.RedirectDomains = *in.RedirectDomains
		}

		if in.WaitForCertificate != nil && !*in.WaitForCertificate {
			req.Now = true
		}

		s, pending, err := svc.ChangeDomains(ctx, p.User.ID, r.PathValue("name"), req)
		if err != nil {
			siteFail(log, w, err, "set domains")

			return
		}

		status := http.StatusOK
		if pending != nil {
			status = http.StatusAccepted
		}

		writeJSON(w, status, toSiteJSON(svc, s))
	}))

	mux.Op("DELETE /api/v1/sites/{name}/domains/pending", openapi.Operation{
		ID: "cancelSiteDomainChange", Tag: "sites", Summary: "Cancel the site's domain change that waits for its certificate (ADR-0068)", Out: siteJSON{},
	}, a.allow("sites.domains:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		name := r.PathValue("name")

		if err := svc.CancelDomainChange(r.Context(), p.User.ID, name); err != nil {
			if errors.Is(err, hosting.ErrNotFound) {
				if _, gerr := svc.Get(r.Context(), name); gerr == nil {
					writeError(w, http.StatusNotFound, "no_pending_change", "the site has no pending domain change")

					return
				}
			}

			siteFail(log, w, err, "cancel domain change")

			return
		}

		s, err := svc.Get(r.Context(), name)
		if err != nil {
			siteFail(log, w, err, "get site")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	mux.Op("PUT /api/v1/sites/{name}/php", openapi.Operation{
		ID: "setSitePHP", Tag: "sites", Summary: "Set the site's PHP version (empty turns PHP off)", In: sitePHPRequest{}, Out: siteJSON{},
	}, a.allow("sites.php:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in sitePHPRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.PHP == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"php" is required ("" turns PHP off)`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetPHP(ctx, p.User.ID, r.PathValue("name"), *in.PHP)
		if err != nil {
			siteFail(log, w, err, "set php")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// The document root folder and the static-site options (ADR-0054).
	mux.Op("PUT /api/v1/sites/{name}/web", openapi.Operation{
		ID: "setSiteWeb", Tag: "sites", Summary: "Set the document root and the static-site options", In: siteWebRequest{}, Out: siteJSON{},
	}, a.allow("sites.web:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in siteWebRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Dir == nil || in.SPA == nil || in.AssetCache == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"dir", "spa" and "assetCache" are required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetWebOptions(ctx, p.User.ID, r.PathValue("name"), store.WebOptions{
			Dir: *in.Dir, SPA: *in.SPA, AssetCache: *in.AssetCache, AssetCacheSeconds: in.AssetCacheSeconds, AssetCacheExts: in.AssetCacheExts,
		})
		if err != nil {
			siteFail(log, w, err, "set web options")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	mux.Op("PUT /api/v1/sites/{name}/tls", openapi.Operation{
		ID: "setSiteTLS", Tag: "sites", Summary: "Turn HTTPS on or off; 202 while the certificate is requested", In: enabledRequest{}, Out: siteJSON{}, AlsoStatus: []int{http.StatusAccepted},
	}, a.allow("sites.tls:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in enabledRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Enabled == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"enabled" is required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		// On: the certificate comes in the background; "tls.active" turns
		// true when the site is served over HTTPS.
		s, err := svc.SetTLS(ctx, p.User.ID, r.PathValue("name"), *in.Enabled)
		if err != nil {
			siteFail(log, w, err, "set tls")

			return
		}

		code := http.StatusOK
		if *in.Enabled && !svc.TLSActive(s) {
			code = http.StatusAccepted
		}

		writeJSON(w, code, toSiteJSON(svc, s))
	}))

	// The www redirect sends one name of each pair of d and www.d to the
	// other (ADR-0065).
	mux.Op("PUT /api/v1/sites/{name}/www", openapi.Operation{
		ID: "setSiteWWWRedirect", Tag: "sites", Summary: "Set the redirect between example.com and www.example.com", In: wwwRequest{}, Out: siteJSON{},
	}, a.allow("sites.www:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in wwwRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Mode == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"mode" is required`)

			return
		}

		mode := *in.Mode
		switch mode {
		case "off":
			mode = agentapi.WWWRedirectOff
		case agentapi.WWWRedirectToWWW, agentapi.WWWRedirectToNonWWW:
		default:
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"mode" must be "off", "to-www" or "to-non-www"`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetWWWRedirect(ctx, p.User.ID, r.PathValue("name"), mode)
		if err != nil {
			siteFail(log, w, err, "set www redirect")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// The display name changes nothing on the server (ADR-0072).
	mux.Op("PUT /api/v1/sites/{name}/title", openapi.Operation{
		ID: "setSiteTitle", Tag: "sites", Summary: "Set the display name of a site (empty clears it); the system name stays", In: titleRequest{}, Out: siteJSON{},
	}, a.allow("sites.title:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in titleRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Title == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"title" is required`)

			return
		}

		s, err := svc.SetTitle(r.Context(), p.User.ID, r.PathValue("name"), *in.Title)
		if err != nil {
			siteFail(log, w, err, "set title")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// HSTS can be set before HTTPS is active; it is sent only over HTTPS.
	mux.Op("PUT /api/v1/sites/{name}/hsts", openapi.Operation{
		ID: "setSiteHSTS", Tag: "sites", Summary: "Turn HSTS on or off (sent over HTTPS only)", In: enabledRequest{}, Out: siteJSON{},
	}, a.allow("sites.hsts:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in enabledRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Enabled == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"enabled" is required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetHSTS(ctx, p.User.ID, r.PathValue("name"), *in.Enabled)
		if err != nil {
			siteFail(log, w, err, "set hsts")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// Programs from PHP (exec, system, ...) are off unless allowed here.
	mux.Op("PUT /api/v1/sites/{name}/php-programs", openapi.Operation{
		ID: "setSitePHPPrograms", Tag: "sites", Summary: "Allow or forbid programs from PHP (exec, system, ...)", In: enabledRequest{}, Out: siteJSON{},
	}, a.allow("sites.programs:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in enabledRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Enabled == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"enabled" is required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetPHPPrograms(ctx, p.User.ID, r.PathValue("name"), *in.Enabled)
		if err != nil {
			siteFail(log, w, err, "set php programs")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// Disabling keeps everything of the site but serves 503 and stops PHP.
	mux.Op("PUT /api/v1/sites/{name}/disabled", openapi.Operation{
		ID: "setSiteDisabled", Tag: "sites", Summary: "Disable or enable a site", In: siteDisabledRequest{}, Out: siteJSON{},
	}, a.allow("sites.disabled:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in siteDisabledRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Disabled == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"disabled" is required`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetDisabled(ctx, p.User.ID, r.PathValue("name"), *in.Disabled, in.Page)
		if err != nil {
			siteFail(log, w, err, "set disabled")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// The versions a site can use: installed ones from the agent, plus the
	// supported list and which of them are legacy (ADR-0019).
	mux.Op("GET /api/v1/php/versions", openapi.Operation{
		ID: "listPHPVersions", Tag: "php", Summary: "PHP versions a site can use", Out: phpVersionsJSON{},
	}, a.allow("php.versions:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		installed, err := svc.PHPVersions(ctx)
		if err != nil {
			siteFail(log, w, err, "php versions")

			return
		}

		if installed == nil {
			installed = []string{}
		}

		security, active := phpVersionInfo()
		writeJSON(w, http.StatusOK, phpVersionsJSON{
			Installed: installed, Supported: agentapi.PHPVersions, Legacy: agentapi.LegacyPHPVersions,
			// End of security fixes and of active support per version (ADR-0036).
			SecurityUntil: security, ActiveUntil: active,
			// The distribution's own version, marked in the panel (ADR-0037).
			Distro: distroPHP(),
		})
	}))

	mux.Op("DELETE /api/v1/sites/{name}", openapi.Operation{
		ID: "deleteSite", Tag: "sites", Summary: "Delete a site",
	}, a.allow("sites:delete", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.Delete(ctx, p.User.ID, r.PathValue("name")); err != nil {
			siteFail(log, w, err, "delete site")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	// Deletes the site and parks the chosen domains of it (ADR-0048). It
	// deletes like DELETE and parks like PUT parked-domains, so it needs
	// both permissions.
	mux.Op("POST /api/v1/sites/{name}/park", openapi.Operation{
		ID: "deleteAndParkSite", Tag: "sites", Summary: "Delete a site and park its chosen domains", In: siteParkRequest{},
	}, a.allow("sites:park", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		// Parked domains are server-wide: a token limited to some sites
		// may not make them.
		if !alsoAllowed(w, p, "parked:write") {
			return
		}

		var in siteParkRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		req := hosting.ParkRequest{Domains: in.Domains, Page: in.Page, TLS: in.TLS == nil || *in.TLS, Confirm: in.Confirm}
		if err := svc.DeleteAndPark(ctx, p.User.ID, r.PathValue("name"), req); err != nil {
			pageFail(log, w, err, "delete and park site")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	// The panel's own HTTPS (ADR-0023). Disabled without a host name in
	// the public URL; then the panel is reachable through the SSH tunnel only.
	mux.Op("GET /api/v1/panel/tls", openapi.Operation{
		ID: "getPanelTLS", Tag: "panel", Summary: "The panel's own HTTPS", Out: panelTLSJSON{},
	}, a.allow("panel.tls:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		p, err := svc.PanelTLS(r.Context())
		if err != nil {
			siteFail(log, w, err, "panel tls")

			return
		}

		writeJSON(w, http.StatusOK, toPanelTLSJSON(p))
	}))

	// Sends the stored state to the web server again, for example after a
	// change failed while the agent was down.
	mux.Op("POST /api/v1/web/sync", openapi.Operation{
		ID: "syncWeb", Tag: "system", Summary: "Send the stored state to the web server, cron and SSH again",
	}, a.allow("web:sync", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.Sync(ctx); err != nil {
			siteFail(log, w, err, "web sync")

			return
		}

		// The cron jobs too (ADR-0028); a site that fails is logged.
		if err := svc.SyncCron(ctx); err != nil {
			log.WarnContext(ctx, "cron sync", "err", err)
		}

		// And the SSH keys (ADR-0032).
		if err := svc.SyncSSH(ctx); err != nil {
			log.WarnContext(ctx, "ssh sync", "err", err)
		}

		w.WriteHeader(http.StatusNoContent)
	}))
}
