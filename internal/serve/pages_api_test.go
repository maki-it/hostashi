// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
)

// form builds a multipart body; a field "file" is sent as a file part.
func form(t *testing.T, fields map[string]string, filename string) (string, string) {
	t.Helper()

	var buf bytes.Buffer

	mw := multipart.NewWriter(&buf)

	for k, v := range fields {
		var err error

		if k == "file" {
			w, ferr := mw.CreateFormFile("file", filename)
			if ferr != nil {
				t.Fatal(ferr)
			}

			_, err = w.Write([]byte(v))
		} else {
			err = mw.WriteField(k, v)
		}

		if err != nil {
			t.Fatal(err)
		}
	}

	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	return buf.String(), mw.FormDataContentType()
}

func TestStatusPagesOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/status-pages", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"builtin-maintenance"`) || !strings.Contains(rec.Body.String(), `"id":"builtin-parking"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	body, ct := form(t, map[string]string{"name": "Summer", "kind": "maintenance", "file": "<h1>Back soon</h1>"}, "summer.html")

	rec = e.do(t, reqOpts{method: "POST", path: "/api/v1/status-pages", cookie: c, body: body, header: map[string]string{"Content-Type": ct}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	var page pageJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil || page.ID != "p1" || len(page.Files) != 1 || page.Files[0].Path != "index.html" || len(page.SHA256) != 64 {
		t.Fatalf("%+v %v", page, err)
	}

	for name, f := range map[string]map[string]string{
		"php file":  {"name": "x", "kind": "maintenance", "file": "<?php"},
		"no file":   {"name": "x", "kind": "maintenance"},
		"bad kind":  {"name": "x", "kind": "other", "file": "x"},
		"long name": {"name": strings.Repeat("x", 65), "kind": "maintenance", "file": "x"},
	} {
		fn := "x.html"
		if name == "php file" {
			fn = "x.php"
		}

		b, ct := form(t, f, fn)
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/status-pages", cookie: c, body: b, header: map[string]string{"Content-Type": ct}}); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}

	big, ct := form(t, map[string]string{"name": "x", "kind": "maintenance", "file": strings.Repeat("x", 3<<20)}, "x.html")
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/status-pages", cookie: c, body: big, header: map[string]string{"Content-Type": ct}}); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("too large: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/status-pages", cookie: c, body: `{"name":"x"}`}); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("json body: %d", rec.Code)
	}

	// Maintenance on a site with the uploaded page.
	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"blog","domains":["blog.example.org"]}`}); rec.Code != http.StatusCreated ||
		!strings.Contains(rec.Body.String(), `"maintenance":{"enabled":false,"page":"builtin-maintenance","allowIps":[]}`) {
		t.Fatalf("create site: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/maintenance", cookie: c, body: `{"enabled":true,"page":"p1","allowIps":["192.0.2.10"]}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"maintenance":{"enabled":true,"page":"p1","allowIps":["192.0.2.10"]}`) ||
		ag.web.Sites[0].Maintenance == nil || ag.web.Sites[0].Maintenance.Digest != page.SHA256 {
		t.Fatalf("maintenance: %d %s %+v", rec.Code, rec.Body, ag.web.Sites)
	}

	for _, b := range []string{`{}`, `{"enabled":true,"allowIps":["10.1.2.3/8"]}`, `{"enabled":true,"page":"p9"}`} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/maintenance", cookie: c, body: b}); rec.Code < 400 {
			t.Errorf("%s: %d", b, rec.Code)
		}
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/status-pages/p1", cookie: c}); rec.Code != http.StatusConflict || errCode(t, rec) != "page_in_use" {
		t.Errorf("delete in use: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/status-pages/builtin-maintenance", cookie: c}); rec.Code != http.StatusConflict || errCode(t, rec) != "page_builtin" {
		t.Errorf("delete built-in: %d %s", rec.Code, rec.Body)
	}

	body, ct = form(t, map[string]string{"name": "Renamed"}, "")
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/status-pages/p1", cookie: c, body: body, header: map[string]string{"Content-Type": ct}}); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"name":"Renamed"`) || !strings.Contains(rec.Body.String(), `"sites":["blog"]`) {
		t.Errorf("rename: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/blog/maintenance", cookie: c, body: `{"enabled":false}`}); rec.Code != http.StatusOK {
		t.Fatalf("off: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/status-pages/p1", cookie: c}); rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/status-pages/p1", cookie: c}); rec.Code != http.StatusNotFound {
		t.Errorf("get deleted: %d", rec.Code)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/auth/me", cookie: c})
	if !strings.Contains(rec.Body.String(), `"clientIp":"192.0.2.10"`) {
		t.Errorf("me: %s", rec.Body)
	}
}

func TestParkedDomainsOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	// Without an ACME client HTTPS is refused; it is on by default.
	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/parked-domains/soon.example.net", cookie: c, body: `{}`}); rec.Code != http.StatusConflict || errCode(t, rec) != "tls_unavailable" {
		t.Fatalf("default https: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/parked-domains/soon.example.net", cookie: c, body: `{"tls":false}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"domain":"soon.example.net","page":"builtin-parking","tls":{"enabled":false,"active":false,"notAfter":null,"source":"letsencrypt","certSource":"","trusted":true,"renewPending":false}`) ||
		len(ag.web.Parked) != 1 {
		t.Fatalf("park: %d %s %+v", rec.Code, rec.Body, ag.web.Parked)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/parked-domains/bad;domain", cookie: c, body: `{"tls":false}`}); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad domain: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/parked-domains", cookie: c}); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"parkedDomains":[{"domain":"soon.example.net"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/status-pages/builtin-parking", cookie: c}); !strings.Contains(rec.Body.String(), `"parked":["soon.example.net"]`) {
		t.Errorf("page: %s", rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: "/api/v1/parked-domains/soon.example.net", cookie: c}); rec.Code != http.StatusNoContent || len(ag.web.Parked) != 0 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/parked-domains/soon.example.net", cookie: c}); rec.Code != http.StatusNotFound || errCode(t, rec) != "parked_not_found" {
		t.Errorf("get deleted: %d %s", rec.Code, rec.Body)
	}
}
