// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Kinds of service events (ADR-0052).
const (
	ServiceEventCrashRestart = "crash-restart"
	ServiceEventStarted      = "started"
	ServiceEventFailed       = "failed"
	ServiceEventAction       = "action"
)

// Retention of service events.
const (
	ServiceEventsKeep    = 30 * 24 * time.Hour
	ServiceEventsMaxRows = 10000
)

// ServiceEvent is one row of service_events. UserID is set for actions and
// nil once that account is gone; UserName is its name then, else empty.
type ServiceEvent struct {
	ID       int64
	At       time.Time
	Service  string
	Site     string
	Kind     string
	Detail   string
	Restarts int
	UserID   *int64
	UserName string
}

// AddServiceEvent stores an event.
func (s *Store) AddServiceEvent(ctx context.Context, e ServiceEvent) error {
	_, err := s.w.ExecContext(ctx,
		`INSERT INTO service_events (at, service, site, kind, detail, restarts, user_id) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.At.Unix(), e.Service, e.Site, e.Kind, e.Detail, e.Restarts, e.UserID)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// ServiceEventFilter narrows ServiceEvents. Empty Service means all; Site
// is only compared when Service is set. Limit defaults to 100, at most 1000.
type ServiceEventFilter struct {
	Service string
	Site    string
	Since   time.Time
	Limit   int
}

// ServiceEvents returns events, newest first.
func (s *Store) ServiceEvents(ctx context.Context, f ServiceEventFilter) ([]ServiceEvent, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = 100
	}

	limit = min(limit, 1000)

	q := `SELECT e.id, e.at, e.service, e.site, e.kind, e.detail, e.restarts, e.user_id, COALESCE(u.username, '')
		FROM service_events e LEFT JOIN users u ON u.id = e.user_id WHERE e.at >= ?`
	args := []any{f.Since.Unix()}

	if f.Service != "" {
		q += ` AND e.service = ? AND e.site = ?`

		args = append(args, f.Service, f.Site)
	}

	q += ` ORDER BY e.at DESC, e.id DESC LIMIT ?`

	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []ServiceEvent{}

	for rows.Next() {
		var (
			e  ServiceEvent
			at int64
			by sql.NullInt64
		)

		if err := rows.Scan(&e.ID, &at, &e.Service, &e.Site, &e.Kind, &e.Detail, &e.Restarts, &by, &e.UserName); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		e.At = time.Unix(at, 0).UTC()
		if by.Valid {
			e.UserID = &by.Int64
		}

		out = append(out, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// CrashRestartsSince sums the automatic restarts per service and site
// since t, keyed by "service" or "service/site".
func (s *Store) CrashRestartsSince(ctx context.Context, t time.Time) (map[string]int, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT service, site, SUM(restarts) FROM service_events WHERE kind = ? AND at >= ? GROUP BY service, site`,
		ServiceEventCrashRestart, t.Unix())
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := map[string]int{}

	for rows.Next() {
		var (
			svc, site string
			n         int
		)

		if err := rows.Scan(&svc, &site, &n); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		k := svc
		if site != "" {
			k += "/" + site
		}

		out[k] = n
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// PruneServiceEvents drops events older than before and keeps at most
// ServiceEventsMaxRows of the newest.
func (s *Store) PruneServiceEvents(ctx context.Context, before time.Time) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM service_events WHERE at < ?`, before.Unix()); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	_, err := s.w.ExecContext(ctx, `DELETE FROM service_events WHERE id NOT IN
		(SELECT id FROM service_events ORDER BY at DESC, id DESC LIMIT ?)`, ServiceEventsMaxRows)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// HostDiskSample is one file system in a host sample.
type HostDiskSample struct {
	Name  string `json:"name"`
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

// HostNetSample is one interface in a host sample, in bytes per second.
type HostNetSample struct {
	Name string  `json:"name"`
	Rx   float64 `json:"rx"`
	Tx   float64 `json:"tx"`
}

// HostSample is one point of the dashboard (ADR-0052). CPU values are
// percentages of all CPUs.
type HostSample struct {
	At        time.Time        `json:"at"`
	CPUBusy   float64          `json:"cpuBusy"`
	CPUIOWait float64          `json:"cpuIowait"`
	CPUSteal  float64          `json:"cpuSteal"`
	Load1     float64          `json:"load1"`
	MemUsed   uint64           `json:"memUsed"`
	MemTotal  uint64           `json:"memTotal"`
	SwapUsed  uint64           `json:"swapUsed"`
	SwapTotal uint64           `json:"swapTotal"`
	Disks     []HostDiskSample `json:"disks"`
	Net       []HostNetSample  `json:"net"`
}

// Tiers of host_samples, as their step in seconds.
const (
	HostTier5m = 300
	HostTier1h = 3600
)

// PutHostSample stores or replaces the sample of tier at its time.
func (s *Store) PutHostSample(ctx context.Context, tier int, h HostSample) error {
	if tier != HostTier5m && tier != HostTier1h {
		return fmt.Errorf("store: unknown host sample tier %d", tier)
	}

	disks, err := json.Marshal(nonNil(h.Disks))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	nets, err := json.Marshal(nonNil(h.Net))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	_, err = s.w.ExecContext(ctx, `INSERT OR REPLACE INTO host_samples
		(tier, at, cpu_busy, cpu_iowait, cpu_steal, load1, mem_used, mem_total, swap_used, swap_total, disks, net)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		tier, h.At.Unix(), h.CPUBusy, h.CPUIOWait, h.CPUSteal, h.Load1,
		clampInt64(h.MemUsed), clampInt64(h.MemTotal), clampInt64(h.SwapUsed), clampInt64(h.SwapTotal), string(disks), string(nets))
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// HostSamples returns the samples of tier since t, oldest first.
func (s *Store) HostSamples(ctx context.Context, tier int, since time.Time) ([]HostSample, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT at, cpu_busy, cpu_iowait, cpu_steal, load1, mem_used, mem_total,
		swap_used, swap_total, disks, net FROM host_samples WHERE tier = ? AND at >= ? ORDER BY at`, tier, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []HostSample{}

	for rows.Next() {
		var (
			h              HostSample
			at             int64
			mu, mt, su, st int64
			disks, nets    string
		)

		if err := rows.Scan(&at, &h.CPUBusy, &h.CPUIOWait, &h.CPUSteal, &h.Load1, &mu, &mt, &su, &st, &disks, &nets); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		h.At = time.Unix(at, 0).UTC()
		h.MemUsed, h.MemTotal, h.SwapUsed, h.SwapTotal = uint64(max(mu, 0)), uint64(max(mt, 0)), uint64(max(su, 0)), uint64(max(st, 0))

		if err := json.Unmarshal([]byte(disks), &h.Disks); err != nil {
			return nil, fmt.Errorf("store: host sample disks: %w", err)
		}

		if err := json.Unmarshal([]byte(nets), &h.Net); err != nil {
			return nil, fmt.Errorf("store: host sample net: %w", err)
		}

		out = append(out, h)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// PruneHostSamples drops samples of tier older than before.
func (s *Store) PruneHostSamples(ctx context.Context, tier int, before time.Time) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM host_samples WHERE tier = ? AND at < ?`, tier, before.Unix()); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}

	return s
}

func clampInt64(v uint64) int64 {
	if v > 1<<62 {
		return 1 << 62
	}

	return int64(v)
}
