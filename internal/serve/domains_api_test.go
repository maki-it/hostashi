// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"strings"
	"testing"
)

func TestDomainChangeOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)
	ctx := t.Context()

	e.sites.EnableTLS(stubIssuer{})

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["old.example.com"],"tls":true}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	e.sites.RenewDue(ctx)

	put := func(body string) (int, string) {
		t.Helper()

		rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/domains", cookie: c, body: body})

		return rec.Code, rec.Body.String()
	}

	for _, body := range []string{
		`{"domains":["new.example.com"],"redirectDomains":["new.example.com"]}`,
		`{"domains":["new.example.com"],"redirectDomains":["bad domain"]}`,
		`{"domains":[]}`,
	} {
		if code, out := put(body); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", body, code, out)
		}
	}

	// HTTPS is live: the change waits for its certificate.
	code, out := put(`{"domains":["new.example.com"],"redirectDomains":["old.example.com"]}`)
	if code != http.StatusAccepted || !strings.Contains(out, `"domains":["old.example.com"],"redirectDomains":[],"pendingDomains":{"domains":["new.example.com"],"redirectDomains":["old.example.com"],`) {
		t.Fatalf("pending: %d %s", code, out)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites", cookie: c})
	if !strings.Contains(rec.Body.String(), `"pendingDomains":{"domains":["new.example.com"]`) {
		t.Fatalf("list without the pending change: %s", rec.Body)
	}

	// Cancel, and cancel again.
	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/shop/domains/pending", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"pendingDomains":null`) {
		t.Fatalf("cancel: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/shop/domains/pending", cookie: c})
	if rec.Code != http.StatusNotFound || errCode(t, rec) != "no_pending_change" {
		t.Fatalf("cancel twice: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/nope/domains/pending", cookie: c}); rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Errorf("unknown site: %d", rec.Code)
	}

	// Again, and the renewal loop finishes it.
	if code, out := put(`{"domains":["new.example.com"],"redirectDomains":["old.example.com"]}`); code != http.StatusAccepted {
		t.Fatalf("pending: %d %s", code, out)
	}

	e.sites.RenewDue(ctx)

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop", cookie: c})
	if !strings.Contains(rec.Body.String(), `"domains":["new.example.com"],"redirectDomains":["old.example.com"],"pendingDomains":null`) ||
		!strings.Contains(rec.Body.String(), `"active":true`) {
		t.Fatalf("after the certificate: %s", rec.Body)
	}

	ag.mu.Lock()
	ws := ag.web.Sites
	ag.mu.Unlock()

	if len(ws) != 1 || strings.Join(ws[0].Domains, " ") != "new.example.com old.example.com" || strings.Join(ws[0].RedirectDomains, " ") != "old.example.com" || !ws[0].TLS {
		t.Fatalf("agent got %+v", ws)
	}

	// Without redirectDomains they stay; waitForCertificate false changes
	// at once.
	code, out = put(`{"domains":["other.example.com"],"waitForCertificate":false}`)
	if code != http.StatusOK || !strings.Contains(out, `"domains":["other.example.com"],"redirectDomains":["old.example.com"],"pendingDomains":null`) {
		t.Fatalf("now: %d %s", code, out)
	}
}
