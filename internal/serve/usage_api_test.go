// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// SitesStats implements servermon.SiteAgent: alpha uses half a core per
// call and beta a tenth, as if 15 s passed between calls.
func (a *stubServerAgent) SitesStats(context.Context) (agentapi.SitesStatsResponse, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.siteCalls++
	n := uint64(a.siteCalls)

	return agentapi.SitesStatsResponse{Sites: []agentapi.SiteUsage{
		{Name: "alpha", CPUUsec: n * 7_500_000, MemoryBytes: 1 << 20, MemoryKnown: true, HTTPRequests: n * 30, HTTPBytesOut: n * 15_000},
		{Name: "beta", CPUUsec: n * 1_500_000, MemoryBytes: 4 << 20, MemoryKnown: true},
	}}, nil
}

func TestSiteUsageAPI(t *testing.T) {
	e, _ := newFullAPI(t)
	c := e.adminCookie(t)
	e.sites.EnableLimits(&stubLimits{slices: map[string]agentapi.SiteLimits{}, quotas: map[string][2]uint64{}})

	for _, s := range []string{"alpha", "beta"} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"` + s + `","php":"8.4","domains":["` + s + `.example"]}`}); rec.Code >= 300 {
			t.Fatalf("create %s: %d %s", s, rec.Code, rec.Body)
		}
	}

	// alpha may use half a core.
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/alpha/limits", cookie: c, body: `{"custom":{"memoryBytes":1073741824,"phpWorkers":8,"tasks":256,"cpuPriority":"normal","cpuQuotaPercent":50}}`}); rec.Code != http.StatusOK {
		t.Fatalf("limits: %d %s", rec.Code, rec.Body)
	}

	now := time.Now()
	e.monitor.Now = func() time.Time { return now }

	for range 3 {
		e.monitor.SampleSites(t.Context())

		now = now.Add(15 * time.Second)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha/usage", cookie: c})

	var one siteUsageJSON
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(rec.Body.Bytes(), &one) != nil {
		t.Fatalf("usage: %d %s", rec.Code, rec.Body)
	}

	if one.Site != "alpha" || one.Range != "1h" || one.Step != 15 || one.CPUs != runtime.NumCPU() || one.Cores != 0.5 || len(one.Points) != 2 || one.Points[0].CPU != 50 || one.Points[0].HTTPReq != 2 {
		t.Fatalf("usage body %s", rec.Body)
	}

	for _, r := range []string{"24h", "7d", "30d"} {
		if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha/usage?range=" + r, cookie: c}); rec.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", r, rec.Code, rec.Body)
		}
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha/usage?range=1y", cookie: c}); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad range: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/nope/usage", cookie: c}); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown site: %d", rec.Code)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/usage/sites?sort=memory", cookie: c})

	var rank usageRankingJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rank) != nil || len(rank.Sites) != 2 || rank.Sites[0].Site != "beta" || rank.Sort != "memory" {
		t.Fatalf("ranking: %d %s", rec.Code, rec.Body)
	}

	if rank.CPUs != runtime.NumCPU() || rank.Sites[0].Cores != float64(runtime.NumCPU()) || rank.Sites[1].Cores != 0.5 {
		t.Fatalf("ranking cores: %s", rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/usage/sites", cookie: c}); rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &rank) != nil || rank.Sites[0].Site != "alpha" {
		t.Fatalf("ranking by cpu: %d %s", rec.Code, rec.Body)
	}

	for _, q := range []string{"?sort=disk", "?range=2h"} {
		if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/usage/sites" + q, cookie: c}); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", q, rec.Code)
		}
	}

	// A user sees the usage of a granted site, never another site's and
	// never the ranking.
	gina, gid := matrixLogin(t, e, c, "gina", "user")
	if rec := e.do(t, reqOpts{method: "PUT", path: userPath(gid, "/grants"), cookie: c, body: `{"sites":["alpha"]}`}); rec.Code != http.StatusOK {
		t.Fatalf("grant: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/alpha/usage", cookie: gina}); rec.Code != http.StatusOK {
		t.Fatalf("own site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/beta/usage", cookie: gina}); rec.Code != http.StatusNotFound {
		t.Fatalf("other site: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/usage/sites", cookie: gina}); rec.Code != http.StatusForbidden {
		t.Fatalf("user ranking: %d", rec.Code)
	}
}

func TestCoresFor(t *testing.T) {
	for _, c := range []struct {
		quota int64
		cpus  int
		want  float64
	}{
		{0, 4, 4},     // no limit: all cores
		{150, 4, 1.5}, // the limit
		{400, 4, 4},   // a limit of all cores
		{800, 4, 4},   // above the server: all cores
	} {
		if got := coresFor(c.quota, c.cpus); got != c.want {
			t.Errorf("coresFor(%d, %d) = %v, want %v", c.quota, c.cpus, got, c.want)
		}
	}
}
