// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"slices"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/hosting"
)

type stubLimits struct {
	slices map[string]agentapi.SiteLimits
	quotas map[string][2]uint64
	used   uint64
}

func (s *stubLimits) DBSiteConnections(context.Context, string, uint32) error { return nil }

func (s *stubLimits) SiteLimitsSet(_ context.Context, user string, l agentapi.SiteLimits) error {
	s.slices[user] = l

	return nil
}

func (s *stubLimits) SiteQuotaSet(_ context.Context, user string, b, i uint64) (agentapi.SiteQuota, error) {
	s.quotas[user] = [2]uint64{b, i}

	return agentapi.SiteQuota{Available: true, BytesLimit: b, InodesLimit: i}, nil
}

func (s *stubLimits) SiteQuotaGet(_ context.Context, user string) (agentapi.SiteQuota, error) {
	q := s.quotas[user]

	return agentapi.SiteQuota{Available: true, Bytes: s.used, Inodes: 10, BytesLimit: q[0], InodesLimit: q[1]}, nil
}

func TestUsageLevels(t *testing.T) {
	for _, c := range []struct {
		q     agentapi.SiteQuota
		level string
		pct   int
	}{
		{agentapi.SiteQuota{Available: true, Bytes: 79, BytesLimit: 100}, "ok", 79},
		{agentapi.SiteQuota{Available: true, Bytes: 80, BytesLimit: 100}, "warning", 80},
		{agentapi.SiteQuota{Available: true, Bytes: 95, BytesLimit: 100}, "critical", 95},
		{agentapi.SiteQuota{Available: true, Bytes: 500, BytesLimit: 100}, "critical", 500},
		{agentapi.SiteQuota{Available: true, Bytes: 1 << 40}, "ok", 0}, // unlimited
		{agentapi.SiteQuota{Available: true, Inodes: 96, InodesLimit: 100, Bytes: 1, BytesLimit: 100}, "critical", 1},
	} {
		u := toUsageJSON(c.q)
		if u.Level != c.level || u.BytesPercent != c.pct {
			t.Errorf("%+v: %+v, want %s %d", c.q, u, c.level, c.pct)
		}
	}
}

func TestLimitsOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	ag := &stubLimits{slices: map[string]agentapi.SiteLimits{}, quotas: map[string][2]uint64{}, used: 9 << 30}
	e.sites.EnableLimits(ag)

	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/limits", cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}

	var sl siteLimitsJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &sl); err != nil {
		t.Fatal(err)
	}

	// 9 of 10 GiB used: a warning.
	if !sl.Set || sl.Preset != "small" || sl.Limits.DiskBytes != 10<<30 || sl.AppliedAt == 0 || len(sl.Limits.Unlimited) != 0 ||
		sl.Usage == nil || sl.Usage.Level != "warning" || sl.Usage.BytesPercent != 90 || !sl.Usage.Enforced {
		t.Fatalf("limits of a new site: %s", rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/limit-presets", cookie: c})

	var list struct {
		Presets []presetJSON `json:"presets"`
		CPUs    int          `json:"cpus"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list.Presets) != 3 || list.Presets[0].Name != "large" || list.CPUs != runtime.NumCPU() {
		t.Fatalf("presets: %s %v", rec.Body, err)
	}

	// Custom values with unlimited disk space are marked as such.
	rec = e.do(t, reqOpts{
		method: "PUT", path: "/api/v1/sites/shop/limits", cookie: c,
		body: `{"custom":{"diskBytes":0,"diskInodes":0,"memoryBytes":1073741824,"phpWorkers":8,"tasks":256,"cpuPriority":"high"}}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("custom: %d %s", rec.Code, rec.Body)
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &sl); err != nil || sl.Preset != "" || !slices.Equal(sl.Limits.Unlimited, []string{"diskBytes", "diskInodes", "databaseBytes", "databaseConnections"}) {
		t.Fatalf("custom answer %s %v", rec.Body, err)
	}

	if ag.slices["shop"].CPUWeight != agentapi.CPUWeightHigh || ag.quotas["shop"] != [2]uint64{} {
		t.Errorf("agent got %+v %v", ag.slices["shop"], ag.quotas["shop"])
	}

	for body, want := range map[string]string{
		`{"preset":"small","custom":{"phpWorkers":1,"cpuPriority":"low"}}`: "invalid_argument",
		`{}`:                "invalid_argument",
		`{"preset":"nope"}`: "not_found",
		`{"preset":1}`:      "bad_request",
		`{"custom":{"phpWorkers":1,"cpuPriority":"turbo"}}`: "invalid_argument",
	} {
		rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/limits", cookie: c, body: body})
		if rec.Code < 400 || errCode(t, rec) != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	// Presets: create, make default, refuse unlimited default, delete.
	rec = e.do(t, reqOpts{
		method: "POST", path: "/api/v1/limit-presets", cookie: c,
		body: `{"name":"tiny","default":true,"limits":{"diskBytes":1073741824,"diskInodes":10000,"memoryBytes":268435456,"phpWorkers":2,"tasks":32,"cpuPriority":"low"}}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create preset: %d %s", rec.Code, rec.Body)
	}

	for body, want := range map[string]string{
		`{"name":"open","default":true,"limits":{"phpWorkers":2,"cpuPriority":"low"}}`: "invalid_argument",
		`{"name":"tiny","limits":{"phpWorkers":2,"cpuPriority":"low"}}`:                "preset_exists",
		`{"name":"Bad Name","limits":{"phpWorkers":2,"cpuPriority":"low"}}`:            "invalid_argument",
		`{"name":"x","limits":{"phpWorkers":2,"cpuPriority":"low"},"extra":true}`:      "bad_request",
	} {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/limit-presets", cookie: c, body: body})
		if rec.Code < 400 || errCode(t, rec) != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/limit-presets/tiny", cookie: c}); rec.Code != http.StatusConflict || errCode(t, rec) != "preset_is_default" {
		t.Errorf("delete default: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{
		method: "PUT", path: "/api/v1/limit-presets/small", cookie: c,
		body: `{"name":"small","default":true,"limits":{"diskBytes":10737418240,"diskInodes":500000,"memoryBytes":536870912,"phpWorkers":5,"tasks":128,"cpuPriority":"normal"}}`,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("make small default again: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/limit-presets/tiny", cookie: c}); rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/limit-presets/tiny", cookie: c}); rec.Code != http.StatusNotFound {
		t.Errorf("deleted preset: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/nope/limits", cookie: c}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: %d", rec.Code)
	}

	// limits:read does not write.
	read := e.createToken(t, c, `{"name":"r","scopes":["limits:read"],"expiresInDays":1}`)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/limits", header: bearerHeader(read.Token)}); rec.Code != http.StatusOK {
		t.Errorf("read with limits:read: %d", rec.Code)
	}

	for _, o := range []reqOpts{
		{method: "PUT", path: "/api/v1/sites/shop/limits", body: `{"preset":"small"}`},
		{method: "POST", path: "/api/v1/limit-presets", body: `{"name":"x","limits":{"phpWorkers":1,"cpuPriority":"low"}}`},
		{method: "DELETE", path: "/api/v1/limit-presets/medium"},
	} {
		o.header = bearerHeader(read.Token)
		if rec := e.do(t, o); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with limits:read: %d", o.method, o.path, rec.Code)
		}
	}
}

func TestDatabaseUsageOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	e.sites.EnableLimits(&stubLimits{slices: map[string]agentapi.SiteLimits{}, quotas: map[string][2]uint64{}})

	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	for _, n := range []string{"shop_wp", "shop_log"} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/databases", cookie: c, body: `{"name":"` + n + `"}`}); rec.Code != http.StatusCreated {
			t.Fatalf("database: %d %s", rec.Code, rec.Body)
		}
	}

	get := func() siteLimitsJSON {
		t.Helper()

		rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/limits", cookie: c})

		var sl siteLimitsJSON
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &sl) != nil {
			t.Fatalf("get: %d %s", rec.Code, rec.Body)
		}

		return sl
	}

	// Not measured: no databaseUsage.
	if sl := get(); sl.DatabaseUsage != nil || sl.Limits.DatabaseBytes != 2<<30 || sl.Limits.DatabaseConnections != 20 {
		t.Fatalf("without sizes: %+v", sl)
	}

	// 1.75 GiB of 2 GiB: 87 %, a warning.
	e.sites.EnableDBSizes(stubSizer{sizes: map[string]int64{"shop_wp": 1536 << 20, "shop_log": 256 << 20, "other": 5 << 30}})

	du := get().DatabaseUsage
	if du == nil || du.Bytes != 1792<<20 || du.Databases != 2 || du.Percent != 87 || du.Level != "warning" || du.MeasuredAt == 0 {
		t.Fatalf("usage: %+v", du)
	}

	for _, c := range []struct {
		bytes, limit int64
		pct          int
		level        string
	}{{0, 0, 0, "ok"}, {5 << 30, 0, 0, "ok"}, {95, 100, 95, "critical"}, {79, 100, 79, "ok"}, {80, 100, 80, "warning"}} {
		if u := toDBUsageJSON(hosting.DatabaseUsage{Bytes: c.bytes}, c.limit); u.Percent != c.pct || u.Level != c.level {
			t.Errorf("%d of %d: %+v", c.bytes, c.limit, u)
		}
	}
}
