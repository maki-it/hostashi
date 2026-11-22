// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"reflect"
	"slices"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// mapResolver answers from a fixed table.
type mapResolver map[string][]netip.Addr

func (m mapResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	a, ok := m[host]
	if !ok {
		return nil, errors.New("no such host")
	}

	return a, nil
}

func TestProxySiteOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	dns := mapResolver{
		"backend.example.net": {netip.MustParseAddr("10.0.0.6"), netip.MustParseAddr("10.0.0.5")},
		"other.example.net":   {netip.MustParseAddr("192.168.1.9")},
		"rebind.example.net":  {netip.MustParseAddr("10.0.0.5"), netip.MustParseAddr("127.0.0.1")},
		"meta.example.net":    {netip.MustParseAddr("169.254.169.254")},
	}
	e.sites.SetResolver(dns)

	for body, want := range map[string]int{
		`{"name":"app","domains":["app.example.com"],"type":"proxy"}`:                                                     http.StatusUnprocessableEntity,
		`{"name":"app","domains":["app.example.com"],"proxy":{"target":"http://10.0.0.5"}}`:                               http.StatusUnprocessableEntity,
		`{"name":"app","domains":["app.example.com"],"type":"redirect","proxy":{"target":"http://10.0.0.5"}}`:             http.StatusUnprocessableEntity,
		`{"name":"app","domains":["app.example.com"],"type":"proxy","proxy":{"target":"http://127.0.0.1:8444"}}`:          http.StatusUnprocessableEntity,
		`{"name":"app","domains":["app.example.com"],"type":"proxy","proxy":{"target":"http://169.254.169.254/latest"}}`:  http.StatusUnprocessableEntity,
		`{"name":"app","domains":["app.example.com"],"type":"proxy","proxy":{"target":"http://rebind.example.net"}}`:      http.StatusUnprocessableEntity,
		`{"name":"app","domains":["app.example.com"],"type":"proxy","proxy":{"target":"http://meta.example.net"}}`:        http.StatusUnprocessableEntity,
		`{"name":"app","domains":["app.example.com"],"type":"proxy","proxy":{"target":"http://missing.example.net"}}`:     http.StatusUnprocessableEntity,
		`{"name":"app","domains":["app.example.com"],"type":"proxy","proxy":{"target":"https://app.example.com"}}`:        http.StatusUnprocessableEntity,
		`{"name":"app","domains":["app.example.com"],"type":"proxy","php":"8.4","proxy":{"target":"http://10.0.0.5"}}`:    http.StatusUnprocessableEntity,
		`{"name":"app","domains":["app.example.com"],"type":"proxy","proxy":{"target":"http://backend.example.net/a;b"}}`: http.StatusUnprocessableEntity,
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body}); rec.Code != want {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{
		method: "POST", path: "/api/v1/sites", cookie: c,
		body: `{"name":"app","domains":["app.example.com"],"type":"proxy","proxy":{"target":"https://backend.example.net/base","targetHost":true}}`,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	var site siteJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || site.Type != "proxy" || site.Account || site.Redirect != nil ||
		!reflect.DeepEqual(site.Proxy, &proxyJSON{Target: "https://backend.example.net/base", TargetHost: true}) {
		t.Fatalf("site %+v %v", site, err)
	}

	if len(ag.created) != 0 {
		t.Fatalf("account created: %v", ag.created)
	}

	ws := ag.web.Sites[0]
	if ws.User != "app" || ws.PHP != "" || ws.Redirect != nil ||
		!reflect.DeepEqual(ws.Proxy, &agentapi.WebProxy{Target: "https://backend.example.net/base", TargetHost: true}) ||
		!slices.Equal(ws.ProxyAddrs, []string{"10.0.0.5:443", "10.0.0.6:443"}) {
		t.Fatalf("web state %+v", ws)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/app/proxy", cookie: c, body: `{"target":"http://other.example.net:8080","insecure":true}`})
	if rec.Code != http.StatusOK {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	if ws := ag.web.Sites[0]; ws.Proxy == nil || ws.Proxy.Target != "http://other.example.net:8080" || !ws.Proxy.Insecure || ws.Proxy.TargetHost ||
		!slices.Equal(ws.ProxyAddrs, []string{"192.168.1.9:8080"}) {
		t.Fatalf("after set %+v", ws)
	}

	for _, body := range []string{`{"target":"http://[::1]"}`, `{"target":"http://rebind.example.net"}`, `{"target":"https://app.example.com"}`} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/app/proxy", cookie: c, body: body}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("set %s: %d %s", body, rec.Code, rec.Body)
		}
	}

	// A name that stops resolving does not fail the apply: the site goes
	// without addresses, and the agent keeps the live ones.
	delete(dns, "other.example.net")

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/app/domains", cookie: c, body: `{"domains":["app.example.com","www.app.example.com"]}`}); rec.Code != http.StatusOK {
		t.Fatalf("domains: %d %s", rec.Code, rec.Body)
	}

	if ws := ag.web.Sites[0]; ws.Proxy == nil || len(ws.ProxyAddrs) != 0 {
		t.Fatalf("unresolved %+v", ws)
	}

	// The target cannot become a domain of the site.
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/app/domains", cookie: c, body: `{"domains":["app.example.com","other.example.net"]}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("domain loop: %d %s", rec.Code, rec.Body)
	}

	for _, req := range []reqOpts{
		{method: "PUT", path: "/api/v1/sites/app/php", body: `{"php":"8.4"}`},
		{method: "PUT", path: "/api/v1/sites/app/web", body: `{"dir":"","spa":false,"assetCache":false}`},
		{method: "PUT", path: "/api/v1/sites/app/php-programs", body: `{"enabled":true}`},
		{method: "PUT", path: "/api/v1/sites/app/snippets", body: `{"server":"","root":""}`},
		{method: "PUT", path: "/api/v1/sites/app/redirect", body: `{"target":"https://x.example.com","status":301}`},
		{method: "GET", path: "/api/v1/sites/app/files?path=/"},
	} {
		req.cookie = c
		if rec := e.do(t, req); rec.Code != http.StatusConflict || errCode(t, rec) != "wrong_site_type" {
			t.Errorf("%s %s: %d %s", req.method, req.path, rec.Code, rec.Body)
		}
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("blog: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/proxy", cookie: c, body: `{"target":"http://10.0.0.5"}`}); rec.Code != http.StatusConflict {
		t.Errorf("proxy on blog: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/app", cookie: c}); rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}
