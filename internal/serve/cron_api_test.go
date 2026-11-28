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
	"github.com/maki-it/hostashi/internal/agentrpc"
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

const stubRun = "0123456789abcdef0123456789abcdef"

func (s *stubCron) CronOutput(_ context.Context, site string, id int64, inv string) (agentapi.CronJobOutput, error) {
	switch inv {
	case "":
	case stubRun:
		return agentapi.CronJobOutput{Ran: true, Lines: []string{"earlier"}}, nil
	default:
		return agentapi.CronJobOutput{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "no such run"}
	}

	return agentapi.CronJobOutput{Ran: true, Lines: []string{"Hi!", site + " " + strconv.FormatInt(id, 10)}, Truncated: true}, nil
}

func (s *stubCron) CronRuns(context.Context, string, int64) (agentapi.CronJobRuns, error) {
	two := 2

	return agentapi.CronJobRuns{Runs: []agentapi.CronRunEntry{
		{Invocation: "fedcba9876543210fedcba9876543210", Start: 1795885672140810, Running: true},
		{Invocation: stubRun, Start: 1795880604914769, End: 1795880605100000, Result: "exit-code", ExitStatus: &two},
	}}, nil
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

	rec = e.do(t, reqOpts{method: "GET", path: path + "/output", cookie: c})
	if want := `{"ran":true,"lines":["Hi!","shop ` + strconv.FormatInt(job.ID, 10) + `"],"truncated":true}`; rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("output: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: path + "/runs", cookie: c})
	if want := `{"runs":[{"id":"fedcba9876543210fedcba9876543210","start":1795885672,"running":true},{"id":"` + stubRun + `","start":1795880604,"end":1795880605,"running":false,"result":"exit-code","exitStatus":2}]}`; rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("runs: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, reqOpts{method: "GET", path: path + "/output?run=" + stubRun, cookie: c})
	if want := `{"ran":true,"lines":["earlier"],"truncated":false}`; rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
		t.Fatalf("output of a run: %d %s", rec.Code, rec.Body)
	}

	// A run that is not among the last ones, and a malformed run id.
	for _, run := range []string{"ffffffffffffffffffffffffffffffff", "x", "0123456789ABCDEF0123456789ABCDEF"} {
		if rec := e.do(t, reqOpts{method: "GET", path: path + "/output?run=" + run, cookie: c}); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "run_not_found") {
			t.Errorf("run %q: %d %s", run, rec.Code, rec.Body)
		}
	}

	for _, p := range []string{"/api/v1/sites/shop/cron/999", "/api/v1/sites/shop/cron/x", "/api/v1/sites/shop/cron/0", "/api/v1/sites/shop/cron/999/output", "/api/v1/sites/shop/cron/999/runs"} {
		if rec := e.do(t, reqOpts{method: "GET", path: p, cookie: c}); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/sites/nope/cron", cookie: c}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown site: %d", rec.Code)
	}

	// cron:read does not write.
	read := e.createToken(t, c, `{"name":"r","scopes":["cron:read"],"expiresInDays":1}`)

	for _, p := range []string{path, path + "/output", path + "/runs"} {
		if rec := e.do(t, reqOpts{method: "GET", path: p, header: bearerHeader(read.Token)}); rec.Code != http.StatusOK {
			t.Errorf("read %s with cron:read: %d", p, rec.Code)
		}
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
