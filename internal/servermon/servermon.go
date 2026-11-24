// SPDX-License-Identifier: AGPL-3.0-or-later

// Package servermon is the panel's side of service management and the
// server dashboard (ADR-0052). It polls the agent's host.stats every
// SampleEvery and services.status every ServicesEvery, keeps the 15 s tier
// of the ring buffer in memory and the 5 min and 1 h tiers in SQLite, and
// turns changes of the units into service events.
package servermon

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Poll intervals and the tiers of the ring buffer [E: ADR-0052].
const (
	SampleEvery   = 15 * time.Second
	ServicesEvery = time.Minute
	RingSize      = 240 // one hour of 15 s points
	Keep5m        = 48 * time.Hour
	Keep1h        = 30 * 24 * time.Hour
	pruneEvery    = time.Hour
	// actionQuiet keeps the start an admin's action causes from showing up
	// a second time as "started".
	actionQuiet = 2 * time.Minute
)

// Agent is what the monitor asks the agent.
type Agent interface {
	HostStats(ctx context.Context) (agentapi.HostStats, error)
	Services(ctx context.Context, req agentapi.ServicesStatusRequest) ([]agentapi.ServiceState, error)
	Action(ctx context.Context, req agentapi.ServiceActionRequest) (agentapi.ServiceState, error)
}

// Store is the part of internal/store the monitor uses.
type Store interface {
	AddServiceEvent(ctx context.Context, e store.ServiceEvent) error
	ServiceEvents(ctx context.Context, f store.ServiceEventFilter) ([]store.ServiceEvent, error)
	CrashRestartsSince(ctx context.Context, t time.Time) (map[string]int, error)
	PruneServiceEvents(ctx context.Context, before time.Time) error
	PutHostSample(ctx context.Context, tier int, h store.HostSample) error
	HostSamples(ctx context.Context, tier int, since time.Time) ([]store.HostSample, error)
	PruneHostSamples(ctx context.Context, tier int, before time.Time) error
}

// CrashWindow is the span over which ServicePoll.Crashes counts crash
// restarts (ADR-0055).
const CrashWindow = 15 * time.Minute

// ServicePoll is what one poll of the services found, for Alerter.
type ServicePoll struct {
	At     time.Time
	States []agentapi.ServiceState
	// Events are the events since the last poll, including those noticed
	// when the services page read the states in between.
	Events []store.ServiceEvent
	// Crashes counts the crash restarts of the last CrashWindow by Key;
	// nil when Events has no crash restart.
	Crashes map[string]int
}

// Alerter is told about every services poll (ADR-0055); internal/notify
// turns it into emails.
type Alerter interface {
	ServicesPolled(ctx context.Context, p ServicePoll)
	// AgentUnreachable is called for each poll the agent did not answer.
	AgentUnreachable(ctx context.Context, at time.Time, err error)
}

// Monitor keeps the dashboard's samples and watches the services.
type Monitor struct {
	Agent  Agent
	Store  Store
	Alerts Alerter // nil: no alerts
	// SiteAgent and SiteStore feed the usage per site (ADR-0063); nil
	// for either turns it off.
	SiteAgent SiteAgent
	SiteStore SiteStore
	Log    *slog.Logger
	Now    func() time.Time // default time.Now

	mu       sync.Mutex
	prev     *raw
	ring     []store.HostSample
	b5, b1   bucket
	units    map[string]agentapi.ServiceState
	actions  map[string]time.Time
	lastSeen time.Time // last successful services poll
	pending  []store.ServiceEvent
	sites    sites
}

type raw struct {
	at time.Time
	s  agentapi.HostStats
}

func (m *Monitor) now() time.Time {
	if m.Now != nil {
		return m.Now().UTC()
	}

	return time.Now().UTC()
}

func (m *Monitor) log() *slog.Logger {
	if m.Log != nil {
		return m.Log
	}

	return slog.New(slog.DiscardHandler)
}

// Run polls until ctx ends.
func (m *Monitor) Run(ctx context.Context) {
	m.Sample(ctx)
	m.SampleSites(ctx)
	m.PollServices(ctx)

	samples := time.NewTicker(SampleEvery)
	defer samples.Stop()

	services := time.NewTicker(ServicesEvery)
	defer services.Stop()

	prune := time.NewTicker(pruneEvery)
	defer prune.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-samples.C:
			m.Sample(ctx)
			m.SampleSites(ctx)
		case <-services.C:
			m.PollServices(ctx)
		case <-prune.C:
			m.Prune(ctx)
		}
	}
}

// Prune drops old events and samples.
func (m *Monitor) Prune(ctx context.Context) {
	now := m.now()

	if err := m.Store.PruneServiceEvents(ctx, now.Add(-store.ServiceEventsKeep)); err != nil {
		m.log().WarnContext(ctx, "servermon: prune events", "err", err)
	}

	if err := m.Store.PruneHostSamples(ctx, store.HostTier5m, now.Add(-Keep5m)); err != nil {
		m.log().WarnContext(ctx, "servermon: prune samples", "err", err)
	}

	if err := m.Store.PruneHostSamples(ctx, store.HostTier1h, now.Add(-Keep1h)); err != nil {
		m.log().WarnContext(ctx, "servermon: prune samples", "err", err)
	}

	m.pruneSites(ctx, now)
}
