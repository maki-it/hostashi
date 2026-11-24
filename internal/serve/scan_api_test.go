// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// scanAgent is a stub agent with ClamAV that can be switched on.
type scanAgent struct {
	stubAgent

	mu sync.Mutex
	on bool
}

func (a *scanAgent) ScanEngineStatus(context.Context) (agentapi.ScanEngineStatus, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.on {
		return agentapi.ScanEngineStatus{State: agentapi.ScanEngineOn, Version: "1.4.6"}, nil
	}

	return agentapi.ScanEngineStatus{State: agentapi.ScanEngineOff}, nil
}

func (a *scanAgent) ScanEngineSet(_ context.Context, on bool) (agentapi.ScanEngineStatus, error) {
	a.mu.Lock()
	a.on = on
	a.mu.Unlock()

	if on {
		return agentapi.ScanEngineStatus{State: agentapi.ScanEngineInstalling}, nil
	}

	return agentapi.ScanEngineStatus{State: agentapi.ScanEngineRemoving}, nil
}

func (*scanAgent) ScanStart(_ context.Context, req agentapi.ScanStartRequest) (agentapi.ScanStatus, error) {
	return agentapi.ScanStatus{Site: req.Site, ID: req.ID, State: agentapi.ScanRunning}, nil
}

func (*scanAgent) ScanStatus(_ context.Context, req agentapi.ScanStatusRequest) (agentapi.ScanStatus, error) {
	return agentapi.ScanStatus{Site: req.Site, ID: req.ID, State: agentapi.ScanRunning}, nil
}

func (*scanAgent) ScanQuarantine(context.Context, agentapi.ScanQuarantineRequest) (agentapi.QuarantineItem, error) {
	return agentapi.QuarantineItem{}, nil
}

func (*scanAgent) ScanRestore(context.Context, agentapi.ScanRestoreRequest) (agentapi.ScanRestored, error) {
	return agentapi.ScanRestored{}, nil
}

func TestMalwareScanOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &scanAgent{})
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/malware-scan", cookie: c}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"off"`) {
		t.Fatalf("engine: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/malware-scans", cookie: c}); rec.Code != http.StatusConflict || errCode(t, rec) != "scan_off" {
		t.Fatalf("scan while off: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/malware-scan", cookie: c, body: `{}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("engine without on: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/malware-scan", cookie: c, body: `{"on":true}`}); rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"state":"installing"`) {
		t.Fatalf("engine on: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/malware-scan", cookie: c, body: `{"schedule":"hourly"}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad schedule: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/malware-scan", cookie: c, body: `{"schedule":"daily"}`}); rec.Code != http.StatusNoContent {
		t.Fatalf("schedule: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/malware-scans", cookie: c})

	var run scanRunJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &run); rec.Code != http.StatusAccepted || err != nil || run.State != "queued" || run.Source != "panel" {
		t.Fatalf("scan: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/malware-scans", cookie: c}); rec.Code != http.StatusConflict || errCode(t, rec) != "scan_running" {
		t.Fatalf("second scan: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/malware-scan", cookie: c})

	var v siteScanJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &v); rec.Code != http.StatusOK || err != nil || v.Engine != "on" || v.Schedule != "daily" ||
		v.NextScheduled == "" || len(v.Runs) != 1 || v.Runs[0].ID != run.ID || v.Findings == nil {
		t.Fatalf("site scan: %d %s", rec.Code, rec.Body)
	}

	for _, path := range []string{"quarantine", "restore", "ignore", "reopen"} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/malware-findings/99/" + path, cookie: c}); rec.Code != http.StatusNotFound || errCode(t, rec) != "finding_not_found" {
			t.Fatalf("%s unknown finding: %d %s", path, rec.Code, rec.Body)
		}
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/malware-findings/x/ignore", cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("bad id: %d %s", rec.Code, rec.Body)
	}
}
