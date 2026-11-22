// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/servermon"
	"github.com/maki-it/hostashi/internal/store"
)

func subjects(ob *fakeOutbox) []string {
	out := []string{}
	for _, m := range ob.sent {
		out = append(out, m.To+" "+m.Subject)
	}

	return out
}

func TestServiceAlerts(t *testing.T) {
	n, st, ob := newNotifier(t)
	st.people[3].Prefs["services"] = true // uli, a user of shop

	a := &ServiceAlerts{Notifier: n}
	ctx := t.Context()
	at := time.Unix(2_000_000_000, 0).UTC()

	up := func(active string) []agentapi.ServiceState {
		return []agentapi.ServiceState{
			{ID: "nginx", Installed: true, Active: active},
			{ID: "php", Site: "shop", Installed: true, Active: "active"},
		}
	}
	poll := func(states []agentapi.ServiceState, crashes map[string]int, evs ...store.ServiceEvent) {
		a.ServicesPolled(ctx, servermon.ServicePoll{At: at, States: states, Events: evs, Crashes: crashes})
		at = at.Add(time.Minute)
	}

	// nginx fails: admins only, once.
	poll(up("failed"), nil, store.ServiceEvent{At: at, Service: "nginx", Kind: store.ServiceEventFailed, Detail: "exit-code"})
	poll(up("failed"), nil)

	if got := strings.Join(subjects(ob), "|"); got != "kim@example.com Service down: nginx (web server)|ada@example.com Service down: nginx (web server)" {
		t.Fatalf("down: %s", got)
	}

	body := ob.sent[0].Body
	for _, want := range []string{"nginx (web server) stopped on", "Result: exit-code", "#/services"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}

	// Back after two active polls, not after one; a relapse in between
	// starts the count again.
	ob.sent = nil

	poll(up("active"), nil)
	poll(up("activating"), nil)
	poll(up("active"), nil)

	if len(ob.sent) != 0 {
		t.Fatalf("back too early: %v", subjects(ob))
	}

	poll(up("active"), nil)

	if got := strings.Join(subjects(ob), "|"); got != "kim@example.com Service running again: nginx (web server)|ada@example.com Service running again: nginx (web server)" {
		t.Fatalf("back: %s", got)
	}

	if !strings.Contains(ob.sent[0].Body, "The problem started on") {
		t.Errorf("back body:\n%s", ob.sent[0].Body)
	}

	// PHP of shop crashes: two restarts send nothing, three do, and the
	// user with a grant gets it too.
	ob.sent = nil
	crash := store.ServiceEvent{At: at, Service: "php", Site: "shop", Kind: store.ServiceEventCrashRestart, Restarts: 1}

	poll(up("active"), map[string]int{"php/shop": 2}, crash)

	if len(ob.sent) != 0 {
		t.Fatalf("two crashes: %v", subjects(ob))
	}

	poll(up("active"), map[string]int{"php/shop": 3}, crash)
	poll(up("active"), map[string]int{"php/shop": 4}, crash) // still crashing: no second email, no all-clear

	if got := strings.Join(subjects(ob), "|"); got != "kim@example.com Service keeps crashing: PHP of shop|ada@example.com Service keeps crashing: PHP of shop|uli@example.com Service keeps crashing: PHP of shop" {
		t.Fatalf("crashing: %s", got)
	}

	if body := ob.sent[0].Body; !strings.Contains(body, "PHP of the site shop crashed 3 times in the last 15 minutes") || !strings.Contains(body, "#/sites/shop") {
		t.Errorf("crashing body:\n%s", body)
	}

	ob.sent = nil

	poll(up("active"), nil)
	poll(up("active"), nil)

	if len(ob.sent) != 3 || !strings.HasPrefix(ob.sent[2].Subject, "Service running again: PHP of shop") {
		t.Fatalf("crash back: %v", subjects(ob))
	}

	// A site deleted during an outage ends it without an email.
	ob.sent = nil

	poll(up("active"), nil, store.ServiceEvent{At: at, Service: "php", Site: "shop", Kind: store.ServiceEventFailed})
	poll([]agentapi.ServiceState{{ID: "nginx", Installed: true, Active: "active"}}, nil)
	poll(up("active"), nil)
	poll(up("active"), nil)

	if len(ob.sent) != 3 || strings.Contains(strings.Join(subjects(ob), "|"), "running again") {
		t.Fatalf("deleted site: %v", subjects(ob))
	}

	if len(a.open) != 0 {
		t.Fatalf("open outages left: %+v", a.open)
	}
}

func TestAgentUnreachable(t *testing.T) {
	n, _, ob := newNotifier(t)
	a := &ServiceAlerts{Notifier: n}
	ctx := t.Context()
	at := time.Unix(2_000_000_000, 0).UTC()
	err := errors.New("dial unix /run/hostashi/agent.sock: connect: connection refused\nmore")

	// Two failures and an answer: nothing.
	a.AgentUnreachable(ctx, at, err)
	a.AgentUnreachable(ctx, at.Add(time.Minute), err)
	a.ServicesPolled(ctx, servermon.ServicePoll{At: at.Add(2 * time.Minute)})

	if len(ob.sent) != 0 {
		t.Fatalf("early: %v", subjects(ob))
	}

	for i := range 5 {
		a.AgentUnreachable(ctx, at.Add(time.Duration(10+i)*time.Minute), err)
	}

	if got := strings.Join(subjects(ob), "|"); got != "kim@example.com The Hostashi agent does not answer|ada@example.com The Hostashi agent does not answer" {
		t.Fatalf("down: %s", got)
	}

	body := ob.sent[0].Body
	if !strings.Contains(body, "connection refused") || strings.Contains(body, "more") || !strings.Contains(body, when(at.Add(10*time.Minute))) {
		t.Errorf("body:\n%s", body)
	}

	ob.sent = nil

	a.ServicesPolled(ctx, servermon.ServicePoll{At: at.Add(time.Hour)})
	a.ServicesPolled(ctx, servermon.ServicePoll{At: at.Add(time.Hour + time.Minute)})

	if got := strings.Join(subjects(ob), "|"); got != "kim@example.com The Hostashi agent answers again|ada@example.com The Hostashi agent answers again" {
		t.Fatalf("back: %s", got)
	}
}
