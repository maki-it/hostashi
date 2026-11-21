// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

type stubCache struct {
	applied map[string]int
	flushes int
	fail    bool
}

func (s *stubCache) CacheApply(_ context.Context, site string, mb int) (agentapi.CacheStatus, error) {
	if s.fail {
		return agentapi.CacheStatus{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the object cache did not start"}
	}

	s.applied[site] = mb

	return s.status(site), nil
}

func (s *stubCache) CacheFlush(_ context.Context, site string) (agentapi.CacheStatus, error) {
	s.flushes++

	return s.status(site), nil
}

func (s *stubCache) CacheStatus(_ context.Context, site string) (agentapi.CacheStatus, error) {
	if s.fail {
		return agentapi.CacheStatus{}, errors.New("agent down")
	}

	return s.status(site), nil
}

func (s *stubCache) status(site string) agentapi.CacheStatus {
	mb := s.applied[site]
	if mb == 0 {
		return agentapi.CacheStatus{Site: site}
	}

	return agentapi.CacheStatus{Site: site, MemoryMB: mb, State: "active", UsedBytes: 1 << 20}
}

func TestObjectCacheOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	cache := &stubCache{applied: map[string]int{}}

	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	// Without the agent's cache operations the routes say so.
	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/object-cache", cookie: c})
	if rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "object_cache_unavailable" {
		t.Fatalf("without cache: %d %s", rec.Code, rec.Body)
	}

	e.sites.EnableObjectCache(cache)

	get := func() objectCacheJSON {
		t.Helper()

		rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/object-cache", cookie: c})
		if rec.Code != http.StatusOK {
			t.Fatalf("get: %d %s", rec.Code, rec.Body)
		}

		var out objectCacheJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}

		return out
	}

	if v := get(); v.Enabled || v.MemoryMB != agentapi.DefaultCacheMB || v.Socket != "/run/hostashi-valkey/shop/valkey.sock" || v.Status == nil || v.Status.State != "" {
		t.Fatalf("initial: %+v", v)
	}

	// Flushing a cache that is off is refused.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/object-cache/flush", cookie: c})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "object_cache_off" {
		t.Fatalf("flush while off: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/object-cache", cookie: c, body: `{"enabled":true}`})
	if rec.Code != http.StatusOK || cache.applied["shop"] != agentapi.DefaultCacheMB {
		t.Fatalf("enable: %d %s %v", rec.Code, rec.Body, cache.applied)
	}

	for body, code := range map[string]string{
		`{"memoryMb":16}`:   "invalid_argument",
		`{"memoryMb":4096}`: "invalid_argument",
		`{}`:                "invalid_argument",
		`{"memoryMb":"x"}`:  "bad_request",
	} {
		rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/object-cache/size", cookie: c, body: body})
		if rec.Code < 400 || errCode(t, rec) != code {
			t.Errorf("size %s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/object-cache/size", cookie: c, body: `{"memoryMb":256}`})
	if rec.Code != http.StatusOK || cache.applied["shop"] != 256 {
		t.Fatalf("resize: %d %s %v", rec.Code, rec.Body, cache.applied)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/object-cache/flush", cookie: c})
	if rec.Code != http.StatusOK || cache.flushes != 1 {
		t.Fatalf("flush: %d %s", rec.Code, rec.Body)
	}

	// Off keeps the size, so turning it on again uses it.
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/object-cache", cookie: c, body: `{"enabled":false}`}); rec.Code != http.StatusOK || cache.applied["shop"] != 0 {
		t.Fatalf("disable: %d %s %v", rec.Code, rec.Body, cache.applied)
	}

	if v := get(); v.Enabled || v.MemoryMB != 256 {
		t.Fatalf("after disable: %+v", v)
	}

	// A change the agent refuses is not stored.
	cache.fail = true

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/object-cache", cookie: c, body: `{"enabled":true}`})
	if rec.Code != http.StatusConflict {
		t.Fatalf("refused enable: %d %s", rec.Code, rec.Body)
	}

	if v := get(); v.Enabled || v.Status != nil {
		t.Fatalf("after refused enable: %+v", v)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/nope/object-cache", cookie: c})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown site: %d %s", rec.Code, rec.Body)
	}
}
