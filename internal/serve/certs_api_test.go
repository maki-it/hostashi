// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"strings"
	"testing"
)

func TestOwnCertificateOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.org"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	// A wrong pair: the agent's fixed text, 422.
	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/tls/certificate", cookie: c, body: `{"certificate":"bad","key":"k"}`})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "does not belong") {
		t.Fatalf("bad pair: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/tls/certificate", cookie: c, body: `{"key":"k"}`})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no certificate: %d %s", rec.Code, rec.Body)
	}

	big := strings.Repeat("A", 17<<10)

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/tls/certificate", cookie: c, body: `{"certificate":"good","key":"` + big + `"}`})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("big key: %d %s", rec.Code, rec.Body)
	}

	// No ACME client is needed for an own certificate.
	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/tls/certificate", cookie: c, body: `{"certificate":"good","key":"k"}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"enabled":true`) || !strings.Contains(rec.Body.String(), `"source":"custom"`) {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}

	if strings.Contains(rec.Body.String(), `"k"`) {
		t.Fatalf("key echoed: %s", rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/blog/tls/certificate", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"fingerprint":"ab12"`) {
		t.Fatalf("details: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/tls/csr", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "CERTIFICATE REQUEST") {
		t.Fatalf("csr: %d %s", rec.Code, rec.Body)
	}

	// Renew is for Let's Encrypt; without an ACME client there is none.
	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/tls/renew", cookie: c})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "tls_unavailable" {
		t.Fatalf("renew without issuer: %d %s", rec.Code, rec.Body)
	}

	e.sites.EnableTLS(stubIssuer{})

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/tls/renew", cookie: c})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "not_letsencrypt" {
		t.Fatalf("renew custom: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/tls/source", cookie: c, body: `{"source":"letsencrypt"}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":"letsencrypt","certSource":"custom"`) {
		t.Fatalf("source: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/tls/source", cookie: c, body: `{"source":"other"}`})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad source: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/tls/renew", cookie: c}); rec.Code != http.StatusAccepted {
		t.Fatalf("renew: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/blog/tls/renew", cookie: c})
	if rec.Code != http.StatusTooManyRequests || errCode(t, rec) != "renew_too_soon" {
		t.Fatalf("renew again: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog/tls/certificate", cookie: c})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "certificate_in_use" {
		t.Fatalf("delete with https on: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/tls", cookie: c, body: `{"enabled":false}`}); rec.Code != http.StatusOK {
		t.Fatalf("tls off: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "DELETE", path: "/api/v1/sites/blog/tls/certificate", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"certSource":""`) {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}
