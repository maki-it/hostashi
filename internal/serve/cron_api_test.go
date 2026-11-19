// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type stubCron struct {
	applied map[string][]agentapi.CronJob
	runs    int
}

func (s *stubCron) CronApply(_ context.Context, req agentapi.CronApplyRequest) error {
	s.applied[req.Site] = req.Jobs

	return nil
}

func (s *stubCron) CronRun(context.Context, string, int64) error {
	s.runs++

	return nil
}

func (s *stubCron) CronStatus(_ context.Context, site string) (agentapi.CronSiteStatus, error) {
	exit := 0
	out := agentapi.CronSiteStatus{Site: site, Jobs: []agentapi.CronJobStatus{}}

	for _, j := range s.applied[site] {
		out.Jobs = append(out.Jobs, agentapi.CronJobStatus{ID: j.ID, Result: "success", ExitStatus: &exit, LastStart: 1700000000, Next: 1700003600})
	}

	return out, nil
}

func TestCronOverHTTP(t *testing.T) {
	e := newAPIWithAgent(t, &stubAgent{})
	cr := &stubCron{applied: map[string][]agentapi.CronJob{}}
	e.sites.EnableCron(cr)

	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites", cookie: c, body: `{"name":"shop","domains":["shop.example.com"]}`}); rec.Code != http.StatusCreated {
		t.Fatalf("site: %d %s", rec.Code, rec.Body)
	}

	rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/cron", cookie: c, body: `{"schedule":"*/5 * * * *","command":"php wp-cron.php"}`})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}

	var job cronJobJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil || job.ID == 0 || job.Nice != nil {
		t.Fatalf("%s %v", rec.Body, err)
	}

	path := "/api/v1/sites/shop/cron/" + strconv.FormatInt(job.ID, 10)

	for body, code := range map[string]string{
		`{"schedule":"@reboot","command":"x"}`:          "invalid_argument",
		`{"schedule":"@daily","command":"a\nb"}`:        "invalid_argument",
		`{"schedule":"@daily","command":"x","nice":20}`: "invalid_argument",
		`{"schedule":"@daily","command":"x","bogus":1}`: "bad_request",
	} {
		rec := e.do(t, reqOpts{method: "POST", path: "/api/v1/sites/shop/cron", cookie: c, body: body})
		if rec.Code < 400 || errCode(t, rec) != code {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/shop/cron", cookie: c})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"statusKnown":true`) || !strings.Contains(rec.Body.String(), `"result":"success"`) ||
		!strings.Contains(rec.Body.String(), `"exitStatus":0`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "PUT", path: path, cookie: c, body: `{"schedule":"@daily","command":"php wp-cron.php","paused":true,"nice":15}`})
	if rec.Code != http.StatusOK || !cr.applied["shop"][0].Paused || *cr.applied["shop"][0].Nice != 15 {
		t.Fatalf("pause: %d %s", rec.Code, rec.Body)
	}

	if rec := e.do(t, reqOpts{method: "POST", path: path + "/run", cookie: c}); rec.Code != http.StatusAccepted || cr.runs != 1 {
		t.Fatalf("run: %d %s", rec.Code, rec.Body)
	}

	for _, p := range []string{"/api/v1/sites/shop/cron/999", "/api/v1/sites/shop/cron/x", "/api/v1/sites/shop/cron/0"} {
		if rec := e.do(t, reqOpts{method: "GET", path: p, cookie: c}); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/nope/cron", cookie: c}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: %d", rec.Code)
	}

	// cron:read does not write.
	read := e.createToken(t, c, `{"name":"r","scopes":["cron:read"],"expiresInDays":1}`)

	if rec := e.do(t, reqOpts{method: "GET", path: path, header: bearerHeader(read.Token)}); rec.Code != http.StatusOK {
		t.Errorf("read with cron:read: %d", rec.Code)
	}

	for _, o := range []reqOpts{
		{method: "POST", path: "/api/v1/sites/shop/cron", body: `{"schedule":"@daily","command":"x"}`},
		{method: "PUT", path: path, body: `{"schedule":"@daily","command":"x"}`},
		{method: "POST", path: path + "/run"},
		{method: "DELETE", path: path},
	} {
		o.header = bearerHeader(read.Token)
		if rec := e.do(t, o); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with cron:read: %d", o.method, o.path, rec.Code)
		}
	}

	if rec := e.do(t, reqOpts{method: "DELETE", path: path, cookie: c}); rec.Code != http.StatusNoContent || len(cr.applied["shop"]) != 0 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
}
