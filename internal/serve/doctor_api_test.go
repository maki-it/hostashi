// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/doctor"
)

type stubDoctor struct {
	rep agentapi.DoctorReport
	err error
}

func (d *stubDoctor) run(context.Context) (agentapi.DoctorReport, error) { return d.rep, d.err }

func TestDoctor(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)

	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/doctor"}); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}

	// No checks at all is still an answer.
	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/doctor", cookie: c})

	var empty doctorJSON
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &empty) != nil || empty.Status != "ok" || empty.Checks == nil {
		t.Fatalf("empty: %d %s", rec.Code, rec.Body)
	}

	e.doctor.rep = agentapi.DoctorReport{Checks: []agentapi.DoctorCheck{
		{ID: "system.os", Status: agentapi.DoctorOK, Summary: "Debian 13 is supported"},
		{ID: "system.quota", Status: agentapi.DoctorWarn, Summary: "no project quota", Hint: "Run the installer again."},
		{ID: "site.tls", Site: "shop", Status: agentapi.DoctorFail, Summary: "certificate expired"},
	}}

	rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/doctor", cookie: c})

	var got doctorJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	if got.Status != "fail" || len(got.Checks) != 3 || got.Checks[2].Site != "shop" || got.CheckedAt.IsZero() ||
		got.Counts["ok"] != 1 || got.Counts["warn"] != 1 || got.Counts["fail"] != 1 {
		t.Errorf("report: %+v", got)
	}

	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("the report must not be cached")
	}

	// ?lang=de gives the German texts of checks with a text key; one without
	// stays English.
	e.doctor.rep.Checks = append(e.doctor.rep.Checks,
		doctor.New(agentapi.DoctorWarn, "system.quota", "quota.off", "quota.off.hint", doctor.P{"path": "/srv"}))

	for lang, want := range map[string]string{"": "/srv: no project quota, sites get no disk limit", "en": "/srv: no project quota, sites get no disk limit", "de": "/srv: kein Project Quota, Sites bekommen kein Speicherlimit"} {
		rec = e.do(t, reqOpts{method: "GET", path: "/api/v1/doctor?lang=" + lang, cookie: c})

		var r doctorJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil || len(r.Checks) != 4 {
			t.Fatalf("lang %q: %d %s", lang, rec.Code, rec.Body)
		}

		if r.Checks[3].Summary != want || r.Checks[0].Summary != "Debian 13 is supported" {
			t.Errorf("lang %q: %q, %q", lang, r.Checks[3].Summary, r.Checks[0].Summary)
		}

		if lang == "de" && !strings.HasPrefix(r.Checks[3].Hint, "Führ den Installer") {
			t.Errorf("German hint %q", r.Checks[3].Hint)
		}
	}

	// A token needs doctor:read.
	other := e.createToken(t, c, `{"name":"o","scopes":["sites:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/doctor", header: bearerHeader(other.Token)}); rec.Code != http.StatusForbidden {
		t.Errorf("sites:read: %d", rec.Code)
	}

	good := e.createToken(t, c, `{"name":"d","scopes":["doctor:read"],"expiresInDays":1}`)
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/doctor", header: bearerHeader(good.Token)}); rec.Code != http.StatusOK {
		t.Errorf("doctor:read: %d", rec.Code)
	}

	e.doctor.err = errors.New("agent gone")
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/doctor", cookie: c}); rec.Code != http.StatusServiceUnavailable || errCode(t, rec) != "agent_unavailable" {
		t.Errorf("agent down: %d %s", rec.Code, rec.Body)
	}

	e.doctor.err = context.DeadlineExceeded
	if rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/doctor", cookie: c}); rec.Code != http.StatusGatewayTimeout || errCode(t, rec) != "agent_timeout" {
		t.Errorf("timeout: %d %s", rec.Code, rec.Body)
	}
}
