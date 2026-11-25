// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"strings"
	"testing"
)

func TestSiteTitleOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","title":"Shop Müller","domains":["shop.example.com"]}`})
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"name":"shop","title":"Shop Müller"`) {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	put := func(body string) (int, string) {
		t.Helper()

		rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/title", cookie: c, body: body})

		return rec.Code, rec.Body.String()
	}

	if code, body := put(`{"title":"  Müller GmbH  "}`); code != http.StatusOK || !strings.Contains(body, `"name":"shop","title":"Müller GmbH"`) {
		t.Fatalf("set: %d %s", code, body)
	}

	for _, body := range []string{`{}`, `{"title":null}`, `{"title":"a\nb"}`, `{"title":"` + strings.Repeat("x", 65) + `"}`, `{"title":1}`} {
		if code, out := put(body); code != http.StatusUnprocessableEntity && code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", body, code, out)
		}
	}

	if code, body := put(`{"title":""}`); code != http.StatusOK || !strings.Contains(body, `"title":""`) {
		t.Fatalf("clear: %d %s", code, body)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/gone/title", cookie: c, body: `{"title":"x"}`}); rec.Code != http.StatusNotFound {
		t.Fatalf("missing site: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites", cookie: c})
	if !strings.Contains(rec.Body.String(), `"name":"shop","title":""`) {
		t.Fatalf("list: %s", rec.Body)
	}
}
