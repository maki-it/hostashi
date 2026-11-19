// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/cloudpanel/snippetdraft"
)

func TestSnippetsOverHTTP(t *testing.T) {
	ag := &stubAgent{}
	e := newAPIWithAgent(t, ag)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/snippets", cookie: c})
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"server":"","root":""}` {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/snippets", cookie: c, body: `{"server":"add_header X-A b;","root":"expires 1h;"}`})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"root":"expires 1h;"`) || !strings.Contains(rec.Body.String(), "updatedAt") {
		t.Fatalf("set: %d %s", rec.Code, rec.Body)
	}

	if s := ag.web.Sites; len(s) != 1 || s[0].Snippets == nil || s[0].Snippets.Server != "add_header X-A b;" {
		t.Fatalf("web state: %+v", s)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/snippets", cookie: c, body: `{"server":"listen 81;","root":""}`})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rejected: %d %s", rec.Code, rec.Body)
	}

	var bad snippetErrorJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &bad); err != nil || bad.Error.Code != "snippet_rejected" || len(bad.Problems.Server) != 1 || bad.Problems.Server[0].Line != 1 {
		t.Fatalf("problems: %+v %v %s", bad, err, rec.Body)
	}

	for _, body := range []string{`{"server":"x"}`, `{}`, `{"server":"","root":"","extra":1}`} {
		if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/snippets", cookie: c, body: body}); rec.Code == http.StatusOK {
			t.Errorf("%s accepted", body)
		}
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/nope/snippets", cookie: c, body: `{"server":"","root":""}`}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: %d", rec.Code)
	}

	// A token needs the high-risk scope by name; sites:write is not enough.
	tok := e.createToken(t, c, `{"name":"deploy","scopes":["sites:read","sites:write"]}`)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/snippets", header: bearerHeader(tok.Token)}); rec.Code != http.StatusOK {
		t.Errorf("read with sites:read: %d", rec.Code)
	}

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/snippets", header: bearerHeader(tok.Token), body: `{"server":"","root":""}`}); rec.Code != http.StatusForbidden {
		t.Errorf("write with sites:write: %d %s", rec.Code, rec.Body)
	}

	tok = e.createToken(t, c, `{"name":"snip","scopes":["sites.snippets:write"]}`)

	if rec := e.do(t, reqOpts{method: "PUT", path: "/api/v1/sites/shop/snippets", header: bearerHeader(tok.Token), body: `{"server":"","root":""}`}); rec.Code != http.StatusOK {
		t.Errorf("write with sites.snippets:write: %d %s", rec.Code, rec.Body)
	}
}

func TestSnippetDraftOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)

	rec := e.do(t, reqOpts{method: "POST", path: reportsPath, cookie: c, body: scanReportJSON(t)})

	var rep cloudPanelReportJSON
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &rep) != nil {
		t.Fatalf("upload: %d %s", rec.Code, rec.Body)
	}

	draft := reportsPath + "/" + rep.ID + "/sites/shop/snippets-draft"

	// Not created here yet: no draft, and no snippet text in the answer.
	rec = e.do(t, reqOpts{method: "GET", path: draft, cookie: c})
	if rec.Code != http.StatusConflict || errCode(t, rec) != "site_missing" || strings.Contains(rec.Body.String(), "s3cr3t") {
		t.Fatalf("missing site: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: reportsPath + "/" + rep.ID + "/sites/shop", cookie: c, body: `{"reviewed":true}`}); rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: draft, cookie: c})
	if rec.Code != http.StatusOK {
		t.Fatalf("draft: %d %s", rec.Code, rec.Body)
	}

	var d snippetdraft.Result
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil || !strings.Contains(d.Server, `add_header X-Secret s3cr3t-in-snippet;`) || len(d.Items) != 1 || d.Items[0].Action != snippetdraft.Kept {
		t.Fatalf("draft: %+v %v", d, err)
	}

	// Nothing was saved.
	if sn, err := e.sites.Snippets(t.Context(), "shop"); err != nil || !sn.Empty() {
		t.Fatalf("draft saved: %+v %v", sn, err)
	}

	if rec := e.do(t, reqOpts{method: "GET", path: reportsPath + "/" + rep.ID + "/sites/nobody/snippets-draft", cookie: c}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: %d", rec.Code)
	}

	tok := e.createToken(t, c, `{"name":"r","scopes":["sites:read"]}`)
	if rec := e.do(t, reqOpts{method: "GET", path: draft, header: bearerHeader(tok.Token)}); rec.Code != http.StatusForbidden {
		t.Errorf("without migrations:read: %d", rec.Code)
	}
}
