// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/store"
)

// The list of sites marks open virus finds and copies by site name.
func TestSiteListMarks(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	c := e.adminCookie(t)
	ctx := t.Context()

	for _, body := range []string{`{"name":"blog","domains":["blog.example.com"]}`, `{"name":"stage","domains":["stage.example.com"]}`, `{"name":"docs","domains":["docs.example.com"]}`} {
		if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: body}); rec.Code != http.StatusCreated {
			t.Fatalf("site: %d %s", rec.Code, rec.Body)
		}
	}

	blog, err := e.st.SiteByName(ctx, "blog")
	if err != nil {
		t.Fatal(err)
	}

	stage, err := e.st.SiteByName(ctx, "stage")
	if err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	sha := "0000000000000000000000000000000000000000000000000000000000000000"

	if _, _, err := e.st.RecordRealtimeFindings(ctx, blog.ID, []store.FoundFile{{Path: "a.php", Signature: "S", SHA256: sha, Size: 1}}, now); err != nil {
		t.Fatal(err)
	}

	if err := e.st.CreateSiteClone(ctx, store.SiteClone{SiteID: stage.ID, SourceID: blog.ID, Staging: true, StartedBy: 1}, now); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites", cookie: c})

	var got siteListJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &got) != nil {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	want := map[string]siteMarksJSON{"blog": {OpenFindings: 1}, "stage": {CloneOf: "blog", Staging: true}}
	if len(got.Marks) != len(want) || got.Marks["blog"] != want["blog"] || got.Marks["stage"] != want["stage"] {
		t.Fatalf("marks: %+v", got.Marks)
	}
}
