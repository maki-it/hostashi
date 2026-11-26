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

// stubFail2ban holds bans per jail like fail2ban does.
type stubFail2ban struct {
	mu      sync.Mutex
	down    bool
	bans    map[string][]agentapi.Fail2banBan
	unbaned []string
}

func (f *stubFail2ban) Status(context.Context) (agentapi.Fail2banStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.down {
		return agentapi.Fail2banStatus{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "fail2ban does not answer"}
	}

	var st agentapi.Fail2banStatus

	for _, name := range agentapi.Fail2banJails {
		b := f.bans[name]
		st.Jails = append(st.Jails, agentapi.Fail2banJail{Name: name, Running: name != "sshd", CurrentlyBanned: len(b), TotalBanned: len(b), TotalFailed: 10 * len(b), Bans: b})
	}

	return st, nil
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

	if got.Jails[0].Running || got.Jails[0].Bans == nil || !got.Jails[2].Running || len(got.Jails[3].Bans) != 2 {
		t.Errorf("jails: %+v", got.Jails)
	}

	if b := got.Jails[3].Bans[0]; b.Until != nil || b.Since.Unix() != 1_800_000_100 {
		t.Errorf("ban without end: %+v", b)
	}

	if b := got.Jails[2].Bans[0]; b.Until == nil || b.Until.Unix() != 1_800_003_600 {
		t.Errorf("ban with end: %+v", b)
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
