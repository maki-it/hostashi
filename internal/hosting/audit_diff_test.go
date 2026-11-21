// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// lastDiff returns the diff of the newest entry of an action.
func lastDiff(t *testing.T, st *store.Store, action string) map[string]store.Change {
	t.Helper()

	es, _, err := st.AuditEntries(t.Context(), store.AuditFilter{Action: action}, 0, 1)
	if err != nil || len(es) != 1 {
		t.Fatalf("%s: %v %v", action, es, err)
	}

	var d map[string]store.Change
	if err := json.Unmarshal(es[0].Diff, &d); err != nil {
		t.Fatal(err)
	}

	return d
}

func changed(c store.Change, old, now string) bool {
	return c.Old != nil && *c.Old == old && c.New != nil && *c.New == now
}

func TestAuditDiffs(t *testing.T) {
	s, _, st := newService(t)
	ctx := store.WithClientIP(t.Context(), "192.0.2.44")

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}, PHP: "8.3"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetPHP(ctx, 1, "shop", "8.4"); err != nil {
		t.Fatal(err)
	}

	if d := lastDiff(t, st, "site.php"); len(d) != 1 || !changed(d["php"], "8.3", "8.4") {
		t.Errorf("php: %+v", d)
	}

	if _, err := s.SetDomains(ctx, 1, "shop", []string{"shop.example.com", "www.shop.example.com"}); err != nil {
		t.Fatal(err)
	}

	if d := lastDiff(t, st, "site.domains"); !changed(d["domains"], "shop.example.com", "shop.example.com www.shop.example.com") {
		t.Errorf("domains: %+v", d)
	}

	es, _, err := st.AuditEntries(t.Context(), store.AuditFilter{Action: "site.domains"}, 0, 1)
	if err != nil || es[0].IP != "192.0.2.44" {
		t.Errorf("ip: %+v %v", es, err)
	}

	// A cron job's command never reaches the log, only that it changed.
	ag := &fakeCron{applied: map[string][]agentapi.CronJob{}}
	s.EnableCron(ag)

	j, err := s.CreateCronJob(ctx, 1, "shop", CronInput{Schedule: "@daily", Command: "curl -H 'Token: s3cret' https://x"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.UpdateCronJob(ctx, 1, "shop", j.ID, CronInput{Schedule: "@hourly", Command: "curl -H 'Token: n3w' https://x"}); err != nil {
		t.Fatal(err)
	}

	d := lastDiff(t, st, "cron.update")
	if !changed(d["schedule"], "@daily", "@hourly") || d["command"].New == nil || strings.Contains(*d["command"].New, "n3w") {
		t.Errorf("cron: %+v", d)
	}

	var n int
	if err := st.RawCount(t.Context(), `SELECT count(*) FROM audit_log WHERE diff LIKE '%s3cret%' OR diff LIKE ?`, "%n3w%", &n); err != nil || n != 0 {
		t.Errorf("command in a diff: %d %v", n, err)
	}

	// An update that changes nothing records an empty diff.
	if _, err := s.UpdateCronJob(ctx, 1, "shop", j.ID, CronInput{Schedule: "@hourly", Command: "curl -H 'Token: n3w' https://x"}); err != nil {
		t.Fatal(err)
	}

	if d := lastDiff(t, st, "cron.update"); len(d) != 0 {
		t.Errorf("no change: %+v", d)
	}
}
