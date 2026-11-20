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
	"github.com/maki-it/hostashi/internal/agentrpc"
)

// stubFirewall keeps one pending change like the agent does.
type stubFirewall struct {
	mu         sync.Mutex
	enabled    bool
	live       agentapi.FirewallState
	pending    *agentapi.FirewallPending
	rolledBack int64
	applied    int
}

func (f *stubFirewall) status() agentapi.FirewallStatus {
	return agentapi.FirewallStatus{Enabled: f.enabled, State: f.live, Pending: f.pending, RolledBack: f.rolledBack}
}

func (f *stubFirewall) Status(context.Context) (agentapi.FirewallStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.status(), nil
}

func (f *stubFirewall) Apply(_ context.Context, s agentapi.FirewallState) (agentapi.FirewallStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if !f.enabled {
		return agentapi.FirewallStatus{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the firewall is off"}
	}

	if f.pending != nil {
		return agentapi.FirewallStatus{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "another change is pending"}
	}

	f.applied++
	f.live = s
	f.pending = &agentapi.FirewallPending{ID: strings.Repeat("ab", 16), Deadline: 1_800_000_120}

	return f.status(), nil
}

func (f *stubFirewall) Confirm(_ context.Context, id string) (agentapi.FirewallStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.pending == nil || f.pending.ID != id {
		return agentapi.FirewallStatus{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "no change pending"}
	}

	f.pending = nil

	return f.status(), nil
}

func TestFirewallAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	put := func(body string) (int, string, firewallJSON) {
		rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/firewall", body: body, cookie: c, header: map[string]string{"Content-Type": "application/json"}})

		var out firewallJSON

		_ = json.Unmarshal(rec.Body.Bytes(), &out)

		code := ""
		if rec.Code != http.StatusOK {
			code = errCode(t, rec)
		}

		return rec.Code, code, out
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/firewall"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/firewall", cookie: c})

	var got firewallJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || !got.Enabled || got.State.Open == nil ||
		got.State.SSHAllow == nil || got.Pending != nil || got.ClientIP != "192.0.2.10" || got.ConfirmWithin != 120 {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}

	// The request comes from 192.0.2.10: an allowlist without it is a
	// lockout unless the admin says otherwise.
	if code, ec, _ := put(`{"state":{"open":[],"sshAllow":[],"panelAllow":["198.51.100.0/24"]}}`); code != http.StatusConflict || ec != "lockout_panel" {
		t.Errorf("panel lockout: %d %s", code, ec)
	}

	if code, ec, _ := put(`{"state":{"open":[],"sshAllow":["198.51.100.1"],"panelAllow":[]}}`); code != http.StatusConflict || ec != "lockout_ssh" {
		t.Errorf("ssh lockout: %d %s", code, ec)
	}

	if code, ec, _ := put(`{"state":{"open":[{"proto":"tcp","from":443}],"sshAllow":[],"panelAllow":[]}}`); code != http.StatusUnprocessableEntity || ec != "invalid_argument" {
		t.Errorf("port 443: %d %s", code, ec)
	}

	if e.firewall.applied != 0 {
		t.Fatalf("refused changes reached the agent: %d", e.firewall.applied)
	}

	code, _, out := put(`{"state":{"open":[{"proto":"tcp","from":3000,"note":"app"}],"sshAllow":["192.0.2.0/24"],"panelAllow":["192.0.2.10"]}}`)
	if code != http.StatusOK || out.Pending == nil || out.Pending.ID == "" || out.Pending.Deadline.Unix() != 1_800_000_120 {
		t.Fatalf("change: %d %+v", code, out)
	}

	// The ID is only in the answer of the change.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/firewall", cookie: c})
	if json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Pending == nil || got.Pending.ID != "" {
		t.Errorf("status while pending: %s", rec.Body)
	}

	if code, ec, _ := put(`{"state":{"open":[],"sshAllow":[],"panelAllow":[]}}`); code != http.StatusConflict || ec != "firewall_unavailable" {
		t.Errorf("second change while pending: %d %s", code, ec)
	}

	confirm := func(id string) int {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/firewall/confirm", body: `{"id":"` + id + `"}`, cookie: c, header: map[string]string{"Content-Type": "application/json"}})

		return rec.Code
	}

	if code := confirm(strings.Repeat("0", 32)); code != http.StatusNotFound {
		t.Errorf("wrong id: %d", code)
	}

	if code := confirm("nope"); code != http.StatusUnprocessableEntity {
		t.Errorf("bad id: %d", code)
	}

	if code := confirm(out.Pending.ID); code != http.StatusOK {
		t.Fatalf("confirm: %d", code)
	}

	// An allowlist without the address passes with otherWay.
	if code, _, _ := put(`{"state":{"open":[],"sshAllow":["198.51.100.1"],"panelAllow":[]},"otherWay":true}`); code != http.StatusOK {
		t.Errorf("otherWay: %d", code)
	}

	// The agent rolled back on its own: the next status writes it into
	// the audit log once.
	e.firewall.mu.Lock()
	e.firewall.pending, e.firewall.rolledBack = nil, 1_800_000_200
	e.firewall.mu.Unlock()

	for range 2 {
		e.do(t, reqOpts{method: "GET", path: "/api/v1/firewall", cookie: c})
	}

	for action, want := range map[string]int{"firewall.change": 2, "firewall.confirm": 1, "firewall.rollback": 1} {
		var n int
		if err := e.st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE action = ?`, action, &n); err != nil || n != want {
			t.Errorf("audit %s: %d %v", action, n, err)
		}
	}

	// Tokens need the firewall scopes.
	read := e.createToken(t, c, `{"name":"r","scopes":["firewall:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/firewall", header: bearerHeader(read.Token)}); rec.Code != http.StatusOK {
		t.Errorf("firewall:read: %d", rec.Code)
	}

	h := bearerHeader(read.Token)
	h["Content-Type"] = "application/json"

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/firewall", body: `{"state":{"open":[],"sshAllow":[],"panelAllow":[]}}`, header: h}); rec.Code != http.StatusForbidden {
		t.Errorf("write with firewall:read: %d", rec.Code)
	}
}

func TestFirewallAPIOff(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	e.firewall.enabled = false

	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/firewall", body: `{"state":{"open":[],"sshAllow":[],"panelAllow":[]}}`, cookie: c, header: map[string]string{"Content-Type": "application/json"}})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "firewall_unavailable" {
		t.Errorf("firewall off: %d %s", rec.Code, rec.Body)
	}
}
