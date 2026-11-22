// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

type stubNode struct {
	mu      sync.Mutex
	started []string
	status  agentapi.NodeStatus
	refuse  bool
}

func (s *stubNode) Install(_ context.Context, m string) (agentapi.NodeStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refuse {
		return agentapi.NodeStatus{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "nodeinstall: an installation is already running"}
	}

	s.started = append(s.started, m)
	s.status = agentapi.NodeStatus{Last: m, State: agentapi.PHPInstallRunning}

	return s.status, nil
}

func (s *stubNode) Status(context.Context) (agentapi.NodeStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.status.State == "" {
		return agentapi.NodeStatus{State: agentapi.PHPInstallIdle}, nil
	}

	return s.status, nil
}

func TestNodeAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	js := map[string]string{"Content-Type": "application/json"}
	post := func(body string) (int, string) {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/node/install", body: body, cookie: c, header: js})
		if rec.Code >= 400 {
			return rec.Code, errCode(t, rec)
		}

		return rec.Code, ""
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/node", cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}

	var got nodeJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.State != agentapi.PHPInstallIdle ||
		got.Log == nil || got.Installed == nil || len(got.Releases) != len(agentapi.NodeReleases) {
		t.Fatalf("status: %+v %v", got, err)
	}

	if r := got.Releases[len(got.Releases)-1]; r.Major != "26" || r.Version == "" || r.End == "" {
		t.Errorf("newest release: %+v", r)
	}

	for _, body := range []string{`{}`, `{"major":"25"}`, `{"major":"../24"}`, `{"major":24}`} {
		if code, _ := post(body); code != http.StatusUnprocessableEntity && code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, code)
		}
	}

	if code, _ := post(`{"major":"24"}`); code != http.StatusAccepted {
		t.Errorf("install 24: %d", code)
	}

	if want := []string{"24"}; !slices.Equal(e.node.started, want) {
		t.Errorf("started %v", e.node.started)
	}

	e.node.refuse = true

	if code, ec := post(`{"major":"22"}`); code != http.StatusConflict || ec != "node_install_refused" {
		t.Errorf("busy: %d %s", code, ec)
	}

	var n int
	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action = ?`, "node.install", &n); err != nil || n != 1 {
		t.Errorf("audit: %d %v", n, err)
	}

	// sites:read may look, only node:install may install.
	read := e.createToken(t, c, `{"name":"r","scopes":["sites:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/node", header: bearerHeader(read.Token)}); rec.Code != http.StatusOK {
		t.Errorf("status with sites:read: %d", rec.Code)
	}

	h := bearerHeader(read.Token)
	h["Content-Type"] = "application/json"

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/node/install", body: `{"major":"24"}`, header: h}); rec.Code != http.StatusForbidden {
		t.Errorf("install with sites:read: %d", rec.Code)
	}
}
