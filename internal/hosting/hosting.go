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
	"strings"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

// Agent is the part of the agent this package uses. The production one wraps
// an agentrpc.Client; tests use a fake.
type Agent interface {
	SiteCreate(ctx context.Context, req agentapi.SiteCreateRequest) (agentapi.SiteInfo, error)
	SiteDelete(ctx context.Context, req agentapi.SiteDeleteRequest) error
	WebApply(ctx context.Context, req agentapi.WebApplyRequest) (agentapi.WebApplyResponse, error)
	PHPVersions(ctx context.Context) ([]string, error)
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

// Errors a caller can act on. Errors of the agent come back as
// *agentrpc.Error with its code.
var (
	ErrInvalid     = errors.New("hosting: invalid input")
	ErrExists      = errors.New("hosting: a site with this name exists")
	ErrDomainTaken = errors.New("hosting: domain belongs to another site")
	ErrNotFound    = errors.New("hosting: unknown site")
	ErrState       = errors.New("hosting: the site is being created or deleted")
)

// Service manages sites. Changes are serialized: each one ends with the
// complete web state, which must not interleave with another change.
type Service struct {
	st  *store.Store
	ag  Agent
	log *slog.Logger
	now func() time.Time
	mu  sync.Mutex
}

// New builds a Service.
func New(st *store.Store, ag Agent, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	return &Service{st: st, ag: ag, log: log, now: time.Now}
}

// CreateRequest describes a new site.
type CreateRequest struct {
	Name    string
	Pool    string // empty: the default pool
	PHP     string // PHP version, empty: no PHP
	Domains []string
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

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.st.CreateSite(ctx, req.Name, req.Pool, req.PHP, domains, s.now())

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

	if _, err := s.ag.SiteCreate(ctx, agentapi.SiteCreateRequest{User: req.Name, Pool: req.Pool}); err != nil {
		if derr := s.st.DeleteSite(undo, req.Name); derr != nil {
			s.log.Error("remove site row after failed create", "site", req.Name, "err", derr)
		}

		return store.Site{}, err
	}

	if err := s.st.SetSiteState(ctx, req.Name, store.SiteActive, s.now()); err != nil {
		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		s.removeAfterFailure(undo, req.Name)

		return store.Site{}, err
	}

	s.audit(ctx, actor, "site.create", req.Name, map[string]string{"pool": req.Pool, "php": req.PHP, "domains": strings.Join(domains, " ")})

	site.State = store.SiteActive

	return site, nil
}

// removeAfterFailure takes a just created site away again. If the account
// cannot be removed, the row stays in state deleting so a delete can finish
// the job.
func (s *Service) removeAfterFailure(ctx context.Context, name string) {
	if err := s.st.SetSiteState(ctx, name, store.SiteDeleting, s.now()); err != nil {
		s.log.Error("mark site deleting after failed create", "site", name, "err", err)

		return
	}

	if err := s.ag.SiteDelete(ctx, agentapi.SiteDeleteRequest{User: name}); err != nil && !isCode(err, agentrpc.CodeNotFound) {
		s.log.Error("remove site account after failed create", "site", name, "err", err)

		return
	}

	if err := s.st.DeleteSite(ctx, name); err != nil {
		s.log.Error("remove site row after failed create", "site", name, "err", err)
	}
}

// SetDomains replaces the domains of an active site. If the new virtual host
// cannot go live, the previous domains are stored again.
func (s *Service) SetDomains(ctx context.Context, actor int64, name string, in []string) (store.Site, error) {
	domains, err := NormalizeDomains(in)
	if err != nil {
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

	old, err := s.st.ReplaceSiteDomains(ctx, name, domains, s.now())
	if errors.Is(err, store.ErrDomainTaken) {
		return store.Site{}, fmt.Errorf("%w: %w", ErrDomainTaken, err)
	}

	if err != nil {
		return store.Site{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.ReplaceSiteDomains(context.WithoutCancel(ctx), name, old, s.now()); rerr != nil {
			s.log.Error("restore domains after failed change", "site", name, "err", rerr)
		}

		return store.Site{}, err
	}

	s.audit(ctx, actor, "site.domains", name, map[string]string{"domains": strings.Join(domains, " ")})

	site.Domains = domains

	return site, nil
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
			s.log.Error("restore php version after failed change", "site", name, "err", rerr)
		}

		return store.Site{}, err
	}

	s.audit(ctx, actor, "site.php", name, map[string]string{"php": version, "previous": old})

	site.PHP = version

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
				s.log.Error("restore site state after failed delete", "site", name, "err", rerr)
			}
		}

		return err
	}

	if err := s.ag.SiteDelete(ctx, agentapi.SiteDeleteRequest{User: name}); err != nil && !isCode(err, agentrpc.CodeNotFound) {
		return err
	}

	if err := s.st.DeleteSite(ctx, name); err != nil {
		return err
	}

	s.audit(ctx, actor, "site.delete", name, nil)

	return nil
}

// Sync sends the current web state to the agent again, for example after a
// change that failed because the agent was unavailable.
func (s *Service) Sync(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.applyWeb(ctx)
}

// applyWeb sends the complete web state: every active site with its domains
// and PHP version.
func (s *Service) applyWeb(ctx context.Context) error {
	sites, err := s.st.Sites(ctx)
	if err != nil {
		return err
	}

	req := agentapi.WebApplyRequest{Sites: []agentapi.WebSite{}}

	for _, site := range sites {
		if site.State == store.SiteActive && len(site.Domains) > 0 {
			req.Sites = append(req.Sites, agentapi.WebSite{User: site.Name, Domains: site.Domains, PHP: site.PHP})
		}
	}

	res, err := s.ag.WebApply(ctx, req)
	if err != nil {
		return err
	}

	if res.Changed {
		s.log.Info("web server state applied", "generation", res.Generation, "sites", len(req.Sites))
	}

	return nil
}

func (s *Service) audit(ctx context.Context, actor int64, action, target string, detail map[string]string) {
	if err := s.st.Audit(ctx, &actor, action, target, detail); err != nil {
		s.log.Error("audit log", "action", action, "err", err)
	}
}

func isCode(err error, code string) bool {
	var e *agentrpc.Error

	return errors.As(err, &e) && e.Code == code
}
