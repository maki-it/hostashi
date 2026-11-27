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
	"github.com/maki-it/hostashi/internal/f2bctl"
)

// stubFail2ban holds bans per jail like fail2ban does.
type stubFail2ban struct {
	mu      sync.Mutex
	down    bool
	bans    map[string][]agentapi.Fail2banBan
	unbaned []string
	last    agentapi.Fail2banStatusRequest
}

func (f *stubFail2ban) Status(_ context.Context, req agentapi.Fail2banStatusRequest) (agentapi.Fail2banStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.down {
		return agentapi.Fail2banStatus{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "fail2ban does not answer"}
	}

	f.last = req

	var jails []agentapi.Fail2banJail

	for _, name := range agentapi.Fail2banJails {
		b := f.bans[name]
		jails = append(jails, agentapi.Fail2banJail{Name: name, Running: name != "sshd", CurrentlyBanned: len(b), TotalBanned: len(b), TotalFailed: 10 * len(b)})
	}

	return f2bctl.Page(jails, f.bans, req), nil
}

func (f *stubFail2ban) Unban(_ context.Context, address string) (agentapi.Fail2banUnbanResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	resp := agentapi.Fail2banUnbanResponse{Jails: []string{}}

	for _, name := range agentapi.Fail2banJails {
		n := len(f.bans[name])
		f.bans[name] = slices.DeleteFunc(f.bans[name], func(b agentapi.Fail2banBan) bool { return b.Address == address })

		if len(f.bans[name]) != n {
			resp.Jails = append(resp.Jails, name)
		}
	}

	f.unbaned = append(f.unbaned, address)

	return resp, nil
}

func TestFail2banAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	e.fail2ban.bans = map[string][]agentapi.Fail2banBan{
		"hostashi-site-auth": {{Address: "203.0.113.7", Since: 1_800_000_000, Until: 1_800_003_600}},
		"recidive":           {{Address: "203.0.113.7", Since: 1_800_000_100}, {Address: "2001:db8::1", Since: 1_800_000_200, Until: 1_800_604_800}},
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/firewall/bans"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/firewall/bans", cookie: c})

	var got fail2banJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil || len(got.Jails) != 4 {
		t.Fatalf("status: %d %s", rec.Code, rec.Body)
	}

	if got.Jails[0].Running || !got.Jails[2].Running || got.Jails[3].CurrentlyBanned != 2 || got.Total != 2 || got.Page != 1 || got.PerPage != 50 || len(got.Addresses) != 2 {
		t.Errorf("status: %+v", got)
	}

	// Newest first; one row per address with every jail that blocks it.
	if b := got.Addresses[1]; b.Address != "203.0.113.7" || b.Until != nil || b.Since.Unix() != 1_800_000_000 || !slices.Equal(b.Jails, []string{"hostashi-site-auth", "recidive"}) {
		t.Errorf("ban without end: %+v", b)
	}

	if b := got.Addresses[0]; b.Address != "2001:db8::1" || b.Until == nil || b.Until.Unix() != 1_800_604_800 {
		t.Errorf("ban with end: %+v", b)
	}

	// The filters and the page reach the agent.
	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/firewall/bans?jail=hostashi-site-auth&address=203.0.113.0/24&page=2&perPage=1", cookie: c})
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("filtered: %d %s", rec.Code, rec.Body)
	}

	if want := (agentapi.Fail2banStatusRequest{Jail: "hostashi-site-auth", Address: "203.0.113.0/24", Offset: 1, Limit: 1}); e.fail2ban.last != want {
		t.Errorf("request %+v", e.fail2ban.last)
	}

	if got.Total != 1 || len(got.Addresses) != 0 || got.Page != 2 || got.PerPage != 1 {
		t.Errorf("page 2: %+v", got)
	}

	e.fail2ban.last = agentapi.Fail2banStatusRequest{Jail: "unset"}

	for _, bad := range []string{
		"jail=apache", "address=example.com", "address=203.0.113.0/99", "page=0", "page=x", "perPage=101", "perPage=0", "page=999999999",
	} {
		rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/firewall/bans?" + bad, cookie: c})
		if rec.Code != http.StatusUnprocessableEntity || errCode(t, rec) != "invalid_argument" {
			t.Errorf("%s: %d %s", bad, rec.Code, rec.Body)
		}
	}

	if e.fail2ban.last.Jail != "unset" {
		t.Errorf("agent called for a bad filter: %+v", e.fail2ban.last)
	}

	del := func(addr string) (int, string, fail2banUnbanJSON) {
		rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/firewall/bans/" + addr, cookie: c})

		var out fail2banUnbanJSON

		_ = json.Unmarshal(rec.Body.Bytes(), &out)

		code := ""
		if rec.Code != http.StatusOK {
			code = errCode(t, rec)
		}

		return rec.Code, code, out
	}

	// One action lifts the address in every jail.
	if code, _, out := del("203.0.113.7"); code != http.StatusOK || !slices.Equal(out.Jails, []string{"hostashi-site-auth", "recidive"}) {
		t.Fatalf("unban: %d %+v", code, out)
	}

	if n := e.auditCount(t, "firewall.unban"); n != 1 {
		t.Errorf("audit entries %d", n)
	}

	if code, ec, _ := del("203.0.113.7"); code != http.StatusNotFound || ec != "not_banned" {
		t.Errorf("second unban: %d %s", code, ec)
	}

	if code, _, out := del("2001:db8::1"); code != http.StatusOK || !slices.Equal(out.Jails, []string{"recidive"}) {
		t.Errorf("IPv6 unban: %d %+v", code, out)
	}

	e.fail2ban.unbaned = nil

	for _, bad := range []string{"2001:DB8::1", "203.0.113.0%2F24", "example.com", "%20203.0.113.7"} {
		if code, ec, _ := del(bad); code != http.StatusUnprocessableEntity || ec != "invalid_argument" {
			t.Errorf("%q: %d %s", bad, code, ec)
		}
	}

	if len(e.fail2ban.unbaned) != 0 {
		t.Errorf("agent called for bad addresses: %v", e.fail2ban.unbaned)
	}

	e.fail2ban.down = true

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/firewall/bans", cookie: c})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "fail2ban_unavailable" {
		t.Errorf("fail2ban down: %d %s", rec.Code, rec.Body)
	}
}
