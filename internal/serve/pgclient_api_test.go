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

type stubPGClient struct {
	mu      sync.Mutex
	started []string
	status  agentapi.PGClientStatus
	refuse  bool
}

func (s *stubPGClient) Install(_ context.Context, m string) (agentapi.PGClientStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.refuse {
		return agentapi.PGClientStatus{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "pgclient: an installation is already running"}
	}

	if m == "" {
		m = agentapi.PGClientNewest()
	}

	s.started = append(s.started, m)
	s.status = agentapi.PGClientStatus{Major: m, State: agentapi.PHPInstallRunning}

	return s.status, nil
}

func (s *stubPGClient) Status(context.Context) (agentapi.PGClientStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.status.State == "" {
		return agentapi.PGClientStatus{State: agentapi.PHPInstallIdle}, nil
	}

	return s.status, nil
}

func TestPGClientAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	js := map[string]string{"Content-Type": "application/json"}
	path := "/api/v1/db-servers/postgresql-client"
	post := func(body string) (int, string) {
		rec := e.do(t, reqOpts{method: "POST", path: path, body: body, cookie: c, header: js})
		if rec.Code >= 400 {
			return rec.Code, errCode(t, rec)
		}

		return rec.Code, ""
	}

	rec := e.do(t, reqOpts{method: "GET", path: path, cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}

	var got pgClientJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.State != agentapi.PHPInstallIdle ||
		got.Newest != agentapi.PGClientNewest() || got.Log == nil || got.Installed == nil {
		t.Fatalf("status: %+v %v", got, err)
	}

	for _, body := range []string{`{"major":"13"}`, `{"major":"99"}`, `{"major":"../19"}`, `{"major":19}`} {
		if code, _ := post(body); code != http.StatusUnprocessableEntity && code != http.StatusBadRequest {
			t.Errorf("%s: %d", body, code)
		}
	}

	if code, _ := post(`{}`); code != http.StatusAccepted {
		t.Errorf("newest: %d", code)
	}

	if code, _ := post(`{"major":"` + agentapi.PGClientNewest() + `"}`); code != http.StatusAccepted {
		t.Errorf("named major: %d", code)
	}

	if want := []string{agentapi.PGClientNewest(), agentapi.PGClientNewest()}; !slices.Equal(e.pgclient.started, want) {
		t.Errorf("started %v", e.pgclient.started)
	}

	e.pgclient.refuse = true

	if code, ec := post(`{}`); code != http.StatusConflict || ec != "pg_client_install_refused" {
		t.Errorf("busy: %d %s", code, ec)
	}

	var n int
	if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action = ?`, "pg_client.install", &n); err != nil || n != 2 {
		t.Errorf("audit: %d %v", n, err)
	}

	// A token without db_servers:write can do neither.
	read := e.createToken(t, c, `{"name":"r","scopes":["databases:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: path, header: bearerHeader(read.Token)}); rec.Code != http.StatusForbidden {
		t.Errorf("status with databases:read: %d", rec.Code)
	}

	h := bearerHeader(read.Token)
	h["Content-Type"] = "application/json"

	if rec := e.do(t, reqOpts{method: "POST", path: path, body: `{}`, header: h}); rec.Code != http.StatusForbidden {
		t.Errorf("install with databases:read: %d", rec.Code)
	}
}
