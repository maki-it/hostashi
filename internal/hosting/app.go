// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of Node.js apps.
var (
	// ErrNodeMissing: the app's Node.js line is not installed on the server.
	ErrNodeMissing = errors.New("hosting: this Node.js version is not installed; install it under Node.js first")
	// ErrNoApps: this panel runs without an agent for apps.
	ErrNoApps = errors.New("hosting: Node.js apps are not available")
)

// AppAgent is the part of the agent Node.js sites use (ADR-0054).
type AppAgent interface {
	NodeStatus(ctx context.Context) (agentapi.NodeStatus, error)
	AppApply(ctx context.Context, a agentapi.AppSpec) (agentapi.AppStatus, error)
	AppRemove(ctx context.Context, site string) error
	AppAction(ctx context.Context, req agentapi.AppActionRequest) (agentapi.AppStatus, error)
	AppStatus(ctx context.Context, site string) (agentapi.AppStatus, error)
}

// NodeStatus calls node.status.
func (a RPCAgent) NodeStatus(ctx context.Context) (agentapi.NodeStatus, error) {
	return agentapi.NodeStatusOp.Call(ctx, a.C, agentapi.NodeStatusRequest{})
}

// AppApply calls site.app.apply.
func (a RPCAgent) AppApply(ctx context.Context, spec agentapi.AppSpec) (agentapi.AppStatus, error) {
	return agentapi.AppApply.Call(ctx, a.C, agentapi.AppApplyRequest{App: spec})
}

// AppRemove calls site.app.remove.
func (a RPCAgent) AppRemove(ctx context.Context, site string) error {
	_, err := agentapi.AppRemove.Call(ctx, a.C, agentapi.AppRemoveRequest{Site: site})

	return err
}

// AppAction calls site.app.action.
func (a RPCAgent) AppAction(ctx context.Context, req agentapi.AppActionRequest) (agentapi.AppStatus, error) {
	return agentapi.AppAction.Call(ctx, a.C, req)
}

// AppStatus calls site.app.status.
func (a RPCAgent) AppStatus(ctx context.Context, site string) (agentapi.AppStatus, error) {
	return agentapi.AppStatusOp.Call(ctx, a.C, agentapi.AppStatusRequest{Site: site})
}

// EnableApps sets the agent for Node.js apps. Without it Node.js sites
// cannot be created and app calls answer ErrNoApps.
func (s *Service) EnableApps(a AppAgent) { s.apps = a }

// hasApp reports whether a site runs an app: a Node.js or an app site
// (ADR-0054).
func hasApp(site store.Site) bool {
	return (site.Type == store.SiteTypeNode || site.Type == store.SiteTypeApp) && site.App != nil
}

// checkApp normalizes and checks the app of a Node.js site, or with binary
// of an app site (ADR-0054): an empty working directory is htdocs, the
// port is the store's.
func checkApp(a store.SiteApp, binary bool) (store.SiteApp, error) {
	if a.Workdir == "" {
		a.Workdir = agentapi.DefaultAppWorkdir
	}

	if len(a.Args) == 0 {
		a.Args = nil
	}

	if binary {
		if a.Node != "" {
			return store.SiteApp{}, fmt.Errorf("%w: an app site runs no Node.js", ErrInvalid)
		}

		if a.StartKind != "" && a.StartKind != agentapi.AppStartBinary {
			return store.SiteApp{}, fmt.Errorf("%w: an app site starts a program (startKind binary)", ErrInvalid)
		}

		if a.StartKind == "" && len(a.Args) > 0 {
			return store.SiteApp{}, fmt.Errorf("%w: arguments need a program", ErrInvalid)
		}

		if err := agentapi.ValidateAppArgs(a.Args); err != nil {
			return store.SiteApp{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	} else {
		if err := agentapi.ValidateNodeMajor(a.Node); err != nil {
			return store.SiteApp{}, fmt.Errorf("%w: node: %w", ErrInvalid, err)
		}

		if a.StartKind == agentapi.AppStartBinary || len(a.Args) > 0 {
			return store.SiteApp{}, fmt.Errorf("%w: a Node.js site starts a script or an npm script, without arguments", ErrInvalid)
		}
	}

	if err := agentapi.ValidateAppWorkdir(a.Workdir); err != nil {
		return store.SiteApp{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := agentapi.ValidateAppStart(a.StartKind, a.Start); err != nil {
		return store.SiteApp{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return a, nil
}

// NodeLines lists the Node.js lines installed here, none without apps.
func (s *Service) NodeLines(ctx context.Context) ([]string, error) {
	if s.apps == nil {
		return nil, nil
	}

	st, err := s.apps.NodeStatus(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(st.Installed))
	for _, i := range st.Installed {
		out = append(out, i.Major)
	}

	return out, nil
}

// checkNodeInstalled asks the agent whether the app's line is installed.
func (s *Service) checkNodeInstalled(ctx context.Context, major string) error {
	if s.apps == nil {
		return ErrNoApps
	}

	st, err := s.apps.NodeStatus(ctx)
	if err != nil {
		return err
	}

	if !slices.ContainsFunc(st.Installed, func(i agentapi.NodeInstalled) bool { return i.Major == major }) {
		return fmt.Errorf("%w (Node.js %s)", ErrNodeMissing, major)
	}

	return nil
}

// appSpec is what the agent runs for a site. The app stays stopped without
// a start command, when it was stopped in the panel, and while the site is
// disabled.
func appSpec(site store.Site) agentapi.AppSpec {
	a := site.App

	return agentapi.AppSpec{
		Site: site.Name, Node: a.Node, Workdir: a.Workdir, StartKind: a.StartKind, Start: a.Start, Args: a.Args, Port: a.Port,
		Stopped: a.Stopped || a.StartKind == "" || site.Disabled,
	}
}

// applyApp sets up the app of a Node.js or app site on the agent as it is
// stored.
func (s *Service) applyApp(ctx context.Context, site store.Site) (agentapi.AppStatus, error) {
	if !hasApp(site) {
		return agentapi.AppStatus{}, ErrSiteType
	}

	if s.apps == nil {
		return agentapi.AppStatus{}, ErrNoApps
	}

	st, err := s.apps.AppApply(ctx, appSpec(site))
	if site.Type == store.SiteTypeNode && isCode(err, agentrpc.CodePrecondition) {
		return agentapi.AppStatus{}, fmt.Errorf("%w: %w", ErrNodeMissing, err)
	}

	return st, err
}

// syncApps sets up the apps of all active Node.js and app sites again,
// and removes those of the other sites with an account, for Sync.
// Failures are logged; the next change of the site retries.
func (s *Service) syncApps(ctx context.Context) {
	if s.apps == nil {
		return
	}

	all, err := s.st.Sites(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "list sites for their apps", "err", err)

		return
	}

	for _, site := range all {
		if site.State != store.SiteActive || !site.Account {
			continue
		}

		// The app of a former Node.js or app site, if a type change
		// could not remove it (ADR-0054 addendum 3); nothing for others.
		if !hasApp(site) {
			if err := s.apps.AppRemove(ctx, site.Name); err != nil {
				s.log.ErrorContext(ctx, "remove app", "site", site.Name, "err", err)
			}

			continue
		}

		if _, err := s.applyApp(ctx, site); err != nil {
			s.log.ErrorContext(ctx, "apply app", "site", site.Name, "err", err)
		}
	}
}

// SetApp changes the app of an active Node.js or app site: version,
// working directory, start command, arguments (ADR-0054). The running app
// restarts with it. If the agent refuses, the previous app is stored
// again.
func (s *Service) SetApp(ctx context.Context, actor int64, name string, a store.SiteApp) (store.Site, agentapi.AppStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, agentapi.AppStatus{}, err
	}

	switch {
	case site.State != store.SiteActive:
		return store.Site{}, agentapi.AppStatus{}, ErrState
	case !hasApp(site):
		return store.Site{}, agentapi.AppStatus{}, ErrSiteType
	}

	a, err = checkApp(a, site.Type == store.SiteTypeApp)
	if err != nil {
		return store.Site{}, agentapi.AppStatus{}, err
	}

	if site.Type == store.SiteTypeNode && a.Node != site.App.Node {
		if err := s.checkNodeInstalled(ctx, a.Node); err != nil {
			return store.Site{}, agentapi.AppStatus{}, err
		}
	}

	a.Stopped, a.Port = site.App.Stopped, site.App.Port

	return s.changeApp(ctx, actor, site, a)
}

// changeApp stores a and applies it; called with s.mu held.
func (s *Service) changeApp(ctx context.Context, actor int64, site store.Site, a store.SiteApp) (store.Site, agentapi.AppStatus, error) {
	old, err := s.st.SetSiteApp(ctx, site.Name, a, s.now())
	if err != nil {
		return store.Site{}, agentapi.AppStatus{}, err
	}

	next := site
	next.App = &a

	st, err := s.applyApp(ctx, next)
	if err != nil {
		undo := context.WithoutCancel(ctx)
		if _, rerr := s.st.SetSiteApp(undo, site.Name, old, s.now()); rerr != nil {
			s.log.ErrorContext(ctx, "restore app after failed change", "site", site.Name, "err", rerr)
		} else if _, rerr := s.applyApp(undo, site); rerr != nil {
			s.log.ErrorContext(ctx, "apply previous app after failed change", "site", site.Name, "err", rerr)
		}

		return store.Site{}, agentapi.AppStatus{}, err
	}

	s.auditChange(ctx, actor, "site.app", site.Name, nil, appAudit(old), appAudit(a))

	out, err := s.Get(ctx, site.Name)

	return out, st, err
}

// AppAction starts, stops or restarts the app of an active Node.js or app
// site.
// Restart of a stopped app starts it.
// Start and stop last: a stopped app stays stopped after a reboot, until
// it is started again.
func (s *Service) AppAction(ctx context.Context, actor int64, name, action string) (agentapi.AppStatus, error) {
	if err := (agentapi.AppActionRequest{Site: name, Action: action}).Validate(); err != nil {
		return agentapi.AppStatus{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if s.apps == nil {
		return agentapi.AppStatus{}, ErrNoApps
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	switch {
	case site.State != store.SiteActive:
		return agentapi.AppStatus{}, ErrState
	case !hasApp(site):
		return agentapi.AppStatus{}, ErrSiteType
	case action != agentapi.AppActionStop && site.App.StartKind == "":
		return agentapi.AppStatus{}, fmt.Errorf("%w: set the start command first", ErrInvalid)
	case action != agentapi.AppActionStop && site.Disabled:
		return agentapi.AppStatus{}, fmt.Errorf("%w: the site is disabled", ErrState)
	}

	if want := action == agentapi.AppActionStop; want != site.App.Stopped {
		a := *site.App
		a.Stopped = want

		_, st, err := s.changeApp(ctx, actor, site, a)

		return st, err
	}

	st, err := s.apps.AppAction(ctx, agentapi.AppActionRequest{Site: name, Action: action})
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	s.audit(ctx, actor, "site.app."+action, name, nil)

	return st, nil
}

// AppStatus reports the app of a Node.js or app site.
func (s *Service) AppStatus(ctx context.Context, name string) (agentapi.AppStatus, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return agentapi.AppStatus{}, err
	}

	if !hasApp(site) {
		return agentapi.AppStatus{}, ErrSiteType
	}

	if site.State != store.SiteActive {
		return agentapi.AppStatus{Site: name}, nil
	}

	if s.apps == nil {
		return agentapi.AppStatus{}, ErrNoApps
	}

	return s.apps.AppStatus(ctx, name)
}

func appAudit(a store.SiteApp) map[string]string {
	out := map[string]string{
		"node": a.Node, "workdir": a.Workdir, "startKind": a.StartKind, "start": a.Start,
		"port": strconv.Itoa(a.Port), "stopped": strconv.FormatBool(a.Stopped),
	}

	if len(a.Args) > 0 {
		b, _ := json.Marshal(a.Args)
		out["args"] = string(b)
	}

	return out
}
