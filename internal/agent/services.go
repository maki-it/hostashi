// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/hoststats"
	"github.com/maki-it/hostashi/internal/objcache"
	"github.com/maki-it/hostashi/internal/php"
	"github.com/maki-it/hostashi/internal/web"
)

// ServiceCtl reads and drives the catalog's units (phpctl.Services in
// production).
type ServiceCtl interface {
	Show(ctx context.Context, units []string) ([]agentapi.ServiceState, error)
	Action(ctx context.Context, unit, action string) error
	TestSSHD(ctx context.Context) error
}

// HostReader reads the server's counters (hoststats.Reader in production).
type HostReader interface {
	Read(mounts []hoststats.Mount) (agentapi.HostStats, error)
}

// serviceOps serves services.status, services.action and host.stats
// (ADR-0052).
type serviceOps struct {
	ctl  ServiceCtl
	host HostReader
	h    *siteOps // nil without the site manager: no per-site services
	web  *web.Reconciler
	log  *slog.Logger
}

var (
	errNoServices  = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "service management is not available on this server"}
	errNotInstall  = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the service is not installed"}
	errSiteNoPHP   = &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "the site has no PHP"}
	errSiteNoCache = &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "the site has no object cache"}
	errCachePaused = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the site is disabled; its object cache comes back with the site"}
	errNoWebGen    = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "nginx has no configuration from Hostashi yet"}
	errWebBusy     = &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "the web server configuration is being changed, try again"}
	errSSHDConfig  = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "sshd -t rejected the SSH server configuration, nothing was reloaded"}
	errNginxConfig = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "nginx -t rejected the current configuration, nginx was not touched"}
	errAction      = &agentrpc.Error{Code: agentrpc.CodeInternal, Message: "systemd did not complete the action, see the agent's log"}
)

func registerServices(s *agentrpc.Server, o *serviceOps) {
	agentrpc.Handle(s, agentapi.ServicesStatus, func(ctx context.Context, _ agentrpc.Caller, req agentapi.ServicesStatusRequest) (agentapi.ServicesStatusResponse, error) {
		if o.ctl == nil {
			return agentapi.ServicesStatusResponse{}, errNoServices
		}

		return o.status(ctx, req)
	})

	agentrpc.Handle(s, agentapi.ServiceAction, func(ctx context.Context, _ agentrpc.Caller, req agentapi.ServiceActionRequest) (agentapi.ServiceState, error) {
		if o.ctl == nil {
			return agentapi.ServiceState{}, errNoServices
		}

		return o.action(ctx, req)
	})

	agentrpc.Handle(s, agentapi.HostStatsOp, func(context.Context, agentrpc.Caller, agentapi.HostStatsRequest) (agentapi.HostStats, error) {
		if o.host == nil {
			return agentapi.HostStats{}, errNoServices
		}

		return o.host.Read(o.mounts())
	})
}

// mounts are "/" and every storage pool (ADR-0016).
func (o *serviceOps) mounts() []hoststats.Mount {
	out := []hoststats.Mount{{Name: "root", Path: "/"}}

	if o.h != nil {
		for _, p := range o.h.m.Pools() {
			out = append(out, hoststats.Mount{Name: p.Name, Path: p.Path})
		}
	}

	return out
}

// ref is one unit the status lists.
type ref struct {
	id, site, unit string
}

// siteUnits returns the PHP and Valkey units per site, sorted by site.
func (o *serviceOps) siteUnits(ctx context.Context) (phpUnits, cacheUnits []ref) {
	if o.h == nil {
		return nil, nil
	}

	if o.h.php != nil {
		pools, err := o.h.php.Current()
		if err != nil {
			o.log.WarnContext(ctx, "services: read php state", "err", err)
		}

		for site, p := range pools {
			phpUnits = append(phpUnits, ref{"php", site, php.Unit(p.Version, site)})
		}
	}

	if o.h.objc != nil {
		caches, err := o.h.objc.Sites()
		if err != nil {
			o.log.WarnContext(ctx, "services: read object cache state", "err", err)
		}

		for site := range caches {
			cacheUnits = append(cacheUnits, ref{"valkey", site, objcache.Unit(site)})
		}
	}

	bySite := func(a, b ref) int {
		if a.site < b.site {
			return -1
		}

		if a.site > b.site {
			return 1
		}

		return 0
	}
	slices.SortFunc(phpUnits, bySite)
	slices.SortFunc(cacheUnits, bySite)

	return phpUnits, cacheUnits
}

// resolve maps a catalog row and site to its unit, or an error a caller
// may see.
func (o *serviceOps) resolve(id, site string) (agentapi.Service, string, error) {
	svc, _ := agentapi.LookupService(id) // the request was validated
	if !svc.PerSite {
		return svc, svc.Unit, nil
	}

	if o.h == nil {
		return svc, "", errNoServices
	}

	switch id {
	case "php":
		if o.h.php == nil {
			return svc, "", errSiteNoPHP
		}

		pools, err := o.h.php.Current()
		if err != nil {
			return svc, "", err
		}

		p, ok := pools[site]
		if !ok {
			return svc, "", errSiteNoPHP
		}

		return svc, php.Unit(p.Version, site), nil
	case "valkey":
		if o.h.objc == nil {
			return svc, "", errSiteNoCache
		}

		caches, err := o.h.objc.Sites()
		if err != nil {
			return svc, "", err
		}

		c, ok := caches[site]
		if !ok {
			return svc, "", errSiteNoCache
		}

		if c.Paused {
			return svc, "", errCachePaused
		}

		return svc, objcache.Unit(site), nil
	}

	return svc, "", errNoServices
}

func (o *serviceOps) status(ctx context.Context, req agentapi.ServicesStatusRequest) (agentapi.ServicesStatusResponse, error) {
	var refs []ref

	if req.ID != "" {
		svc, unit, err := o.resolve(req.ID, req.Site)
		if err != nil && !errors.Is(err, errCachePaused) {
			return agentapi.ServicesStatusResponse{}, err
		}

		if errors.Is(err, errCachePaused) {
			unit = objcache.Unit(req.Site) // a paused cache still has a state
		}

		refs = []ref{{svc.ID, req.Site, unit}}
	} else {
		for _, svc := range agentapi.Services {
			if !svc.PerSite {
				refs = append(refs, ref{svc.ID, "", svc.Unit})
			}
		}

		p, c := o.siteUnits(ctx)

		refs = append(append(refs, p...), c...)
		if len(refs) > agentapi.MaxServiceStates {
			refs = refs[:agentapi.MaxServiceStates]
		}
	}

	out := agentapi.ServicesStatusResponse{Services: make([]agentapi.ServiceState, 0, len(refs))}

	for chunk := range slices.Chunk(refs, 64) {
		units := make([]string, len(chunk))
		for i, r := range chunk {
			units[i] = r.unit
		}

		states, err := o.ctl.Show(ctx, units)
		if err != nil {
			return agentapi.ServicesStatusResponse{}, err
		}

		for i, st := range states {
			st.ID, st.Site = chunk[i].id, chunk[i].site
			out.Services = append(out.Services, st)
		}
	}

	return out, nil
}

func (o *serviceOps) action(ctx context.Context, req agentapi.ServiceActionRequest) (agentapi.ServiceState, error) {
	svc, unit, err := o.resolve(req.ID, req.Site)
	if err != nil {
		return agentapi.ServiceState{}, err
	}

	cur, err := o.ctl.Show(ctx, []string{unit})
	if err != nil {
		return agentapi.ServiceState{}, err
	}

	if len(cur) != 1 || !cur[0].Installed {
		return agentapi.ServiceState{}, errNotInstall
	}

	if err := o.run(ctx, svc, unit, req.Action); err != nil {
		o.log.WarnContext(ctx, "service action failed", "service", svc.ID, "site", req.Site, "action", req.Action, "err", err)

		var rpcErr *agentrpc.Error
		if errors.As(err, &rpcErr) {
			return agentapi.ServiceState{}, rpcErr
		}

		return agentapi.ServiceState{}, o.actionError(ctx, unit)
	}

	o.log.InfoContext(ctx, "service action", "service", svc.ID, "site", req.Site, "action", req.Action)

	// The action ran; a state that cannot be read now is not its failure.
	st, err := o.ctl.Show(ctx, []string{unit})
	if err != nil || len(st) != 1 {
		o.log.WarnContext(ctx, "services: read state after action", "service", svc.ID, "err", err)

		return agentapi.ServiceState{ID: svc.ID, Site: req.Site, Unit: unit, Installed: true}, nil
	}

	st[0].ID, st[0].Site = svc.ID, req.Site

	return st[0], nil
}

// actionError says why an action failed: for a unit that systemd now
// shows as failed, its result and where its log is, since systemctl only
// says the job failed. Unit and result come from fixed, checked values.
func (o *serviceOps) actionError(ctx context.Context, unit string) error {
	st, err := o.ctl.Show(ctx, []string{unit})
	if err != nil || len(st) != 1 || st[0].Active != "failed" {
		return errAction
	}

	why := "it failed"
	if st[0].Result != "" && st[0].Result != "success" {
		why = "it failed with result " + st[0].Result
	}

	return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: fmt.Sprintf("%s did not start: %s. Its log: journalctl -u %s", unit, why, unit)}
}

func (o *serviceOps) run(ctx context.Context, svc agentapi.Service, unit, action string) error {
	switch svc.ID {
	case "nginx":
		// Every nginx action goes through the reconciler: its lock keeps
		// it apart from web.apply and nginx -t checks the live generation.
		if o.web == nil {
			return errNoServices
		}

		err := o.web.Server(ctx, action != agentapi.ServiceReload)

		switch {
		case errors.Is(err, web.ErrNoGeneration):
			return errNoWebGen
		case errors.Is(err, web.ErrBusy):
			return errWebBusy
		case errors.Is(err, web.ErrConfigRejected):
			o.log.WarnContext(ctx, "nginx -t failed", "err", err)

			return errNginxConfig
		}

		return err
	case "ssh":
		if err := o.ctl.TestSSHD(ctx); err != nil {
			o.log.WarnContext(ctx, "sshd -t failed", "err", err)

			return errSSHDConfig
		}
	case "php", "valkey":
		// The same lock as web.apply and site.delete, so a site's units
		// are not restarted while they are rebuilt or removed.
		o.h.mu.Lock()
		defer o.h.mu.Unlock()
	}

	return o.ctl.Action(ctx, unit, action)
}
