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

type stubPython struct {
	mu      sync.Mutex
	started []string
	removed []string
	status  agentapi.PythonStatus
	refuse  bool
}

func (s *stubPython) Remove(_ context.Context, line string) (agentapi.PythonStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refuse {
		return agentapi.PythonStatus{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "apps still run on this line: shop"}
	}

	s.removed = append(s.removed, line)

	return agentapi.PythonStatus{State: agentapi.PHPInstallIdle}, nil
}

func (s *stubPython) Install(_ context.Context, line string) (agentapi.PythonStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refuse {
		return agentapi.PythonStatus{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "an installation is already running"}
	}

	s.started = append(s.started, line)
	s.status = agentapi.PythonStatus{Last: line, State: agentapi.PHPInstallRunning}

	return s.status, nil
}

func (s *stubPython) Status(context.Context) (agentapi.PythonStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.status.State == "" {
		return agentapi.PythonStatus{State: agentapi.PHPInstallIdle}, nil
	}

	return s.status, nil
}

func TestPythonAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	js := map[string]string{"Content-Type": "application/json"}
	post := func(path, body string) (int, string) {
		rec := e.do(t, reqOpts{method: "POST", path: path, body: body, cookie: c, header: js})
		if rec.Code >= 400 {
			return rec.Code, errCode(t, rec)
		}

		return rec.Code, ""
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/python", cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}

	var got pythonJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.State != agentapi.PHPInstallIdle ||
		got.Log == nil || got.Installed == nil || len(got.Releases) != len(agentapi.PythonReleases) {
		t.Fatalf("status: %+v %v", got, err)
	}

	if r := got.Releases[0]; r.Line != agentapi.PythonReleases[0].Line || r.Version == "" || r.End == "" {
		t.Errorf("oldest release: %+v", r)
	}

	for _, path := range []string{"/api/v1/python/install", "/api/v1/python/remove"} {
		for _, body := range []string{`{}`, `{"line":"3.10"}`, `{"line":"../3.13"}`, `{"line":3.13}`, `{"major":"3.13"}`} {
			if code, _ := post(path, body); code != http.StatusUnprocessableEntity && code != http.StatusBadRequest {
				t.Errorf("%s %s: %d", path, body, code)
			}
		}
	}

	if code, _ := post("/api/v1/python/install", `{"line":"3.13"}`); code != http.StatusAccepted {
		t.Errorf("install 3.13: %d", code)
	}

	if code, _ := post("/api/v1/python/remove", `{"line":"3.12"}`); code != http.StatusOK {
		t.Errorf("remove 3.12: %d", code)
	}

	if !slices.Equal(e.python.started, []string{"3.13"}) || !slices.Equal(e.python.removed, []string{"3.12"}) {
		t.Errorf("started %v, removed %v", e.python.started, e.python.removed)
	}

	e.python.refuse = true

	for _, path := range []string{"/api/v1/python/install", "/api/v1/python/remove"} {
		if code, ec := post(path, `{"line":"3.14"}`); code != http.StatusConflict || ec != "python_install_refused" {
			t.Errorf("%s refused: %d %s", path, code, ec)
		}
	}

	for _, action := range []string{"python.install", "python.remove"} {
		var n int
		if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action = ?`, action, &n); err != nil || n != 1 {
			t.Errorf("audit %s: %d %v", action, n, err)
		}
	}

	// sites:read may look, only python:install may install or remove.
	read := e.createToken(t, c, `{"name":"r","scopes":["sites:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/python", header: bearerHeader(read.Token)}); rec.Code != http.StatusOK {
		t.Errorf("status with sites:read: %d", rec.Code)
	}

	h := bearerHeader(read.Token)
	h["Content-Type"] = "application/json"

	for _, path := range []string{"/api/v1/python/install", "/api/v1/python/remove"} {
		if rec := e.do(t, reqOpts{method: "POST", path: path, body: `{"line":"3.13"}`, header: h}); rec.Code != http.StatusForbidden {
			t.Errorf("%s with sites:read: %d", path, rec.Code)
		}
	}
}
