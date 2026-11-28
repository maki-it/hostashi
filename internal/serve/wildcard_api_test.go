// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
)

// A redirect site takes a wildcard after its primary domain; GET lists
// the names below it that other sites serve (ADR-0075).
func TestWildcardSiteOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	// In order: www must exist before the redirect to it.
	for _, tc := range []struct {
		body string
		want int
	}{
		{`{"name":"blog","domains":["blog.example.com","*.blog.example.com"]}`, http.StatusUnprocessableEntity},
		{`{"name":"fwd","domains":["*.example.com"],"type":"redirect","redirect":{"target":"https://example.org"}}`, http.StatusUnprocessableEntity},
		{`{"name":"fwd","domains":["example.com","*.example.com"],"type":"redirect","redirect":{"target":"https://www.example.com"}}`, http.StatusUnprocessableEntity},
		{`{"name":"fwd","domains":["example.com","*.example.com"],"tls":true,"type":"redirect","redirect":{"target":"https://x.org"}}`, http.StatusUnprocessableEntity},
		{`{"name":"www","domains":["www.example.com"]}`, http.StatusCreated},
		{`{"name":"fwd","domains":["example.com","*.example.com"],"type":"redirect","redirect":{"target":"https://www.example.com"}}`, http.StatusCreated},
	} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: tc.body}); rec.Code != tc.want {
			t.Errorf("%s: %d %s", tc.body, rec.Code, rec.Body)
		}
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/fwd", cookie: c})

	var site siteJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &site); err != nil || rec.Code != http.StatusOK ||
		!reflect.DeepEqual(site.Domains, []string{"example.com", "*.example.com"}) || !reflect.DeepEqual(site.ServedElsewhere, []string{"www.example.com"}) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
}
