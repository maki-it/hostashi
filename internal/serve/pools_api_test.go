// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

type stubPools struct {
	pools []agentapi.PoolInfo
	err   error
	// adminErr is what rename and default answer.
	adminErr error
}

func (s *stubPools) list(context.Context) ([]agentapi.PoolInfo, error) { return s.pools, s.err }

var errStubUnknownPool = &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown pool"}

// RenamePool behaves like the agent's registry.
func (s *stubPools) RenamePool(_ context.Context, name, newName string) (agentapi.PoolInfo, error) {
	if s.adminErr != nil {
		return agentapi.PoolInfo{}, s.adminErr
	}

	at, taken := -1, false

	for i, p := range s.pools {
		switch p.Name {
		case newName:
			taken = true
		case name:
			at = i
		}
	}

	// The agent looks for the pool first, then at the new name.
	switch {
	case at < 0:
		return agentapi.PoolInfo{}, errStubUnknownPool
	case taken:
		return agentapi.PoolInfo{}, &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "a pool with this name already exists"}
	}

	s.pools[at].Name = newName

	return s.pools[at], nil
}

// SetDefaultPool behaves like the agent's registry.
func (s *stubPools) SetDefaultPool(_ context.Context, name string) (agentapi.PoolInfo, error) {
	if s.adminErr != nil {
		return agentapi.PoolInfo{}, s.adminErr
	}

	at := -1

	for i, p := range s.pools {
		if p.Name == name {
			at = i
		}
	}

	if at < 0 {
		return agentapi.PoolInfo{}, errStubUnknownPool
	}

	for i := range s.pools {
		s.pools[i].Default = i == at
	}

	return s.pools[at], nil
}

func TestPoolsAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/pools"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	// No pools from the agent is still a list.
	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/pools", cookie: c})
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"pools\":[]}\n" {
		t.Fatalf("empty: %d %q", rec.Code, rec.Body)
	}

	e.pools.pools = []agentapi.PoolInfo{
		{Name: "default", Path: "/home", Sites: 3, Available: true, Quota: "enforced", FreeBytes: 10 << 30, TotalBytes: 40 << 30},
		{Name: "vol1", Path: "/srv/vol1", Available: false},
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/pools", cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	var out struct {
		Pools []agentapi.PoolInfo `json:"pools"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	if len(out.Pools) != 2 || out.Pools[0] != e.pools.pools[0] || out.Pools[1] != e.pools.pools[1] {
		t.Errorf("pools %+v", out.Pools)
	}

	// A token needs sites:read.
	other := e.createToken(t, c, `{"name":"o","scopes":["doctor:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/pools", header: bearerHeader(other.Token)}); rec.Code != http.StatusForbidden {
		t.Errorf("token without sites:read: %d", rec.Code)
	}

	good := e.createToken(t, c, `{"name":"r","scopes":["sites:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/pools", header: bearerHeader(good.Token)}); rec.Code != http.StatusOK {
		t.Errorf("token with sites:read: %d %s", rec.Code, rec.Body)
	}

	// The agent does not answer.
	e.pools.err = errors.New("down")
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/pools", cookie: c}); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "agent_unavailable" {
		t.Errorf("agent down: %d %s", rec.Code, rec.Body)
	}

	e.pools.err = context.DeadlineExceeded
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/pools", cookie: c}); rec.Code != http.StatusGatewayTimeout {
		t.Errorf("agent timeout: %d", rec.Code)
	}
}

// TestPoolsAPIRoles: managers create sites, so they see the pools; a plain
// user does not.
func TestPoolsAPIRoles(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	mona, _ := matrixLogin(t, e, c, "mona", "manager")
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/pools", cookie: mona}); rec.Code != http.StatusOK {
		t.Errorf("manager: %d %s", rec.Code, rec.Body)
	}

	uli, _ := matrixLogin(t, e, c, "uli", "user")
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/pools", cookie: uli}); rec.Code != http.StatusForbidden {
		t.Errorf("user: %d %s", rec.Code, rec.Body)
	}
}

func TestPoolRenameAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	ctx := t.Context()

	e.pools.pools = []agentapi.PoolInfo{
		{Name: "default", Path: "/home", Sites: 1, Available: true, Default: true},
		{Name: "vol1", Path: "/srv/vol1", Sites: 1, Available: true},
	}

	for _, name := range []string{"blog", "shop"} {
		pool := map[string]string{"blog": "vol1", "shop": "default"}[name]
		if _, err := e.st.CreateSite(ctx, name, pool, "", []string{name + ".example.com"}, time.Now()); err != nil {
			t.Fatal(err)
		}
	}

	rename := func(name, body string) *httptest.ResponseRecorder {
		return e.do(t, reqOpts{method: "POST", path: "/api/v1/pools/" + name + "/rename", body: body, cookie: c})
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/pools/vol1/rename", body: `{"name":"fast"}`}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	for _, tc := range []struct{ name, body string }{
		{"vol1", `{"name":"Fast"}`}, {"vol1", `{"name":""}`}, {"vol1", `{"name":"a b"}`}, {"Bad", `{"name":"fast"}`}, {"vol1", `{"nme":"fast"}`},
	} {
		if rec := rename(tc.name, tc.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: %d %s", tc.name, tc.body, rec.Code, rec.Body)
		}
	}

	if rec := rename("nope", `{"name":"fast"}`); rec.Code != http.StatusNotFound {
		t.Errorf("unknown pool: %d %s", rec.Code, rec.Body)
	}

	if rec := rename("vol1", `{"name":"default"}`); rec.Code != http.StatusConflict {
		t.Errorf("taken name: %d %s", rec.Code, rec.Body)
	}

	rec := rename("vol1", `{"name":"fast"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("rename: %d %s", rec.Code, rec.Body)
	}

	var got agentapi.PoolInfo
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Name != "fast" {
		t.Fatalf("answer %+v %v", got, err)
	}

	if s, err := e.st.SiteByName(ctx, "blog"); err != nil || s.Pool != "fast" {
		t.Errorf("blog: %+v %v", s.Pool, err)
	}

	if s, err := e.st.SiteByName(ctx, "shop"); err != nil || s.Pool != "default" {
		t.Errorf("shop: %+v %v", s.Pool, err)
	}

	if d := string(e.auditEntry(t, "pool.rename").Diff); !strings.Contains(d, "vol1") || !strings.Contains(d, "fast") {
		t.Errorf("audit diff %s", d)
	}

	// The same request again after the agent renamed already: the panel's
	// step finishes, nothing fails.
	if _, err := e.st.RenameSitePool(ctx, "fast", "vol1", time.Now()); err != nil {
		t.Fatal(err)
	}

	if rec := rename("vol1", `{"name":"fast"}`); rec.Code != http.StatusOK {
		t.Fatalf("repeat: %d %s", rec.Code, rec.Body)
	}

	if s, _ := e.st.SiteByName(ctx, "blog"); s.Pool != "fast" {
		t.Errorf("blog after repeat: %s", s.Pool)
	}

	// The agent does not answer.
	e.pools.adminErr = errors.New("down")
	if rec := rename("fast", `{"name":"slow"}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("agent down: %d", rec.Code)
	}

	e.pools.adminErr = nil

	// Managers and users may not; a token needs system:write.
	for _, role := range []string{"manager", "user"} {
		rc, _ := matrixLogin(t, e, c, role+"1", role)
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/pools/fast/rename", body: `{"name":"x"}`, cookie: rc}); rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d", role, rec.Code)
		}
	}

	read := e.createToken(t, c, `{"name":"r","scopes":["sites:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/pools/fast/rename", body: `{"name":"x"}`, header: bearerHeader(read.Token)}); rec.Code != http.StatusForbidden {
		t.Errorf("token without system:write: %d", rec.Code)
	}
}

func TestPoolDefaultAPI(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	e.pools.pools = []agentapi.PoolInfo{
		{Name: "default", Path: "/home", Available: true, Default: true},
		{Name: "vol1", Path: "/srv/vol1", Available: true},
	}

	set := func(name string) *httptest.ResponseRecorder {
		return e.do(t, reqOpts{method: "POST", path: "/api/v1/pools/" + name + "/default", cookie: c})
	}

	if rec := set("Bad"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad name: %d", rec.Code)
	}

	if rec := set("nope"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", rec.Code)
	}

	rec := set("vol1")
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	if e.pools.pools[0].Default || !e.pools.pools[1].Default {
		t.Errorf("pools %+v", e.pools.pools)
	}

	if d := string(e.auditEntry(t, "pool.default").Diff); !strings.Contains(d, `"default"`) || !strings.Contains(d, "vol1") {
		t.Errorf("audit diff %s", d)
	}

	if DefaultPoolOf(e.pools.pools) != "vol1" || DefaultPoolOf(nil) != "" {
		t.Error("DefaultPoolOf")
	}
}
