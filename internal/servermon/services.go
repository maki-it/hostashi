// SPDX-License-Identifier: AGPL-3.0-or-later

package servermon

import (
	"context"
	"errors"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

func key(id, site string) string {
	if site == "" {
		return id
	}

	return id + "/" + site
}

// Changes compares two states of one unit and returns the events between
// them. The first state of a unit (prev nil) gives none.
func Changes(prev *agentapi.ServiceState, cur agentapi.ServiceState, at time.Time) []store.ServiceEvent {
	if prev == nil || !cur.Installed {
		return nil
	}

	var out []store.ServiceEvent

	ev := func(kind, detail string, restarts int) {
		out = append(out, store.ServiceEvent{At: at, Service: cur.ID, Site: cur.Site, Kind: kind, Detail: detail, Restarts: restarts})
	}

	startedChanged := !cur.StartedAt.IsZero() && !cur.StartedAt.Equal(prev.StartedAt)

	switch {
	case cur.NRestarts > prev.NRestarts:
		ev(store.ServiceEventCrashRestart, cur.Result, int(cur.NRestarts-prev.NRestarts))
	case startedChanged:
		// A start by someone else (or a manual start, which resets the
		// counter [U]); crash restarts in between are not counted then.
		ev(store.ServiceEventStarted, "", 0)
	}

	if cur.Active == "failed" && prev.Active != "failed" {
		ev(store.ServiceEventFailed, cur.Result, 0)
	}

	return out
}

// PollServices reads every unit's state and stores the events.
func (m *Monitor) PollServices(ctx context.Context) {
	ctx, cancel := context.WithTimeout(ctx, ServicesEvery)
	defer cancel()

	states, err := m.Agent.Services(ctx, agentapi.ServicesStatusRequest{})
	if err != nil {
		if !errors.Is(err, context.Canceled) {
			m.log().WarnContext(ctx, "servermon: service states", "err", err)
		}

		return
	}

	m.record(ctx, states)
}

func (m *Monitor) record(ctx context.Context, states []agentapi.ServiceState) {
	at := m.now()

	m.mu.Lock()

	if m.units == nil {
		m.units = map[string]agentapi.ServiceState{}
	}

	var events []store.ServiceEvent

	next := make(map[string]agentapi.ServiceState, len(states))

	for _, st := range states {
		k := key(st.ID, st.Site)

		var prev *agentapi.ServiceState
		if p, ok := m.units[k]; ok {
			prev = &p
		}

		for _, e := range Changes(prev, st, at) {
			if e.Kind == store.ServiceEventStarted && at.Sub(m.actions[k]) < actionQuiet {
				continue // the admin's own action, already recorded
			}

			events = append(events, e)
		}

		next[k] = st
	}

	m.units, m.lastSeen = next, at
	m.mu.Unlock()

	for _, e := range events {
		if err := m.Store.AddServiceEvent(ctx, e); err != nil {
			m.log().WarnContext(ctx, "servermon: store event", "service", e.Service, "err", err)
		}
	}
}

// Service is one row of the service page: the catalog row, the unit's
// state and the automatic restarts of the last 24 hours.
type Service struct {
	agentapi.ServiceState
	Actions    []string `json:"actions"`
	Confirm    []string `json:"confirm,omitempty"`
	Restarts24 int      `json:"restarts24h"`
}

// Services reads the states now (not from the last poll) and adds the
// catalog row and the restart count. site narrows to one site's units
// (both PHP and Valkey) when not empty.
func (m *Monitor) Services(ctx context.Context, site string) ([]Service, error) {
	states, err := m.Agent.Services(ctx, agentapi.ServicesStatusRequest{})
	if err != nil {
		return nil, err
	}

	m.record(ctx, states)

	restarts, err := m.Store.CrashRestartsSince(ctx, m.now().Add(-24*time.Hour))
	if err != nil {
		return nil, err
	}

	out := make([]Service, 0, len(states))

	for _, st := range states {
		if site != "" && st.Site != site {
			continue
		}

		svc, _ := agentapi.LookupService(st.ID)
		row := Service{ServiceState: st, Actions: svc.Actions, Confirm: svc.Confirm, Restarts24: restarts[key(st.ID, st.Site)]}

		if !st.Installed || row.Actions == nil {
			row.Actions = []string{}
		}

		out = append(out, row)
	}

	return out, nil
}

// Action runs one catalog action for the user and records it.
func (m *Monitor) Action(ctx context.Context, userID int64, req agentapi.ServiceActionRequest) (agentapi.ServiceState, error) {
	k := key(req.ID, req.Site)

	m.mu.Lock()
	if m.actions == nil {
		m.actions = map[string]time.Time{}
	}

	m.actions[k] = m.now()
	m.mu.Unlock()

	st, err := m.Agent.Action(ctx, req)
	if err != nil {
		return st, err
	}

	uid := userID
	if err := m.Store.AddServiceEvent(ctx, store.ServiceEvent{
		At: m.now(), Service: req.ID, Site: req.Site, Kind: store.ServiceEventAction, Detail: req.Action, UserID: &uid,
	}); err != nil {
		m.log().WarnContext(ctx, "servermon: store action", "service", req.ID, "err", err)
	}

	// The new state is the base for the next poll, so the restart the
	// action caused is not counted as a crash.
	m.mu.Lock()
	if m.units != nil && st.Installed {
		m.units[k] = st
	}
	m.mu.Unlock()

	return st, nil
}

// Events returns the stored events, newest first.
func (m *Monitor) Events(ctx context.Context, f store.ServiceEventFilter) ([]store.ServiceEvent, error) {
	return m.Store.ServiceEvents(ctx, f)
}
