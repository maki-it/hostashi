// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

// TypeRequest is a new type for a site with the settings of that type
// (ADR-0054 addendum 3): PHP for php, Redirect for redirect, Proxy for
// proxy, App for node (with Node) and app (without). static takes none.
// Settings of another type are refused.
type TypeRequest struct {
	Type     string
	PHP      string
	Redirect *store.SiteRedirect
	Proxy    *store.SiteProxy
	App      *store.SiteApp
}

// accountType reports whether sites of a type run on a Linux account.
func accountType(typ string) bool {
	switch typ {
	case store.SiteTypeStatic, store.SiteTypePHP, store.SiteTypeNode, store.SiteTypeApp, store.SiteTypePython:
		return true
	}

	return false
}

// checkType normalizes and checks a type change of site to the store's
// form.
func (s *Service) checkType(ctx context.Context, site store.Site, req TypeRequest) (store.SiteTypeChange, error) {
	c := store.SiteTypeChange{Type: req.Type, Account: site.Account || accountType(req.Type)}

	if req.Type == site.Type {
		return c, fmt.Errorf("%w: the site already has type %s", ErrInvalid, req.Type)
	}

	if w := wildcardsOf(site.AllDomains()); len(w) > 0 && !wildcardType(req.Type) {
		return c, fmt.Errorf("%w: only redirect and reverse proxy sites can have a wildcard domain; remove %s first", ErrInvalid, strings.Join(w, ", "))
	}

	wrong := func(field string, set bool) error {
		if set {
			return fmt.Errorf("%w: %s does not belong to type %s", ErrInvalid, field, req.Type)
		}

		return nil
	}

	if err := wrong("php", req.PHP != "" && req.Type != store.SiteTypePHP); err != nil {
		return c, err
	}

	if err := wrong("redirect", req.Redirect != nil && req.Type != store.SiteTypeRedirect); err != nil {
		return c, err
	}

	if err := wrong("proxy", req.Proxy != nil && req.Type != store.SiteTypeProxy); err != nil {
		return c, err
	}

	if err := wrong("app", req.App != nil && !appType(req.Type)); err != nil {
		return c, err
	}

	switch req.Type {
	case store.SiteTypeStatic:
	case store.SiteTypePHP:
		if req.PHP == "" {
			return c, fmt.Errorf("%w: type php needs a PHP version", ErrInvalid)
		}

		if err := checkPHP(req.PHP); err != nil {
			return c, err
		}

		c.PHP = req.PHP
	case store.SiteTypeRedirect:
		if req.Redirect == nil {
			return c, fmt.Errorf("%w: type redirect needs a target", ErrInvalid)
		}

		r, err := checkRedirect(*req.Redirect, site.AllDomains())
		if err != nil {
			return c, err
		}

		if err := s.checkRedirectLoop(ctx, site.Name, r, site.AllDomains()); err != nil {
			return c, err
		}

		c.Redirect = &r
	case store.SiteTypeProxy:
		if req.Proxy == nil {
			return c, fmt.Errorf("%w: type proxy needs a target", ErrInvalid)
		}

		if err := s.checkProxy(ctx, *req.Proxy, site.AllDomains()); err != nil {
			return c, err
		}

		p := *req.Proxy
		c.Proxy = &p
	case store.SiteTypeNode, store.SiteTypeApp, store.SiteTypePython:
		if req.App == nil {
			return c, fmt.Errorf("%w: type %s needs an app", ErrInvalid, req.Type)
		}

		a, err := checkApp(*req.App, req.Type)
		if err != nil {
			return c, err
		}

		if err := s.checkRuntime(ctx, req.Type, a); err != nil {
			return c, err
		}

		a.Port, a.Stopped = 0, false
		c.App = &a
	default:
		return c, fmt.Errorf("%w: unknown site type %q", ErrInvalid, req.Type)
	}

	return c, nil
}

// SetType changes the type of an active site (ADR-0054 addendum 3). The
// account and everything in it stays; a site without one gets it as on
// create. In the order of ADR-0048: the store, then the new runtime (the
// account, the app), then one web state; only then the old app goes. If
// the new runtime or the web state fails, the old type is stored again and
// the new runtime removed. A failure to remove the old app is logged;
// Sync removes it.
func (s *Service) SetType(ctx context.Context, actor int64, name string, req TypeRequest) (store.Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, ErrState
	}

	c, err := s.checkType(ctx, site, req)
	if err != nil {
		return store.Site{}, err
	}

	old, err := s.st.SetSiteType(ctx, name, c, s.now())
	if err != nil {
		return store.Site{}, err
	}

	next, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	newAccount := next.Account && !site.Account
	sw := typeSwitch{s: s, site: site, next: next, old: old, newAccount: newAccount}

	if err := sw.prepare(ctx); err != nil {
		sw.undo(context.WithoutCancel(ctx))

		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		sw.undo(context.WithoutCancel(ctx))

		return store.Site{}, err
	}

	// The old app goes after nginx no longer passes requests to it.
	if hasApp(site) && !hasApp(next) {
		if err := s.apps.AppRemove(ctx, name); err != nil {
			s.log.ErrorContext(ctx, "remove the app after a type change", "site", name, "err", err)
		}
	}

	s.auditChange(ctx, actor, "site.type", name, map[string]string{"type": next.Type, "previous": site.Type},
		typeAudit(site), typeAudit(next))

	if newAccount && sw.hasLimits {
		if err := s.applyLimits(ctx, next, sw.limits); err != nil {
			s.log.WarnContext(ctx, "apply limits after a type change", "site", name, "err", err)
		}

		s.presetCaches(ctx, actor, next, sw.caches)
	}

	return s.Get(ctx, name)
}

// typeSwitch is one type change in progress.
type typeSwitch struct {
	s          *Service
	site, next store.Site
	old        store.SiteTypeChange
	newAccount bool

	accountMade bool
	limitsMade  bool
	appApplied  bool

	limits    store.Limits
	caches    store.PresetCaches
	hasLimits bool
}

// prepare sets up the account and the app of the new type.
func (t *typeSwitch) prepare(ctx context.Context) error {
	s := t.s

	if t.newAccount {
		if _, err := s.ag.SiteCreate(ctx, agentapi.SiteCreateRequest{User: t.next.Name, Pool: t.next.Pool}); err != nil {
			return err
		}

		t.accountMade = true

		// Before the web state, so a PHP pool starts with the preset's
		// workers, as on create.
		t.limits, t.caches, t.hasLimits = s.storeDefaultLimits(ctx, t.next, nil)
		t.limitsMade = t.hasLimits
	}

	if hasApp(t.next) {
		t.appApplied = true

		if _, err := s.applyApp(ctx, t.next); err != nil {
			return err
		}
	}

	return nil
}

// undo stores the old type again and takes the new runtime away.
func (t *typeSwitch) undo(ctx context.Context) {
	s, name := t.s, t.site.Name

	if _, err := s.st.SetSiteType(ctx, name, t.old, s.now()); err != nil {
		s.log.ErrorContext(ctx, "restore the type after a failed change", "site", name, "err", err)
	}

	if t.appApplied {
		var err error
		if hasApp(t.site) {
			_, err = s.applyApp(ctx, t.site)
		} else {
			err = s.apps.AppRemove(ctx, name)
		}

		if err != nil {
			s.log.ErrorContext(ctx, "restore the app after a failed type change", "site", name, "err", err)
		}
	}

	if t.limitsMade {
		if err := s.st.RestoreSiteLimits(ctx, store.SiteLimits{SiteID: t.site.ID}, false); err != nil {
			s.log.ErrorContext(ctx, "remove limits after a failed type change", "site", name, "err", err)
		}
	}

	if t.accountMade {
		if err := s.ag.SiteDelete(ctx, agentapi.SiteDeleteRequest{User: name, AccountOnly: true}); err != nil && !isCode(err, agentrpc.CodeNotFound) {
			s.log.ErrorContext(ctx, "remove the account after a failed type change", "site", name, "err", err)
		}
	}
}

// typeAudit is a site's type with its settings, for the audit diff.
func typeAudit(site store.Site) map[string]string {
	out := map[string]string{"type": site.Type, "account": strconv.FormatBool(site.Account)}

	switch {
	case site.Type == store.SiteTypePHP:
		out["php"] = site.PHP
	case site.Type == store.SiteTypeRedirect && site.Redirect != nil:
		r := site.Redirect
		out["redirectTarget"], out["redirectStatus"], out["redirectKeepPath"] = r.Target, strconv.Itoa(r.Status), strconv.FormatBool(r.KeepPath)
	case site.Type == store.SiteTypeProxy && site.Proxy != nil:
		p := site.Proxy
		out["proxyTarget"], out["proxyTargetHost"], out["proxyInsecure"] = p.Target, strconv.FormatBool(p.TargetHost), strconv.FormatBool(p.Insecure)
	case hasApp(site):
		maps.Copy(out, appAudit(*site.App))
	}

	return out
}

// RemoveAccount removes the Linux account of a redirect or reverse proxy
// site that kept it after a type change (ADR-0054 addendum 3): its
// databases, home, files, cron jobs, SSH keys, limits and caches. nginx
// keeps serving the site, with its certificate. A removal that stops
// halfway can be repeated.
func (s *Service) RemoveAccount(ctx context.Context, actor int64, name string) (store.Site, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	switch {
	case site.State != store.SiteActive:
		return store.Site{}, ErrState
	case !noFiles(site.Type):
		return store.Site{}, ErrSiteType
	case !site.Account:
		return store.Site{}, fmt.Errorf("%w: the site has no account", ErrInvalid)
	}

	// The databases go before the account.
	if err := s.deleteSiteDatabases(ctx, actor, name); err != nil {
		return store.Site{}, err
	}

	if err := s.ag.SiteDelete(ctx, agentapi.SiteDeleteRequest{User: name, AccountOnly: true}); err != nil && !isCode(err, agentrpc.CodeNotFound) {
		return store.Site{}, err
	}

	if err := s.st.RemoveSiteAccount(ctx, name, s.now()); err != nil {
		return store.Site{}, err
	}

	s.audit(ctx, actor, "site.account.remove", name, map[string]string{"type": site.Type})

	return s.Get(ctx, name)
}
