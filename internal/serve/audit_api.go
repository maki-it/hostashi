// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"bufio"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/store"
)

// AuditLog reads the append-only audit log and writes the entry for an
// export.
type AuditLog interface {
	AuditEntries(ctx context.Context, f store.AuditFilter, before int64, limit int) ([]store.AuditEntry, bool, error)
	EachAuditEntry(ctx context.Context, f store.AuditFilter, fn func(store.AuditEntry) error) error
	AuditActions(ctx context.Context) ([]string, error)
	Audit(ctx context.Context, userID *int64, action, target string, detail map[string]string) error
}

const (
	auditDefaultLimit = 50
	auditMaxLimit     = 200
	auditMaxActor     = 64
	auditMaxTarget    = 200
)

// auditActionRe is the shape of an action or a prefix of one.
var auditActionRe = regexp.MustCompile(`^[a-z0-9_]+(\.[a-z0-9_]+)*$`)

type auditEntryJSON struct {
	ID     int64           `json:"id"`
	At     time.Time       `json:"at"`
	UserID *int64          `json:"userId"`
	Actor  string          `json:"actor"`
	Action string          `json:"action"`
	Target string          `json:"target"`
	Detail json.RawMessage `json:"detail"`
	// IP is the client's address; empty for work without a request.
	IP string `json:"ip"`
	// Diff holds the fields an update changed: {"field": {"old": …, "new": …}}.
	Diff json.RawMessage `json:"diff"`
}

func auditJSON(e store.AuditEntry) auditEntryJSON {
	return auditEntryJSON{ID: e.ID, At: e.At, UserID: e.UserID, Actor: e.Actor, Action: e.Action, Target: e.Target, Detail: e.Detail, IP: e.IP, Diff: e.Diff}
}

type auditPageJSON struct {
	Entries []auditEntryJSON `json:"entries"`
	// Next is the cursor of the next, older page; null on the last page.
	Next *string `json:"next"`
}

// auditQueryError is a bad filter; its text is the answer's message.
type auditQueryError struct{ msg string }

func (e auditQueryError) Error() string { return e.msg }

func badAudit(msg string) error { return auditQueryError{msg} }

// printable reports whether s has no control characters.
func printable(s string) bool {
	return !strings.ContainsFunc(s, unicode.IsControl)
}

// parseAuditFilter reads the filters shared by the list and the export.
func parseAuditFilter(q url.Values) (store.AuditFilter, error) {
	var f store.AuditFilter

	if v := q.Get("action"); v != "" {
		if len(v) > 128 || !auditActionRe.MatchString(v) {
			return f, badAudit("action must be an action or a prefix of one, such as site.cert")
		}

		f.Action = v
	}

	if v := q.Get("actor"); v != "" {
		if len(v) > auditMaxActor || !printable(v) {
			return f, badAudit("actor is too long or holds control characters")
		}

		f.Actor = v
	}

	if v := q.Get("target"); v != "" {
		if len(v) > auditMaxTarget || !printable(v) {
			return f, badAudit("target is too long or holds control characters")
		}

		f.Target = v
	}

	if v := q.Get("ip"); v != "" {
		a, err := netip.ParseAddr(v)
		if err != nil {
			return f, badAudit("ip must be an IP address")
		}

		f.IP = a.Unmap().String()
	}

	for _, p := range []struct {
		key string
		to  *time.Time
	}{{"since", &f.Since}, {"until", &f.Until}} {
		v := q.Get(p.key)
		if v == "" {
			continue
		}

		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil || t.Year() < 2000 || t.Year() > 9999 {
			return f, badAudit(p.key + " must be a time in RFC 3339, such as 2026-11-21T00:00:00Z")
		}

		*p.to = t
	}

	if !f.Since.IsZero() && !f.Until.IsZero() && !f.Since.Before(f.Until) {
		return f, badAudit("since must be before until")
	}

	return f, nil
}

func parseAuditPage(q url.Values) (before int64, limit int, err error) {
	limit = auditDefaultLimit

	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > auditMaxLimit {
			return 0, 0, badAudit("limit must be between 1 and " + strconv.Itoa(auditMaxLimit))
		}

		limit = n
	}

	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 1 {
			return 0, 0, badAudit("before must be a cursor from a previous page")
		}

		before = n
	}

	return before, limit, nil
}

// csvCell keeps a spreadsheet from running a cell as a formula: targets and
// actors come from user input, such as the username of a failed login.
func csvCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}

	return s
}

// registerAudit adds the audit log routes. The log names every account,
// site and address of the server, so only admins read it (scope
// audit:read).
func registerAudit(mux *routeMux, a authn, al AuditLog, log *slog.Logger) {
	bad := func(w http.ResponseWriter, err error) bool {
		var qe auditQueryError
		if errors.As(err, &qe) {
			writeError(w, http.StatusBadRequest, "invalid_query", qe.msg)

			return true
		}

		return false
	}

	mux.HandleFunc("GET /api/v1/audit", a.allow("audit:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		q := r.URL.Query()

		f, err := parseAuditFilter(q)
		if bad(w, err) {
			return
		}

		before, limit, err := parseAuditPage(q)
		if bad(w, err) {
			return
		}

		entries, more, err := al.AuditEntries(r.Context(), f, before, limit)
		if err != nil {
			a.fail(w, err, "read audit log")

			return
		}

		out := auditPageJSON{Entries: make([]auditEntryJSON, 0, len(entries))}
		for _, e := range entries {
			out.Entries = append(out.Entries, auditJSON(e))
		}

		if more {
			next := strconv.FormatInt(entries[len(entries)-1].ID, 10)
			out.Next = &next
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, out)
	}))

	mux.HandleFunc("GET /api/v1/audit/actions", a.allow("audit:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		actions, err := al.AuditActions(r.Context())
		if err != nil {
			a.fail(w, err, "read audit actions")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, map[string][]string{"actions": actions})
	}))

	mux.HandleFunc("GET /api/v1/audit/export", a.allow("audit:read", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		q := r.URL.Query()

		format := q.Get("format")
		if format != "csv" && format != "json" {
			writeError(w, http.StatusBadRequest, "invalid_query", "format must be csv or json")

			return
		}

		f, err := parseAuditFilter(q)
		if bad(w, err) {
			return
		}

		// The export itself is written to the log, with its filters, before
		// any row leaves the server.
		detail := map[string]string{"format": format}

		for _, k := range []string{"action", "actor", "target", "ip", "since", "until"} {
			if v := q.Get(k); v != "" {
				detail[k] = v
			}
		}

		if err := al.Audit(r.Context(), &p.User.ID, "audit.export", "", detail); err != nil {
			a.fail(w, err, "audit export")

			return
		}

		name := "hostashi-audit-" + time.Now().UTC().Format("20060102T150405Z") + "." + format

		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)

		bw := bufio.NewWriterSize(w, 32<<10)

		if format == "csv" {
			w.Header().Set("Content-Type", "text/csv; charset=utf-8")

			err = exportAuditCSV(r.Context(), bw, al, f)
		} else {
			w.Header().Set("Content-Type", "application/json")

			err = exportAuditJSON(r.Context(), bw, al, f)
		}

		if err == nil {
			err = bw.Flush()
		}

		// The status is sent already: a cut-off file is the only sign of
		// the error the client gets (a JSON export then does not parse).
		if err != nil && !errors.Is(err, context.Canceled) {
			log.WarnContext(r.Context(), "audit export", "err", err)
		}
	}))
}

func exportAuditCSV(ctx context.Context, w *bufio.Writer, al AuditLog, f store.AuditFilter) error {
	cw := csv.NewWriter(w)

	if err := cw.Write([]string{"id", "at", "user_id", "actor", "ip", "action", "target", "detail", "diff"}); err != nil {
		return err
	}

	err := al.EachAuditEntry(ctx, f, func(e store.AuditEntry) error {
		uid := ""
		if e.UserID != nil {
			uid = strconv.FormatInt(*e.UserID, 10)
		}

		return cw.Write([]string{
			strconv.FormatInt(e.ID, 10), e.At.Format(time.RFC3339Nano), uid,
			csvCell(e.Actor), e.IP, csvCell(e.Action), csvCell(e.Target), csvCell(string(e.Detail)), csvCell(string(e.Diff)),
		})
	})
	if err != nil {
		return err
	}

	cw.Flush()

	return cw.Error()
}

func exportAuditJSON(ctx context.Context, w *bufio.Writer, al AuditLog, f store.AuditFilter) error {
	if _, err := w.WriteString("["); err != nil {
		return err
	}

	first := true

	err := al.EachAuditEntry(ctx, f, func(e store.AuditEntry) error {
		b, err := json.Marshal(auditJSON(e))
		if err != nil {
			return err
		}

		sep := ",\n"
		if first {
			sep, first = "\n", false
		}

		if _, err := w.WriteString(sep); err != nil {
			return err
		}

		_, err = w.Write(b)

		return err
	})
	if err != nil {
		return err
	}

	_, err = w.WriteString("\n]\n")

	return err
}
