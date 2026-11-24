// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// deployAgent is a stub agent that runs deploys.
type deployAgent struct {
	stubAgent
}

func (*deployAgent) DeployStart(_ context.Context, req agentapi.DeployStartRequest) (agentapi.DeployStatus, error) {
	return agentapi.DeployStatus{Site: req.Site, ID: req.ID, State: agentapi.DeployRunning}, nil
}

func (*deployAgent) DeployStatus(_ context.Context, req agentapi.DeployStatusRequest) (agentapi.DeployStatus, error) {
	return agentapi.DeployStatus{Site: req.Site, ID: req.ID, State: agentapi.DeployRunning}, nil
}

func (*deployAgent) DeploySwitch(context.Context, agentapi.DeploySwitchRequest) (agentapi.DeploySwitchResult, error) {
	return agentapi.DeploySwitchResult{}, nil
}

func (*deployAgent) DeployPrune(context.Context, agentapi.DeployPruneRequest) (agentapi.DeployReleases, error) {
	return agentapi.DeployReleases{}, nil
}

func (*deployAgent) DeployLog(context.Context, agentapi.DeployStatusRequest) (agentapi.DeployLog, error) {
	return agentapi.DeployLog{Log: "$ composer install\n"}, nil
}

func TestDeploysOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &deployAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/deploys", cookie: c}); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "git_not_configured") {
		t.Fatalf("without git: %d %s", rec.Code, rec.Body)
	}

	body := `{"url":"https://github.com/acme/blog.git","auth":"none","branch":"main","deployDir":"htdocs","keep":5,"buildTimeoutMinutes":15}`
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/git", cookie: c, body: body}); rec.Code != http.StatusOK {
		t.Fatalf("set git: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/deploys", cookie: c})

	var d deployJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &d); rec.Code != http.StatusAccepted || err != nil || d.State != "running" || d.Source != "panel" || d.Kind != "deploy" {
		t.Fatalf("deploy: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/deploys", cookie: c}); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "deploy_running") {
		t.Fatalf("second: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/deploys", cookie: c})

	var list []deployJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &list); rec.Code != http.StatusOK || err != nil || len(list) != 1 || list[0].ID != d.ID {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	id := strconv.FormatInt(d.ID, 10)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/deploys/" + id + "/log", cookie: c}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "composer install") {
		t.Fatalf("log: %d %s", rec.Code, rec.Body)
	}

	for path, want := range map[string]int{
		"/api/v1/sites/blog/deploys/999/log": http.StatusNotFound,
		"/api/v1/sites/blog/deploys/x/log":   http.StatusNotFound,
		"/api/v1/sites/nope/deploys/1/log":   http.StatusNotFound,
	} {
		if rec := e.do(t, reqOpts{method: "GET", path: path, cookie: c}); rec.Code != want {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}

	// A running deploy is no rollback target.
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/deploys/" + id + "/rollback", cookie: c}); rec.Code != http.StatusConflict {
		t.Fatalf("rollback to running: %d %s", rec.Code, rec.Body)
	}

	// sites:read lists but does not deploy; git:deploy does, as the API.
	read := bearerHeader(e.createToken(t, c, `{"name":"r","scopes":["sites:read"]}`).Token)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/deploys", header: read}); rec.Code != http.StatusOK {
		t.Errorf("list with sites:read: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/deploys/" + id + "/log", header: read}); rec.Code != http.StatusForbidden {
		t.Errorf("log with sites:read: %d", rec.Code)
	}

	deploy := bearerHeader(e.createToken(t, c, `{"name":"d","scopes":["git:deploy"]}`).Token)
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/deploys", header: deploy}); rec.Code != http.StatusConflict {
		t.Errorf("deploy token while running: %d %s", rec.Code, rec.Body)
	}
}
