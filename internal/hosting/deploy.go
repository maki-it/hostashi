// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Deploy runs of a site (ADR-0062). A deploy is a row in site_deploys and
// a run of the site's deploy unit: the panel queues the row, the agent
// starts the unit, and RunDeploys follows every active row until the agent
// reports its end. A successful run goes live: the deploy directory links
// to its release, the app restarts, the page cache empties and old
// releases go. A rollback is a switch to a kept release without a build.

// Errors of deploys.
var (
	// ErrNoDeploys: this panel runs without an agent for deploys.
	ErrNoDeploys = errors.New("hosting: Git deploys are not available")
	// ErrDeployBusy: a deploy of the site is queued or running.
	ErrDeployBusy = store.ErrDeployBusy
	// ErrHostKeyUnconfirmed: an SSH repository whose host key is not
	// confirmed yet.
	ErrHostKeyUnconfirmed = errors.New("hosting: confirm the host key of the repository first")
	// ErrDeployNotFound: the site has no run with this id.
	ErrDeployNotFound = errors.New("hosting: no such deploy")
	// ErrNotDeployable: a rollback target that is no successful deploy.
	ErrNotDeployable = errors.New("hosting: only a successful deploy can be rolled back to")
)

// DeployAgent is the part of the agent deploys use.
type DeployAgent interface {
	DeployStart(ctx context.Context, req agentapi.DeployStartRequest) (agentapi.DeployStatus, error)
	DeployStatus(ctx context.Context, req agentapi.DeployStatusRequest) (agentapi.DeployStatus, error)
	DeploySwitch(ctx context.Context, req agentapi.DeploySwitchRequest) (agentapi.DeploySwitchResult, error)
	DeployPrune(ctx context.Context, req agentapi.DeployPruneRequest) (agentapi.DeployReleases, error)
	DeployLog(ctx context.Context, req agentapi.DeployStatusRequest) (agentapi.DeployLog, error)
}

// DeployStart calls site.deploy.start.
func (a RPCAgent) DeployStart(ctx context.Context, req agentapi.DeployStartRequest) (agentapi.DeployStatus, error) {
	return agentapi.DeployStart.Call(ctx, a.C, req)
}

// DeployStatus calls site.deploy.status.
func (a RPCAgent) DeployStatus(ctx context.Context, req agentapi.DeployStatusRequest) (agentapi.DeployStatus, error) {
	return agentapi.DeployStatusOp.Call(ctx, a.C, req)
}

// DeploySwitch calls site.deploy.switch.
func (a RPCAgent) DeploySwitch(ctx context.Context, req agentapi.DeploySwitchRequest) (agentapi.DeploySwitchResult, error) {
	return agentapi.DeploySwitch.Call(ctx, a.C, req)
}

// DeployPrune calls site.deploy.prune.
func (a RPCAgent) DeployPrune(ctx context.Context, req agentapi.DeployPruneRequest) (agentapi.DeployReleases, error) {
	return agentapi.DeployPrune.Call(ctx, a.C, req)
}

// DeployLog calls site.deploy.log.
func (a RPCAgent) DeployLog(ctx context.Context, req agentapi.DeployStatusRequest) (agentapi.DeployLog, error) {
	return agentapi.DeployLogOp.Call(ctx, a.C, req)
}

// EnableDeploys sets the agent for deploy runs. Without it a deploy
// answers ErrNoDeploys.
func (s *Service) EnableDeploys(a DeployAgent) {
	s.deploys = a

	if s.deployKick == nil {
		s.deployKick = make(chan struct{}, 1)
	}
}

// deployPoll is how often RunDeploys asks the agent about active runs;
// queuedGrace how long a row may stay queued.
const (
	deployPoll    = 3 * time.Second
	queuedGrace   = 2 * time.Minute
	rollbackGrace = 5 * time.Minute
)

// Deploys lists the runs of a site, newest first.
func (s *Service) Deploys(ctx context.Context, name string, limit int) ([]store.SiteDeploy, error) {
	site, err := s.gitSite(ctx, name)
	if err != nil {
		return nil, err
	}

	return s.st.SiteDeploys(ctx, site.ID, limit)
}

// Deploy starts a deploy of the site's branch head. It returns once the
// agent has queued the run; RunDeploys follows it.
func (s *Service) Deploy(ctx context.Context, actor int64, name, source string) (store.SiteDeploy, error) {
	if s.deploys == nil || s.gitBox == nil {
		return store.SiteDeploy{}, ErrNoDeploys
	}

	site, err := s.gitSite(ctx, name)
	if err != nil {
		return store.SiteDeploy{}, err
	}

	if site.State != store.SiteActive {
		return store.SiteDeploy{}, ErrState
	}

	g, err := s.st.SiteGit(ctx, site.ID)
	if errors.Is(err, store.ErrNotFound) {
		return store.SiteDeploy{}, ErrGitNotConfigured
	}

	if err != nil {
		return store.SiteDeploy{}, err
	}

	if g.Auth == agentapi.GitAuthSSH && g.HostKey == "" {
		return store.SiteDeploy{}, ErrHostKeyUnconfirmed
	}

	req := agentapi.DeployStartRequest{
		Site: site.Name, URL: g.URL, Branch: g.Branch, Auth: g.Auth, TokenUser: g.TokenUser, HostKey: g.HostKey,
		Shared: g.Shared, Commands: g.Commands, TimeoutSeconds: int(g.BuildTimeout / time.Second),
	}

	if site.App != nil {
		req.Node, req.Python = site.App.Node, site.App.Python
	}

	switch g.Auth {
	case agentapi.GitAuthToken:
		b, err := s.gitBox.Open(g.Token, gitContext(site.ID, "token"))
		if err != nil {
			return store.SiteDeploy{}, fmt.Errorf("hosting: token of site %s: %w", site.Name, err)
		}

		req.Token = string(b)
	case agentapi.GitAuthSSH:
		b, err := s.gitBox.Open(g.SSHKey, gitContext(site.ID, "key"))
		if err != nil {
			return store.SiteDeploy{}, fmt.Errorf("hosting: deploy key of site %s: %w", site.Name, err)
		}

		req.Key = string(b)
	}

	d, err := s.st.CreateSiteDeploy(ctx, store.SiteDeploy{SiteID: site.ID, Kind: store.DeployKindDeploy, Source: source, Actor: actor}, s.now())
	if err != nil {
		return store.SiteDeploy{}, err
	}

	req.ID = d.ID

	if _, err := s.deploys.DeployStart(ctx, req); err != nil {
		d.State, d.Error = store.DeployFailed, err.Error()
		if ferr := s.st.FinishSiteDeploy(context.WithoutCancel(ctx), d, s.now()); ferr != nil {
			s.log.ErrorContext(ctx, "mark deploy failed", "site", site.Name, "id", d.ID, "err", ferr)
		}

		return store.SiteDeploy{}, err
	}

	if err := s.st.StartSiteDeploy(ctx, d.ID, s.now()); err != nil {
		return store.SiteDeploy{}, err
	}

	s.audit(ctx, actor, "site.deploy", site.Name, map[string]string{"id": strconv.FormatInt(d.ID, 10), "branch": g.Branch, "source": source})
	s.kickDeploys()

	return s.st.SiteDeploy(ctx, site.ID, d.ID)
}

func (s *Service) kickDeploys() {
	select {
	case s.deployKick <- struct{}{}:
	default:
	}
}

// RunDeploys follows the active runs until ctx ends: on start (a panel
// restart picks up where it stopped), every deployPoll while runs are
// active and when a deploy starts.
func (s *Service) RunDeploys(ctx context.Context) {
	if s.deploys == nil {
		return
	}

	t := time.NewTicker(deployPoll)
	defer t.Stop()

	for {
		s.pollDeploys(ctx)

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.deployKick:
		}
	}
}

// pollDeploys asks the agent about every active run once.
func (s *Service) pollDeploys(ctx context.Context) {
	active, err := s.st.ActiveSiteDeploys(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "list active deploys", "err", err)

		return
	}

	if len(active) == 0 {
		return
	}

	all, err := s.st.Sites(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "list sites for deploys", "err", err)

		return
	}

	sites := map[int64]store.Site{}
	for _, site := range all {
		sites[site.ID] = site
	}

	for _, d := range active {
		if ctx.Err() != nil {
			return
		}

		site, ok := sites[d.SiteID]
		if !ok {
			s.finishDeploy(ctx, d, store.DeployFailed, "the site is gone")

			continue
		}

		if d.Kind != store.DeployKindDeploy {
			// A rollback runs within its request and takes seconds; one
			// still active after rollbackGrace was cut short by a restart.
			if s.now().Sub(d.Created) > rollbackGrace {
				s.finishDeploy(ctx, d, store.DeployFailed, "the panel stopped during the rollback")
			}

			continue
		}

		s.pollDeploy(ctx, site, d)
	}
}

func (s *Service) pollDeploy(ctx context.Context, site store.Site, d store.SiteDeploy) {
	if d.State == store.DeployQueued {
		// The panel stopped between the row and the agent's start.
		if s.now().Sub(d.Created) > queuedGrace {
			s.finishDeploy(ctx, d, store.DeployFailed, "the deploy did not start")
		}

		return
	}

	st, err := s.deploys.DeployStatus(ctx, agentapi.DeployStatusRequest{Site: site.Name, ID: d.ID})
	if err != nil {
		s.log.WarnContext(ctx, "deploy status", "site", site.Name, "id", d.ID, "err", err)

		return
	}

	switch st.State {
	case agentapi.DeployRunning:
		return
	case agentapi.DeployUnknown:
		s.finishDeploy(ctx, d, store.DeployFailed, "the server lost track of this deploy")

		return
	case agentapi.DeployFailed:
		d.Commit, d.Author, d.Message = st.Commit, st.Author, st.Message
		s.finishDeploy(ctx, d, store.DeployFailed, st.Error)

		return
	}

	d.Commit, d.Author, d.Message = st.Commit, st.Author, st.Message

	g, err := s.st.SiteGit(ctx, site.ID)
	if err != nil {
		s.finishDeploy(ctx, d, store.DeployFailed, "the Git setting of the site is gone")

		return
	}

	warn, err := s.goLive(ctx, site, g.DeployDir, d.Release)
	if err != nil {
		s.finishDeploy(ctx, d, store.DeployFailed, "the build succeeded, but the release could not go live: "+err.Error())

		return
	}

	s.finishDeploy(ctx, d, store.DeploySucceeded, warn)
	s.pruneReleases(ctx, site, g)
}

// goLive switches the deploy directory to a release and restarts the app
// or empties the page cache. A failed restart or purge is a warning: the
// release is live.
func (s *Service) goLive(ctx context.Context, site store.Site, dir, release string) (string, error) {
	if _, err := s.deploys.DeploySwitch(ctx, agentapi.DeploySwitchRequest{Site: site.Name, Release: release, Dir: dir}); err != nil {
		return "", err
	}

	var warn string

	if hasApp(site) && !site.App.Stopped && site.App.StartKind != "" && !site.Disabled && s.apps != nil {
		if _, err := s.apps.AppAction(ctx, agentapi.AppActionRequest{Site: site.Name, Action: agentapi.AppActionRestart}); err != nil {
			warn = "live, but the app did not restart: " + err.Error()
		}
	}

	if c, err := s.st.SitePageCache(ctx, site.ID); err == nil && c.Enabled {
		if err := s.ag.PageCachePurge(ctx, site.Name); err != nil && warn == "" {
			warn = "live, but the page cache was not emptied: " + err.Error()
		}
	}

	return warn, nil
}

// pruneReleases keeps the releases of the newest successful deploys, the
// files from before the first deploy and the live one.
func (s *Service) pruneReleases(ctx context.Context, site store.Site, g store.SiteGit) {
	keep, err := s.st.KeptReleases(ctx, site.ID, g.Keep)
	if err != nil {
		s.log.ErrorContext(ctx, "kept releases", "site", site.Name, "err", err)

		return
	}

	keep = append(keep, agentapi.InitialRelease)

	if _, err := s.deploys.DeployPrune(ctx, agentapi.DeployPruneRequest{Site: site.Name, Dir: g.DeployDir, Keep: keep}); err != nil {
		s.log.WarnContext(ctx, "prune releases", "site", site.Name, "err", err)
	}
}

func (s *Service) finishDeploy(ctx context.Context, d store.SiteDeploy, state, msg string) {
	d.State, d.Error = state, msg
	if err := s.st.FinishSiteDeploy(context.WithoutCancel(ctx), d, s.now()); err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.ErrorContext(ctx, "finish deploy", "id", d.ID, "err", err)

		return
	}

	s.log.InfoContext(ctx, "deploy finished", "id", d.ID, "kind", d.Kind, "state", state, "commit", d.Commit, "error", msg)
}

// Rollback makes the release of an earlier successful deploy live again,
// without a build. It is a run of its own in the history.
func (s *Service) Rollback(ctx context.Context, actor int64, name string, id int64, source string) (store.SiteDeploy, error) {
	if s.deploys == nil {
		return store.SiteDeploy{}, ErrNoDeploys
	}

	site, err := s.gitSite(ctx, name)
	if err != nil {
		return store.SiteDeploy{}, err
	}

	if site.State != store.SiteActive {
		return store.SiteDeploy{}, ErrState
	}

	g, err := s.st.SiteGit(ctx, site.ID)
	if errors.Is(err, store.ErrNotFound) {
		return store.SiteDeploy{}, ErrGitNotConfigured
	}

	if err != nil {
		return store.SiteDeploy{}, err
	}

	target, err := s.st.SiteDeploy(ctx, site.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.SiteDeploy{}, ErrDeployNotFound
	}

	if err != nil {
		return store.SiteDeploy{}, err
	}

	if target.Kind != store.DeployKindDeploy || target.State != store.DeploySucceeded {
		return store.SiteDeploy{}, ErrNotDeployable
	}

	d, err := s.st.CreateSiteDeploy(ctx, store.SiteDeploy{
		SiteID: site.ID, Kind: store.DeployKindRollback, Source: source, Actor: actor, Release: target.Release,
		Commit: target.Commit, Author: target.Author, Message: target.Message,
	}, s.now())
	if err != nil {
		return store.SiteDeploy{}, err
	}

	if err := s.st.StartSiteDeploy(ctx, d.ID, s.now()); err != nil {
		return store.SiteDeploy{}, err
	}

	warn, err := s.goLive(ctx, site, g.DeployDir, target.Release)
	if err != nil {
		s.finishDeploy(ctx, d, store.DeployFailed, err.Error())

		return store.SiteDeploy{}, err
	}

	s.finishDeploy(ctx, d, store.DeploySucceeded, warn)
	s.audit(ctx, actor, "site.deploy.rollback", site.Name, map[string]string{
		"id": strconv.FormatInt(d.ID, 10), "to": strconv.FormatInt(target.ID, 10), "commit": target.Commit,
	})

	return s.st.SiteDeploy(ctx, site.ID, d.ID)
}

// DeployLog returns the log of a run of a site.
func (s *Service) DeployLog(ctx context.Context, name string, id int64) (agentapi.DeployLog, error) {
	if s.deploys == nil {
		return agentapi.DeployLog{}, ErrNoDeploys
	}

	site, err := s.gitSite(ctx, name)
	if err != nil {
		return agentapi.DeployLog{}, err
	}

	d, err := s.st.SiteDeploy(ctx, site.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return agentapi.DeployLog{}, ErrDeployNotFound
	}

	if err != nil {
		return agentapi.DeployLog{}, err
	}

	if d.Kind != store.DeployKindDeploy {
		// A rollback has no build and no log.
		return agentapi.DeployLog{}, nil
	}

	return s.deploys.DeployLog(ctx, agentapi.DeployStatusRequest{Site: site.Name, ID: d.ID})
}
