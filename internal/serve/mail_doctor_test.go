// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

func TestDoctorMailCheck(t *testing.T) {
	e := newAPI(t)
	c := e.adminCookie(t)
	e.doctor.rep = agentapi.DoctorReport{Checks: []agentapi.DoctorCheck{
		{ID: "system.os", Status: agentapi.DoctorOK, Summary: "Debian 13"},
		{ID: "site.home", Site: "shop", Status: agentapi.DoctorOK, Summary: "ok"},
	}}

	ids := func() (string, string) {
		rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/doctor", cookie: c})
		if rec.Code != http.StatusOK {
			t.Fatalf("%d %s", rec.Code, rec.Body)
		}

		var out doctorJSON
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}

		var list []string
		for _, ch := range out.Checks {
			list = append(list, ch.ID)
		}

		return strings.Join(list, ","), out.Status
	}

	// Email off: no check.
	if got, _ := ids(); got != "system.os,site.home" {
		t.Fatalf("off: %s", got)
	}

	e.putMail(t, c, mailBody)

	if got, status := ids(); got != "system.os,site.home,panel.mail" || status != agentapi.DoctorOK {
		t.Fatalf("on: %s %s", got, status)
	}

	id, err := e.st.EnqueueMail(t.Context(), store.OutboxMail{CreatedAt: time.Now(), Recipient: "kim@example.com", Template: "invitation", Category: store.MailAccount, Body: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}

	if err := e.st.MailFailed(t.Context(), id, time.Now(), "550 5.1.1 no such user", true); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, reqOpts{method: "GET", path: "/api/v1/doctor", cookie: c})
	if !strings.Contains(rec.Body.String(), "1 email could not be sent in the last 24 hours (last: 550 5.1.1 no such user)") || !strings.Contains(rec.Body.String(), `"status":"warn"`) {
		t.Fatalf("failed: %s", rec.Body)
	}
}

func TestWithDatabaseChecks(t *testing.T) {
	if withDatabaseChecks(nil, nil) != nil {
		t.Fatal("no doctor: want nil")
	}

	agent := agentapi.DoctorReport{Checks: []agentapi.DoctorCheck{{ID: "system.os", Status: agentapi.DoctorOK}}}
	run := func(context.Context) (agentapi.DoctorReport, error) { return agent, nil }
	dbErr := error(nil)
	db := func(context.Context, time.Time) ([]agentapi.DoctorCheck, error) {
		return []agentapi.DoctorCheck{{ID: "site.database", Site: "shop", Status: agentapi.DoctorWarn}}, dbErr
	}

	rep, err := withDatabaseChecks(run, db)(t.Context())
	if err != nil || len(rep.Checks) != 2 || rep.Checks[1].ID != "site.database" {
		t.Fatalf("%+v %v", rep, err)
	}

	// A failed measurement keeps the agent's report.
	dbErr = errors.New("agent down")

	rep, err = withDatabaseChecks(run, db)(t.Context())
	if err != nil || len(rep.Checks) != 1 {
		t.Fatalf("measurement failed: %+v %v", rep, err)
	}
}
