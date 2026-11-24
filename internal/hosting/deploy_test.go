// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

type fakeDeploys struct {
	mu       sync.Mutex
	started  []agentapi.DeployStartRequest
	state    map[int64]agentapi.DeployStatus
	switched []agentapi.DeploySwitchRequest
	pruned   []agentapi.DeployPruneRequest
	startErr error
	swErr    error
}

func (f *fakeDeploys) DeployStart(_ context.Context, req agentapi.DeployStartRequest) (agentapi.DeployStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.startErr != nil {
		return agentapi.DeployStatus{}, f.startErr
	}

	f.started = append(f.started, req)

	return agentapi.DeployStatus{Site: req.Site, ID: req.ID, State: agentapi.DeployRunning}, nil
}

func (f *fakeDeploys) DeployStatus(_ context.Context, req agentapi.DeployStatusRequest) (agentapi.DeployStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if st, ok := f.state[req.ID]; ok {
		return st, nil
	}

	return agentapi.DeployStatus{Site: req.Site, ID: req.ID, State: agentapi.DeployRunning}, nil
}

func (f *fakeDeploys) DeploySwitch(_ context.Context, req agentapi.DeploySwitchRequest) (agentapi.DeploySwitchResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.swErr != nil {
		return agentapi.DeploySwitchResult{}, f.swErr
	}

	f.switched = append(f.switched, req)

	return agentapi.DeploySwitchResult{}, nil
}

func (f *fakeDeploys) DeployPrune(_ context.Context, req agentapi.DeployPruneRequest) (agentapi.DeployReleases, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.pruned = append(f.pruned, req)

	return agentapi.DeployReleases{}, nil
}

func (f *fakeDeploys) DeployLog(_ context.Context, req agentapi.DeployStatusRequest) (agentapi.DeployLog, error) {
	return agentapi.DeployLog{Log: "log of " + req.Site}, nil
}

func (f *fakeDeploys) finish(id int64, st agentapi.DeployStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.state == nil {
		f.state = map[int64]agentapi.DeployStatus{}
	}

	f.state[id] = st
}

func deployService(t *testing.T) (*Service, *fakeDeploys, *fakeAgent) {
	t.Helper()

	s, ag, _ := newService(t)
	ctx := t.Context()

	box, err := secrets.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}

	s.EnableGit(box)

	f := &fakeDeploys{}
	s.EnableDeploys(f)

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	return s, f, ag
}

func TestDeploy(t *testing.T) {
	s, f, _ := deployService(t)
	ctx := t.Context()

	if _, err := s.Deploy(ctx, 1, "blog", store.DeploySourcePanel); !errors.Is(err, ErrGitNotConfigured) {
		t.Fatalf("without git: %v", err)
	}

	in := gitInput("https://git.example.com/acme/blog.git", agentapi.GitAuthToken)
	in.Token, in.Commands, in.Shared, in.Keep = "s3cret", []string{"composer install"}, []string{".env"}, 2

	if _, err := s.SetGit(ctx, 1, "blog", in); err != nil {
		t.Fatal(err)
	}

	d, err := s.Deploy(ctx, 1, "blog", store.DeploySourceAPI)
	if err != nil {
		t.Fatal(err)
	}

	if d.State != store.DeployRunning || d.Release != agentapi.ReleaseName(d.ID) || len(f.started) != 1 {
		t.Fatalf("deploy %+v, started %d", d, len(f.started))
	}

	req := f.started[0]
	if req.Token != "s3cret" || req.ID != d.ID || req.Branch != "main" || !slices.Equal(req.Commands, in.Commands) || req.TimeoutSeconds != 900 {
		t.Fatalf("start request %+v", req)
	}

	if _, err := s.Deploy(ctx, 1, "blog", store.DeploySourcePanel); !errors.Is(err, ErrDeployBusy) {
		t.Fatalf("second deploy: %v", err)
	}

	// Still running: nothing changes.
	s.pollDeploys(ctx)

	if len(f.switched) != 0 {
		t.Fatal("switched while running")
	}

	sha := strings.Repeat("ab", 20)
	f.finish(d.ID, agentapi.DeployStatus{State: agentapi.DeploySucceeded, Commit: sha, Author: "Kim", Message: "First"})
	s.pollDeploys(ctx)

	if len(f.switched) != 1 || f.switched[0].Release != d.Release || f.switched[0].Dir != "htdocs" {
		t.Fatalf("switched %+v", f.switched)
	}

	if len(f.pruned) != 1 || !slices.Equal(f.pruned[0].Keep, []string{d.Release, agentapi.InitialRelease}) {
		t.Fatalf("pruned %+v", f.pruned)
	}

	list, err := s.Deploys(ctx, "blog", 10)
	if err != nil || len(list) != 1 || list[0].State != store.DeploySucceeded || list[0].Commit != sha || list[0].Message != "First" {
		t.Fatalf("history %+v %v", list, err)
	}

	// A failed build never goes live.
	d2, err := s.Deploy(ctx, 1, "blog", store.DeploySourcePanel)
	if err != nil {
		t.Fatal(err)
	}

	f.finish(d2.ID, agentapi.DeployStatus{State: agentapi.DeployFailed, Error: "a step of the deploy failed"})
	s.pollDeploys(ctx)

	if len(f.switched) != 1 {
		t.Fatal("failed deploy switched")
	}

	if list, _ := s.Deploys(ctx, "blog", 10); list[0].State != store.DeployFailed || list[0].Error != "a step of the deploy failed" {
		t.Fatalf("failed %+v", list[0])
	}

	// Rollback to the first; the failed one cannot be a target.
	if _, err := s.Rollback(ctx, 1, "blog", d2.ID, store.DeploySourcePanel); !errors.Is(err, ErrNotDeployable) {
		t.Fatalf("rollback to failed: %v", err)
	}

	r, err := s.Rollback(ctx, 1, "blog", d.ID, store.DeploySourcePanel)
	if err != nil || r.State != store.DeploySucceeded || r.Kind != store.DeployKindRollback || r.Release != d.Release || r.Commit != sha {
		t.Fatalf("rollback %+v %v", r, err)
	}

	if l, err := s.DeployLog(ctx, "blog", d.ID); err != nil || l.Log != "log of blog" {
		t.Fatalf("log %+v %v", l, err)
	}

	if l, err := s.DeployLog(ctx, "blog", r.ID); err != nil || l.Log != "" {
		t.Fatalf("rollback log %+v %v", l, err)
	}

	// A switch that fails marks the rollback failed.
	f.swErr = errors.New("release gone")

	if _, err := s.Rollback(ctx, 1, "blog", d.ID, store.DeploySourcePanel); err == nil {
		t.Fatal("failed switch accepted")
	}

	if list, _ := s.Deploys(ctx, "blog", 1); list[0].State != store.DeployFailed || list[0].Error != "release gone" {
		t.Fatalf("failed rollback %+v", list[0])
	}
}

func TestDeployFailures(t *testing.T) {
	s, f, _ := deployService(t)
	ctx := t.Context()

	// SSH needs the confirmed host key.
	if _, err := s.SetGit(ctx, 1, "blog", gitInput("git@github.com:acme/blog.git", agentapi.GitAuthSSH)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Deploy(ctx, 1, "blog", store.DeploySourcePanel); !errors.Is(err, ErrHostKeyUnconfirmed) {
		t.Fatalf("no host key: %v", err)
	}

	if _, err := s.SetGit(ctx, 1, "blog", gitInput("https://github.com/acme/blog.git", agentapi.GitAuthNone)); err != nil {
		t.Fatal(err)
	}

	// The agent refuses: the run is failed, not left queued.
	f.startErr = errors.New("agent down")

	if _, err := s.Deploy(ctx, 1, "blog", store.DeploySourcePanel); err == nil {
		t.Fatal("agent error swallowed")
	}

	list, _ := s.Deploys(ctx, "blog", 10)
	if len(list) != 1 || list[0].State != store.DeployFailed || list[0].Error != "agent down" {
		t.Fatalf("after agent error %+v", list)
	}

	f.startErr = nil

	// The agent lost the run.
	d, err := s.Deploy(ctx, 1, "blog", store.DeploySourcePanel)
	if err != nil {
		t.Fatal(err)
	}

	f.finish(d.ID, agentapi.DeployStatus{State: agentapi.DeployUnknown})
	s.pollDeploys(ctx)

	if list, _ := s.Deploys(ctx, "blog", 1); list[0].State != store.DeployFailed {
		t.Fatalf("unknown %+v", list[0])
	}

	// The build succeeds but the switch fails: the run fails.
	d, err = s.Deploy(ctx, 1, "blog", store.DeploySourcePanel)
	if err != nil {
		t.Fatal(err)
	}

	f.swErr = errors.New("the deploy directory cannot be replaced")
	f.finish(d.ID, agentapi.DeployStatus{State: agentapi.DeploySucceeded, Commit: strings.Repeat("cd", 20)})
	s.pollDeploys(ctx)

	if list, _ := s.Deploys(ctx, "blog", 1); list[0].State != store.DeployFailed || !strings.Contains(list[0].Error, "could not go live") {
		t.Fatalf("switch failed %+v", list[0])
	}

	// A row left queued by a restart fails after the grace time.
	f.swErr = nil

	st := s.st
	site, _ := st.SiteByName(ctx, "blog")

	q, err := st.CreateSiteDeploy(ctx, store.SiteDeploy{SiteID: site.ID, Kind: store.DeployKindDeploy, Source: store.DeploySourcePanel}, s.now())
	if err != nil {
		t.Fatal(err)
	}

	s.pollDeploys(ctx)

	if got, _ := st.SiteDeploy(ctx, site.ID, q.ID); got.State != store.DeployQueued {
		t.Fatalf("queued failed too early %+v", got)
	}

	later := s.now().Add(queuedGrace + time.Minute)
	s.now = func() time.Time { return later }
	s.pollDeploys(ctx)

	if got, _ := st.SiteDeploy(ctx, site.ID, q.ID); got.State != store.DeployFailed || got.Error != "the deploy did not start" {
		t.Fatalf("queued %+v", got)
	}

	// Without the agent there are no deploys.
	s.deploys = nil
	if _, err := s.Deploy(ctx, 1, "blog", store.DeploySourcePanel); !errors.Is(err, ErrNoDeploys) {
		t.Fatalf("no agent: %v", err)
	}
}
