// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

func pgServer(name, version string) store.DBServer {
	return store.DBServer{ID: 7, Name: name, Status: store.DBServerStatus{Engine: enginePostgreSQL, Version: version}}
}

func TestPGClientChecks(t *testing.T) {
	newest := agentapi.PGClientNewest()
	list := []store.DBServer{
		{ID: 1, Name: "local", Status: store.DBServerStatus{Engine: "MariaDB", Version: "11.8.3"}},
		{ID: 5, Name: "maria", Status: store.DBServerStatus{Engine: "MariaDB", Version: "11.8.3"}},
		pgServer("old", "14.19"),
		pgServer("same", newest+".0"),
		pgServer("future", "99.1"),
		pgServer("unchecked", ""),
	}

	got := pgClientChecks(list, agentapi.PGClientStatus{Installed: []string{"17", newest}}, nil)
	want := map[string]string{"old": agentapi.DoctorOK, "same": agentapi.DoctorOK, "future": agentapi.DoctorFail}

	if len(got) != len(want) {
		t.Fatalf("checks: %+v", got)
	}

	for _, c := range got {
		if c.ID != "dbserver.pg_client" {
			t.Errorf("id %q", c.ID)
		}

		for name, status := range want {
			if strings.Contains(c.Summary, "server "+name+" ") && c.Status != status {
				t.Errorf("%s: %s %q", name, c.Status, c.Summary)
			}
		}

		if strings.Contains(c.Summary, "future") && !strings.Contains(c.Hint, "Update Hostashi") {
			t.Errorf("future server hint: %q", c.Hint)
		}
	}

	// An old client: the newest Hostashi knows is the fix.
	got = pgClientChecks([]store.DBServer{pgServer("same", newest+".2")}, agentapi.PGClientStatus{Installed: []string{"14"}}, nil)
	if len(got) != 1 || got[0].Status != agentapi.DoctorFail || !strings.Contains(got[0].Hint, "Install the PostgreSQL "+newest) {
		t.Fatalf("old client: %+v", got)
	}

	got = pgClientChecks([]store.DBServer{pgServer("a", "17.6"), pgServer("b", "")}, agentapi.PGClientStatus{Installed: []string{}}, nil)
	if len(got) != 2 || got[0].Status != agentapi.DoctorFail || !strings.Contains(got[0].Summary, "no PostgreSQL client") {
		t.Fatalf("no client: %+v", got)
	}

	got = pgClientChecks(list, agentapi.PGClientStatus{}, errors.New("down"))
	if len(got) != 1 || got[0].Status != agentapi.DoctorWarn {
		t.Fatalf("agent down: %+v", got)
	}
}

type pgStatusOnly struct {
	stubPGClient

	calls int
}

func (p *pgStatusOnly) Status(ctx context.Context) (agentapi.PGClientStatus, error) {
	p.calls++

	return p.stubPGClient.Status(ctx)
}

func TestPGClientCheckSkipsWithoutPostgreSQL(t *testing.T) {
	pg := &pgStatusOnly{}
	maria := []store.DBServer{{ID: 1, Name: "local"}, {ID: 5, Name: "m", Status: store.DBServerStatus{Engine: "MariaDB"}}}

	got, err := pgClientCheck(func(context.Context) ([]store.DBServer, error) { return maria, nil }, pg)(t.Context(), time.Now())
	if err != nil || len(got) != 0 || pg.calls != 0 {
		t.Fatalf("no PostgreSQL server: %+v %v, %d agent calls", got, err, pg.calls)
	}

	got, err = pgClientCheck(func(context.Context) ([]store.DBServer, error) { return []store.DBServer{pgServer("p", "17.6")}, nil }, pg)(t.Context(), time.Now())
	if err != nil || len(got) != 1 || got[0].Status != agentapi.DoctorFail || pg.calls != 1 {
		t.Fatalf("PostgreSQL server, no client: %+v %v", got, err)
	}

	if _, err := pgClientCheck(func(context.Context) ([]store.DBServer, error) { return nil, errors.New("db") }, pg)(t.Context(), time.Now()); err == nil {
		t.Fatal("store error swallowed")
	}
}
