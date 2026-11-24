// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"math"
	"time"
)

// SiteSample is one point of a site's usage (ADR-0063).
type SiteSample struct {
	Site    string    `json:"-"`
	At      time.Time `json:"at"`
	CPU     float64   `json:"cpu"`   // percent of one core
	Mem     uint64    `json:"mem"`   // working set, bytes
	Cache   uint64    `json:"cache"` // file cache, bytes
	Tasks   uint64    `json:"tasks"`
	IORead  float64   `json:"ioRead"`  // bytes per second
	IOWrite float64   `json:"ioWrite"` // bytes per second
	HTTPReq float64   `json:"httpReq"` // requests per second
	HTTPIn  float64   `json:"httpIn"`  // bytes per second from clients
	HTTPOut float64   `json:"httpOut"` // bytes per second to clients
}

// SiteTotal sums up a site over a range: averages and peaks of the rates
// and gauges, and the amounts the rates add up to.
type SiteTotal struct {
	Site         string  `json:"site"`
	CPUAvg       float64 `json:"cpuAvg"`
	CPUMax       float64 `json:"cpuMax"`
	MemAvg       uint64  `json:"memAvg"`
	MemMax       uint64  `json:"memMax"`
	IOReadBytes  float64 `json:"ioReadBytes"`
	IOWriteBytes float64 `json:"ioWriteBytes"`
	HTTPRequests float64 `json:"httpRequests"`
	HTTPInBytes  float64 `json:"httpInBytes"`
	HTTPOutBytes float64 `json:"httpOutBytes"`
}

func checkSiteTier(tier int) error {
	if tier != HostTier5m && tier != HostTier1h {
		return fmt.Errorf("store: unknown site sample tier %d", tier)
	}

	return nil
}

// finite keeps NaN, infinities and negative values out of the table.
func finite(v float64) float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
		return 0
	}

	return v
}

// PutSiteSamples stores or replaces the samples of tier in one
// transaction. A sample of a site that is not in the store is skipped.
func (s *Store) PutSiteSamples(ctx context.Context, tier int, samples []SiteSample) error {
	if err := checkSiteTier(tier); err != nil {
		return err
	}

	if len(samples) == 0 {
		return nil
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer tx.Rollback() //nolint:errcheck // no-op after Commit

	for _, p := range samples {
		_, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO site_samples
			(site_id, tier, at, cpu, mem, cache, tasks, io_read, io_write, http_req, http_in, http_out)
			SELECT id, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ? FROM sites WHERE name = ?`,
			tier, p.At.Unix(), finite(p.CPU), clampInt64(p.Mem), clampInt64(p.Cache), clampInt64(p.Tasks),
			finite(p.IORead), finite(p.IOWrite), finite(p.HTTPReq), finite(p.HTTPIn), finite(p.HTTPOut), p.Site)
		if err != nil {
			return fmt.Errorf("store: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// SiteSamples returns the samples of one site in tier since t, oldest
// first.
func (s *Store) SiteSamples(ctx context.Context, tier int, site string, since time.Time) ([]SiteSample, error) {
	if err := checkSiteTier(tier); err != nil {
		return nil, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT p.at, p.cpu, p.mem, p.cache, p.tasks, p.io_read, p.io_write, p.http_req, p.http_in, p.http_out
		FROM site_samples p JOIN sites s ON s.id = p.site_id
		WHERE s.name = ? AND p.tier = ? AND p.at >= ? ORDER BY p.at`, site, tier, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []SiteSample{}

	for rows.Next() {
		var (
			p                   SiteSample
			at, mem, cache, tsk int64
		)

		if err := rows.Scan(&at, &p.CPU, &mem, &cache, &tsk, &p.IORead, &p.IOWrite, &p.HTTPReq, &p.HTTPIn, &p.HTTPOut); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		p.Site, p.At = site, time.Unix(at, 0).UTC()
		p.Mem, p.Cache, p.Tasks = uint64(max(mem, 0)), uint64(max(cache, 0)), uint64(max(tsk, 0))
		out = append(out, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SiteTotals sums up every site with samples in tier since t, by name.
// Amounts are rate times the tier's step.
func (s *Store) SiteTotals(ctx context.Context, tier int, since time.Time) ([]SiteTotal, error) {
	if err := checkSiteTier(tier); err != nil {
		return nil, err
	}

	step := float64(tier)

	rows, err := s.db.QueryContext(ctx, `SELECT s.name, AVG(p.cpu), MAX(p.cpu), AVG(p.mem), MAX(p.mem),
		SUM(p.io_read), SUM(p.io_write), SUM(p.http_req), SUM(p.http_in), SUM(p.http_out)
		FROM site_samples p JOIN sites s ON s.id = p.site_id
		WHERE p.tier = ? AND p.at >= ? GROUP BY s.name ORDER BY s.name`, tier, since.Unix())
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []SiteTotal{}

	for rows.Next() {
		var (
			t      SiteTotal
			memAvg float64
			memMax int64
		)

		if err := rows.Scan(&t.Site, &t.CPUAvg, &t.CPUMax, &memAvg, &memMax, &t.IOReadBytes, &t.IOWriteBytes, &t.HTTPRequests, &t.HTTPInBytes, &t.HTTPOutBytes); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		t.MemAvg, t.MemMax = uint64(max(memAvg, 0)), uint64(max(memMax, 0))
		t.IOReadBytes *= step
		t.IOWriteBytes *= step
		t.HTTPRequests *= step
		t.HTTPInBytes *= step
		t.HTTPOutBytes *= step
		out = append(out, t)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// PruneSiteSamples drops samples of tier older than before.
func (s *Store) PruneSiteSamples(ctx context.Context, tier int, before time.Time) error {
	if err := checkSiteTier(tier); err != nil {
		return err
	}

	if _, err := s.w.ExecContext(ctx, `DELETE FROM site_samples WHERE tier = ? AND at < ?`, tier, before.Unix()); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
