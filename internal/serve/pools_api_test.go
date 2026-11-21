// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type stubPools struct {
	pools []agentapi.PoolInfo
	err   error
}

func (s *stubPools) list(context.Context) ([]agentapi.PoolInfo, error) { return s.pools, s.err }

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
