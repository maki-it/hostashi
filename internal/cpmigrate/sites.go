// SPDX-License-Identifier: AGPL-3.0-or-later

package cpmigrate

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/plan"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
)

// Errors of creating and importing sites.
var (
	ErrNoSite = errors.New("cpmigrate: no such site in the report")
	// ErrSkipped: the plan skips the site; PlanError carries the reasons.
	ErrSkipped = errors.New("cpmigrate: the plan skips this site")
	// ErrNeedsReview: the plan marks the site for review and the request
	// did not confirm it.
	ErrNeedsReview = errors.New("cpmigrate: the site needs a review first")
	ErrBadRequest  = errors.New("cpmigrate: invalid request")
	ErrNoImporter  = errors.New("cpmigrate: importing is not available")
)

// PlanError is ErrSkipped or ErrNeedsReview with the plan's reasons.
type PlanError struct {
	Err     error
	Reasons []plan.Reason
}

func (e *PlanError) Error() string { return e.Err.Error() }
func (e *PlanError) Unwrap() error { return e.Err }

// NewSite is what CreateSite hands to the site service.
type NewSite struct {
	Name    string
	PHP     string
	Domains []string
	// Cron are the old site's jobs that Hostashi accepts (plan.CronJobs).
	// They are created paused: they start after the DNS switch, so no job
	// runs on both servers (ADR-0028).
	Cron []scan.CronJob
	// Usage is what the old home uses, nil if the scan did not measure it.
	// The site's disk limits start from it (ADR-0015).
	Usage *scan.Usage
	// SSHKeys are the site user's keys, added as SFTP only (ADR-0032).
	SSHKeys []plan.SSHKey
	// PageCache is the preset of the page cache switched on with the
	// site, "" for none (ADR-0046).
	PageCache string
}

// Created counts what came with a new site.
type Created struct {
	Cron      int
	SSHKeys   int
	PageCache bool
}

// SiteCreator creates a site on this server (hosting.Service.Create), its
// paused cron jobs and its SSH keys. It returns how many of them it
// created; a job or key that fails does not undo the site.
type SiteCreator func(ctx context.Context, actor int64, s NewSite) (Created, error)

// Importer starts and reads imports in the agent (ADR-0026 Part 3).
type Importer interface {
	Start(ctx context.Context, req agentapi.CloudPanelImportRequest) (agentapi.CloudPanelImport, error)
	Get(ctx context.Context, id string) (agentapi.CloudPanelImport, error)
	// List returns the kept imports newest first, of one site or all.
	List(ctx context.Context, site string) ([]agentapi.CloudPanelImport, error)
	// Activate makes the site's imported files its web root.
	Activate(ctx context.Context, site string) (agentapi.CloudPanelActivated, error)
}

// Connect gives the service the site service and the agent. Without them
// CreateSite and StartImport return ErrNoImporter.
func (s *Service) Connect(create SiteCreator, imp Importer) {
	s.create, s.importer = create, imp
}

// CreateOptions confirm or change what the plan proposes.
type CreateOptions struct {
	// Reviewed confirms a site whose plan action is review.
	Reviewed bool
	// PHP replaces the plan's PHP version, for a site that has PHP.
	PHP string
	// PageCache false leaves off the page cache the plan proposes; nil
	// follows the plan (ADR-0046).
	PageCache *bool
}

// CreateSite creates the site of the report that the plan names, with the
// old site's name, domains (redirect hosts as aliases) and PHP version.
// Names and domains come from the stored report only. No certificate is
// requested: DNS still points to the old server.
func (s *Service) CreateSite(ctx context.Context, actor int64, reportID, name string, o CreateOptions) (plan.Site, error) {
	if s.create == nil {
		return plan.Site{}, ErrNoImporter
	}

	ps, site, err := s.planSite(ctx, reportID, name)
	if err != nil {
		return plan.Site{}, err
	}

	switch ps.Action {
	case plan.ActionSkip:
		return ps, &PlanError{Err: ErrSkipped, Reasons: ps.Reasons}
	case plan.ActionReview:
		if !o.Reviewed {
			return ps, &PlanError{Err: ErrNeedsReview, Reasons: ps.Reasons}
		}
	case plan.ActionImport:
	}

	php := ps.PHP
	if o.PHP != "" {
		if ps.PHP == "" {
			return ps, fmt.Errorf("%w: the site has no PHP", ErrBadRequest)
		}

		if err := agentapi.ValidatePHPVersion(o.PHP); err != nil {
			return ps, fmt.Errorf("%w: %w", ErrBadRequest, err)
		}

		php = o.PHP
	}

	domains := append(slices.Clone(ps.Domains), ps.RedirectFrom...)

	cache := ps.PageCache
	if o.PageCache != nil && !*o.PageCache {
		cache = ""
	}

	n, err := s.create(ctx, actor, NewSite{
		Name: ps.Name, PHP: php, Domains: domains, Cron: plan.CronJobs(site), Usage: site.Usage, SSHKeys: plan.SSHKeys(site),
		PageCache: cache,
	})
	if err != nil {
		return ps, err
	}

	s.audit(ctx, actor, "cloudpanel.site.create", ps.Name, map[string]string{
		"report": reportID, "php": php, "cron": strconv.Itoa(n.Cron), "sshKeys": strconv.Itoa(n.SSHKeys),
		"pageCache": strconv.FormatBool(n.PageCache),
	})

	ps.PHP, ps.CronImport, ps.SSHImport = php, n.Cron, n.SSHKeys
	if !n.PageCache {
		ps.PageCache = ""
	}

	return ps, nil
}

// StartImport copies the site's files, and the given databases, from the
// old server into the site of the same name here (ADR-0026). The remote
// user and document root are taken from the stored report, never from the
// request. Run it again for the final sync.
func (s *Service) StartImport(ctx context.Context, actor int64, reportID, name string, dbs []agentapi.CloudPanelDBMap) (agentapi.CloudPanelImport, error) {
	if s.importer == nil {
		return agentapi.CloudPanelImport{}, ErrNoImporter
	}

	ps, site, err := s.planSite(ctx, reportID, name)
	if err != nil {
		return agentapi.CloudPanelImport{}, err
	}

	// After CreateSite the plan skips the site because it exists here and
	// owns its domains; that is expected. Two sites of the report with the
	// same user leave the document root ambiguous.
	for _, r := range ps.Reasons {
		if r.Code == "user_twice" {
			return agentapi.CloudPanelImport{}, &PlanError{Err: ErrSkipped, Reasons: []plan.Reason{r}}
		}
	}

	req := agentapi.CloudPanelImportRequest{Site: site.User, RemoteUser: site.User, DocRoot: site.DocRoot, Databases: dbs}
	if err := req.Validate(); err != nil {
		return agentapi.CloudPanelImport{}, fmt.Errorf("%w: %w", ErrBadRequest, err)
	}

	imp, err := s.importer.Start(ctx, req)
	if err != nil {
		return agentapi.CloudPanelImport{}, err
	}

	s.audit(ctx, actor, "cloudpanel.import.start", site.User, map[string]string{
		"report": reportID, "import": imp.ID, "databases": strconv.Itoa(len(dbs)),
	})

	if s.loads != nil && len(dbs) > 0 {
		//nolint:contextcheck // the watch outlives the request and runs under the panel context
		s.watch(actor, imp.ID)
	}

	return imp, nil
}

// Import reads one import.
func (s *Service) Import(ctx context.Context, id string) (agentapi.CloudPanelImport, error) {
	if s.importer == nil {
		return agentapi.CloudPanelImport{}, ErrNoImporter
	}

	if !agentapi.ValidImportID(id) {
		return agentapi.CloudPanelImport{}, ErrNotFound
	}

	return s.importer.Get(ctx, id)
}

// Imports lists the kept imports newest first, of one site or of all.
func (s *Service) Imports(ctx context.Context, site string) ([]agentapi.CloudPanelImport, error) {
	if s.importer == nil {
		return nil, ErrNoImporter
	}

	if err := (agentapi.CloudPanelImportListRequest{Site: site}).Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrBadRequest, err)
	}

	return s.importer.List(ctx, site)
}

// Activate makes the imported files of a site its web root, keeping the
// previous one next to it. Do it after the final sync, before the DNS
// switch.
func (s *Service) Activate(ctx context.Context, actor int64, site string) (agentapi.CloudPanelActivated, error) {
	if s.importer == nil {
		return agentapi.CloudPanelActivated{}, ErrNoImporter
	}

	if err := (agentapi.CloudPanelActivateRequest{Site: site}).Validate(); err != nil {
		return agentapi.CloudPanelActivated{}, fmt.Errorf("%w: %w", ErrBadRequest, err)
	}

	r, err := s.importer.Activate(ctx, site)
	if err != nil {
		return agentapi.CloudPanelActivated{}, err
	}

	s.audit(ctx, actor, "cloudpanel.import.activate", site, map[string]string{"previous": r.Previous})

	return r, nil
}

// ReportSite returns the scanned site of a report by its name, for the
// snippet draft (ADR-0029). Like StartImport it refuses blocked sites and a
// user that appears twice in the report.
func (s *Service) ReportSite(ctx context.Context, reportID, name string) (scan.Site, error) {
	ps, site, err := s.planSite(ctx, reportID, name)
	if err != nil {
		return scan.Site{}, err
	}

	for _, r := range ps.Reasons {
		if r.Code == "user_twice" {
			return scan.Site{}, &PlanError{Err: ErrSkipped, Reasons: []plan.Reason{r}}
		}
	}

	return site, nil
}

// planSite finds one site of a report by its name, with its plan.
func (s *Service) planSite(ctx context.Context, reportID, name string) (plan.Site, scan.Site, error) {
	rep, _, err := s.load(ctx, reportID)
	if err != nil {
		return plan.Site{}, scan.Site{}, err
	}

	p, err := s.planFor(ctx, rep)
	if err != nil {
		return plan.Site{}, scan.Site{}, err
	}

	// Blocked sites may have no valid name; they never match one.
	for i, site := range rep.Sites {
		if site.Status != scan.StatusBlocked && site.User == name && p.Sites[i].Name == name {
			return p.Sites[i], site, nil
		}
	}

	return plan.Site{}, scan.Site{}, ErrNoSite
}
