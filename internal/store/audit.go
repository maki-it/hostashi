// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// AuditEntry is one row of the append-only audit log.
type AuditEntry struct {
	ID int64
	At time.Time
	// UserID is nil for entries without an account (a failed login) and
	// for accounts deleted since; Actor keeps the name in the second case.
	UserID *int64
	Actor  string
	Action string
	Target string
	// Detail is the JSON object stored with the entry, unchanged.
	Detail json.RawMessage
}

// AuditFilter narrows a read of the audit log. Zero fields do not filter.
type AuditFilter struct {
	// Action matches the action itself and every action below it:
	// "site.cert" matches "site.cert" and "site.cert.upload".
	Action string
	// Actor is the exact username at the time of the action.
	Actor string
	// Target matches entries whose target contains it, ignoring ASCII case.
	Target string
	// Since and Until limit the time: Since <= at < Until.
	Since, Until time.Time
}

// auditTime is the format of audit_log.at, so that times compare as text.
const auditTime = "2006-01-02T15:04:05.000Z"

// likeEscape escapes the wildcards of LIKE, for use with ESCAPE '\'.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// where returns the WHERE clause of f, starting with " WHERE", and its
// arguments.
func (f AuditFilter) where(before int64) (string, []any) {
	var (
		conds []string
		args  []any
	)

	if before > 0 {
		conds = append(conds, "id < ?")
		args = append(args, before)
	}

	if f.Action != "" {
		conds = append(conds, `(action = ? OR action LIKE ? ESCAPE '\')`)
		args = append(args, f.Action, likeEscape(f.Action)+".%")
	}

	if f.Actor != "" {
		conds = append(conds, "actor = ?")
		args = append(args, f.Actor)
	}

	if f.Target != "" {
		conds = append(conds, `target LIKE ? ESCAPE '\'`)
		args = append(args, "%"+likeEscape(f.Target)+"%")
	}

	if !f.Since.IsZero() {
		conds = append(conds, "at >= ?")
		args = append(args, f.Since.UTC().Format(auditTime))
	}

	if !f.Until.IsZero() {
		conds = append(conds, "at < ?")
		args = append(args, f.Until.UTC().Format(auditTime))
	}

	if len(conds) == 0 {
		return "", nil
	}

	return " WHERE " + strings.Join(conds, " AND "), args
}

const auditColumns = `SELECT id, at, user_id, actor, action, target, detail FROM audit_log`

func scanAudit(rows *sql.Rows) (AuditEntry, error) {
	var (
		e      AuditEntry
		at     string
		uid    sql.NullInt64
		detail string
	)

	if err := rows.Scan(&e.ID, &at, &uid, &e.Actor, &e.Action, &e.Target, &detail); err != nil {
		return AuditEntry{}, fmt.Errorf("store: %w", err)
	}

	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return AuditEntry{}, fmt.Errorf("store: audit entry %d: %w", e.ID, err)
	}

	e.At = t.UTC()
	e.Detail = json.RawMessage(detail)

	if uid.Valid {
		e.UserID = &uid.Int64
	}

	return e, nil
}

// AuditEntries returns up to limit entries that match f, newest first, with
// an id below before (0: from the newest). more is true if older matching
// entries exist; the next page then starts before the last entry returned.
func (s *Store) AuditEntries(ctx context.Context, f AuditFilter, before int64, limit int) (entries []AuditEntry, more bool, err error) {
	if limit < 1 {
		return nil, false, fmt.Errorf("store: audit limit %d", limit)
	}

	where, args := f.where(before)

	rows, err := s.db.QueryContext(ctx, auditColumns+where+" ORDER BY id DESC LIMIT ?", append(args, limit+1)...)
	if err != nil {
		return nil, false, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	entries = []AuditEntry{}

	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return nil, false, err
		}

		entries = append(entries, e)
	}

	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("store: %w", err)
	}

	if len(entries) > limit {
		return entries[:limit], true, nil
	}

	return entries, false, nil
}

// EachAuditEntry calls fn for every entry that matches f, oldest first,
// without holding all of them in memory. An error of fn stops the walk and
// is returned unchanged.
func (s *Store) EachAuditEntry(ctx context.Context, f AuditFilter, fn func(AuditEntry) error) error {
	where, args := f.where(0)

	rows, err := s.db.QueryContext(ctx, auditColumns+where+" ORDER BY id", args...)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	for rows.Next() {
		e, err := scanAudit(rows)
		if err != nil {
			return err
		}

		if err := fn(e); err != nil {
			return err
		}
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// AuditActions returns every action the log holds, sorted, for a filter.
func (s *Store) AuditActions(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT action FROM audit_log ORDER BY action`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = rows.Close() }()

	out := []string{}

	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, a)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}
