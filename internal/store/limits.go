// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Errors of the limit presets.
var (
	ErrPresetExists  = errors.New("store: a preset with this name exists")
	ErrPresetDefault = errors.New("store: the default preset cannot be deleted")
)

// Limits are the resource limits of a site or a preset (ADR-0015). 0 in a
// limit means unlimited.
type Limits struct {
	DiskBytes       int64
	DiskInodes      int64
	MemoryBytes     int64
	PHPWorkers      int64
	Tasks           int64
	CPUPriority     string // low, normal or high
	CPUQuotaPercent int64  // 0: no hard CPU limit
}

// LimitPreset is a named set of limits.
type LimitPreset struct {
	ID        int64
	Name      string
	Limits    Limits
	Default   bool
	CreatedAt time.Time
	UpdatedAt time.Time
}

// SiteLimits are the limits a site should have and the result of the last
// attempt to apply them.
type SiteLimits struct {
	SiteID     int64
	PresetID   int64 // 0: custom values
	Limits     Limits
	AppliedAt  time.Time // zero: not applied (see ApplyError)
	ApplyError string
	UpdatedAt  time.Time
}

const limitColumns = `disk_bytes, disk_inodes, memory_bytes, php_workers, tasks, cpu_priority, cpu_quota_percent`

func limitArgs(l Limits) []any {
	return []any{l.DiskBytes, l.DiskInodes, l.MemoryBytes, l.PHPWorkers, l.Tasks, l.CPUPriority, l.CPUQuotaPercent}
}

func scanPreset(r scanner) (LimitPreset, error) {
	var (
		p        LimitPreset
		def      int
		cre, upd int64
	)

	l := &p.Limits
	if err := r.Scan(&p.ID, &p.Name, &l.DiskBytes, &l.DiskInodes, &l.MemoryBytes, &l.PHPWorkers, &l.Tasks, &l.CPUPriority,
		&l.CPUQuotaPercent, &def, &cre, &upd); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return LimitPreset{}, ErrNotFound
		}

		return LimitPreset{}, fmt.Errorf("store: %w", err)
	}

	p.Default = def == 1
	p.CreatedAt, p.UpdatedAt = time.Unix(cre, 0).UTC(), time.Unix(upd, 0).UTC()

	return p, nil
}

const presetSelect = `SELECT id, name, ` + limitColumns + `, is_default, created_at, updated_at FROM limit_presets`

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func presetBy(ctx context.Context, q querier, where string, arg any) (LimitPreset, error) {
	return scanPreset(q.QueryRowContext(ctx, presetSelect+` WHERE `+where, arg))
}

// LimitPresets returns all presets by name.
func (s *Store) LimitPresets(ctx context.Context) ([]LimitPreset, error) {
	rows, err := s.db.QueryContext(ctx, presetSelect+` ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []LimitPreset{}

	for rows.Next() {
		p, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}

		out = append(out, p)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// LimitPreset returns a preset by name or ErrNotFound.
func (s *Store) LimitPreset(ctx context.Context, name string) (LimitPreset, error) {
	return presetBy(ctx, s.db, `name = ?`, name)
}

// LimitPresetByID returns a preset by id or ErrNotFound.
func (s *Store) LimitPresetByID(ctx context.Context, id int64) (LimitPreset, error) {
	return presetBy(ctx, s.db, `id = ?`, id)
}

// DefaultLimitPreset returns the default preset or ErrNotFound.
func (s *Store) DefaultLimitPreset(ctx context.Context) (LimitPreset, error) {
	return presetBy(ctx, s.db, `is_default = ?`, 1)
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// CreateLimitPreset adds a preset; with def it becomes the default.
func (s *Store) CreateLimitPreset(ctx context.Context, name string, l Limits, def bool, now time.Time) (LimitPreset, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LimitPreset{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	if def {
		if _, err := tx.ExecContext(ctx, `UPDATE limit_presets SET is_default = 0 WHERE is_default = 1`); err != nil {
			return LimitPreset{}, fmt.Errorf("store: %w", err)
		}
	}

	args := append([]any{name}, limitArgs(l)...)
	args = append(args, boolInt(def), now.Unix(), now.Unix())

	res, err := tx.ExecContext(ctx, `INSERT INTO limit_presets (name, `+limitColumns+`, is_default, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, args...)
	if isUnique(err) {
		return LimitPreset{}, ErrPresetExists
	}

	if err != nil {
		return LimitPreset{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return LimitPreset{}, fmt.Errorf("store: %w", err)
	}

	p, err := presetBy(ctx, tx, `id = ?`, id)
	if err != nil {
		return LimitPreset{}, err
	}

	if err := tx.Commit(); err != nil {
		return LimitPreset{}, fmt.Errorf("store: %w", err)
	}

	return p, nil
}

// UpdateLimitPreset changes a preset's name and values; with def it becomes
// the default (def false never clears the default flag: another preset
// takes it over instead). The sites that follow the preset get the new
// values, with their applied state reset. It returns the preset as it was.
func (s *Store) UpdateLimitPreset(ctx context.Context, id int64, name string, l Limits, def bool, now time.Time) (LimitPreset, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LimitPreset{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	old, err := presetBy(ctx, tx, `id = ?`, id)
	if err != nil {
		return LimitPreset{}, err
	}

	if def && !old.Default {
		if _, err := tx.ExecContext(ctx, `UPDATE limit_presets SET is_default = 0 WHERE is_default = 1`); err != nil {
			return LimitPreset{}, fmt.Errorf("store: %w", err)
		}
	}

	args := append([]any{name}, limitArgs(l)...)
	args = append(args, boolInt(def || old.Default), now.Unix(), id)

	_, err = tx.ExecContext(ctx, `UPDATE limit_presets SET name = ?, disk_bytes = ?, disk_inodes = ?, memory_bytes = ?, php_workers = ?,
		tasks = ?, cpu_priority = ?, cpu_quota_percent = ?, is_default = ?, updated_at = ? WHERE id = ?`, args...)
	if isUnique(err) {
		return LimitPreset{}, ErrPresetExists
	}

	if err != nil {
		return LimitPreset{}, fmt.Errorf("store: %w", err)
	}

	if err := setFollowers(ctx, tx, id, l, now); err != nil {
		return LimitPreset{}, err
	}

	if err := tx.Commit(); err != nil {
		return LimitPreset{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

func setFollowers(ctx context.Context, tx *sql.Tx, presetID int64, l Limits, now time.Time) error {
	args := append(limitArgs(l), now.Unix(), presetID)

	if _, err := tx.ExecContext(ctx, `UPDATE site_limits SET disk_bytes = ?, disk_inodes = ?, memory_bytes = ?, php_workers = ?,
		tasks = ?, cpu_priority = ?, cpu_quota_percent = ?, applied_at = 0, apply_error = '', updated_at = ? WHERE preset_id = ?`, args...); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// RestoreLimitPreset puts back a preset returned by UpdateLimitPreset,
// together with the values of the sites that follow it.
func (s *Store) RestoreLimitPreset(ctx context.Context, p LimitPreset, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	if p.Default {
		if _, err := tx.ExecContext(ctx, `UPDATE limit_presets SET is_default = 0 WHERE is_default = 1 AND id != ?`, p.ID); err != nil {
			return fmt.Errorf("store: %w", err)
		}
	}

	args := append([]any{p.Name}, limitArgs(p.Limits)...)
	args = append(args, boolInt(p.Default), p.UpdatedAt.Unix(), p.ID)

	if _, err := tx.ExecContext(ctx, `UPDATE limit_presets SET name = ?, disk_bytes = ?, disk_inodes = ?, memory_bytes = ?, php_workers = ?,
		tasks = ?, cpu_priority = ?, cpu_quota_percent = ?, is_default = ?, updated_at = ? WHERE id = ?`, args...); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if err := setFollowers(ctx, tx, p.ID, p.Limits, now); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// DeleteLimitPreset removes a preset that is not the default. The sites
// that followed it keep its values as custom values.
func (s *Store) DeleteLimitPreset(ctx context.Context, id int64) (LimitPreset, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LimitPreset{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	old, err := presetBy(ctx, tx, `id = ?`, id)
	if err != nil {
		return LimitPreset{}, err
	}

	if old.Default {
		return LimitPreset{}, ErrPresetDefault
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM limit_presets WHERE id = ?`, id); err != nil {
		return LimitPreset{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return LimitPreset{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// PresetFollowers returns the names of the sites that follow a preset.
func (s *Store) PresetFollowers(ctx context.Context, presetID int64) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT s.name FROM site_limits l JOIN sites s ON s.id = l.site_id
		WHERE l.preset_id = ? ORDER BY s.name`, presetID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []string{}

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, name)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SiteLimits returns the limits of a site, and false if none are set (a
// site from before ADR-0015).
func (s *Store) SiteLimits(ctx context.Context, siteID int64) (SiteLimits, bool, error) {
	var (
		out          SiteLimits
		preset       sql.NullInt64
		applied, upd int64
	)

	l := &out.Limits

	err := s.db.QueryRowContext(ctx, `SELECT site_id, preset_id, `+limitColumns+`, applied_at, apply_error, updated_at
		FROM site_limits WHERE site_id = ?`, siteID).
		Scan(&out.SiteID, &preset, &l.DiskBytes, &l.DiskInodes, &l.MemoryBytes, &l.PHPWorkers, &l.Tasks, &l.CPUPriority,
			&l.CPUQuotaPercent, &applied, &out.ApplyError, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return SiteLimits{SiteID: siteID}, false, nil
	}

	if err != nil {
		return SiteLimits{}, false, fmt.Errorf("store: %w", err)
	}

	out.PresetID = preset.Int64

	if applied > 0 {
		out.AppliedAt = time.Unix(applied, 0).UTC()
	}

	out.UpdatedAt = time.Unix(upd, 0).UTC()

	return out, true, nil
}

// SetSiteLimits stores the limits of a site (presetID 0: custom values)
// with the applied state reset.
func (s *Store) SetSiteLimits(ctx context.Context, siteID, presetID int64, l Limits, now time.Time) error {
	var preset any
	if presetID != 0 {
		preset = presetID
	}

	args := append([]any{siteID, preset}, limitArgs(l)...)
	args = append(args, now.Unix())

	if _, err := s.db.ExecContext(ctx, `INSERT INTO site_limits (site_id, preset_id, `+limitColumns+`, applied_at, apply_error, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0, '', ?)
		ON CONFLICT (site_id) DO UPDATE SET preset_id = excluded.preset_id, disk_bytes = excluded.disk_bytes,
			disk_inodes = excluded.disk_inodes, memory_bytes = excluded.memory_bytes, php_workers = excluded.php_workers,
			tasks = excluded.tasks, cpu_priority = excluded.cpu_priority, cpu_quota_percent = excluded.cpu_quota_percent,
			applied_at = 0, apply_error = '', updated_at = excluded.updated_at`, args...); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// RestoreSiteLimits puts back limits returned by SiteLimits; ok false
// removes them again.
func (s *Store) RestoreSiteLimits(ctx context.Context, old SiteLimits, ok bool) error {
	if !ok {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM site_limits WHERE site_id = ?`, old.SiteID); err != nil {
			return fmt.Errorf("store: %w", err)
		}

		return nil
	}

	if err := s.SetSiteLimits(ctx, old.SiteID, old.PresetID, old.Limits, old.UpdatedAt); err != nil {
		return err
	}

	var applied int64
	if !old.AppliedAt.IsZero() {
		applied = old.AppliedAt.Unix()
	}

	return s.MarkSiteLimits(ctx, old.SiteID, applied, old.ApplyError)
}

// MarkSiteLimits records the result of applying a site's limits: the time
// and "" on success, 0 and the error text on failure.
func (s *Store) MarkSiteLimits(ctx context.Context, siteID, appliedAt int64, applyErr string) error {
	if len(applyErr) > 500 {
		applyErr = applyErr[:500]
	}

	if _, err := s.db.ExecContext(ctx, `UPDATE site_limits SET applied_at = ?, apply_error = ? WHERE site_id = ?`,
		appliedAt, applyErr, siteID); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}
