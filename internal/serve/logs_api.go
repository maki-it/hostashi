// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
)

// logRetentionKey stores the days rotated logs are kept (ADR-0053).
const logRetentionKey = "logs.retention_days"

// SiteLogs reads the sites' logs and sets their retention in the agent.
type SiteLogs interface {
	Read(ctx context.Context, req agentapi.SiteLogsReadRequest) (agentapi.SiteLogsReadResponse, error)
	SetRetention(ctx context.Context, days int) error
}

// RPCSiteLogs asks the agent over C.
type RPCSiteLogs struct{ C *agentrpc.Client }

// Read calls site.logs.read.
func (l RPCSiteLogs) Read(ctx context.Context, req agentapi.SiteLogsReadRequest) (agentapi.SiteLogsReadResponse, error) {
	return agentapi.SiteLogsRead.Call(ctx, l.C, req)
}

// SetRetention calls logs.retention.set.
func (l RPCSiteLogs) SetRetention(ctx context.Context, days int) error {
	_, err := agentapi.LogsRetentionSet.Call(ctx, l.C, agentapi.LogsRetentionSetRequest{Days: days})

	return err
}

type siteLogsJSON struct {
	Lines []string `json:"lines"`
	// Cursor continues with ?cursor= for the lines written since.
	Cursor  string `json:"cursor"`
	Rotated bool   `json:"rotated"`
	More    bool   `json:"more"`
}

type logRetentionJSON struct {
	Days        int `json:"days"`
	MinDays     int `json:"minDays"`
	MaxDays     int `json:"maxDays"`
	DefaultDays int `json:"defaultDays"`
}

func formatCursor(c agentapi.LogCursor) string {
	return strconv.FormatUint(c.Inode, 10) + "." + strconv.FormatInt(c.Offset, 10)
}

func parseCursor(s string) (agentapi.LogCursor, bool) {
	ino, off, ok := strings.Cut(s, ".")
	if !ok || len(s) > 41 {
		return agentapi.LogCursor{}, false
	}

	i, err1 := strconv.ParseUint(ino, 10, 64)
	o, err2 := strconv.ParseInt(off, 10, 64)

	if err1 != nil || err2 != nil || o < 0 {
		return agentapi.LogCursor{}, false
	}

	return agentapi.LogCursor{Inode: i, Offset: o}, true
}

// logRetention returns the stored days, or the default.
func logRetention(ctx context.Context, st Settings) (int, error) {
	v, ok, err := st.Setting(ctx, logRetentionKey)
	if err != nil || !ok {
		return agentapi.DefaultLogRetentionDays, err
	}

	// A damaged value falls back to the default rather than failing.
	if d, convErr := strconv.Atoi(v); convErr == nil && d >= agentapi.MinLogRetentionDays && d <= agentapi.MaxLogRetentionDays {
		return d, nil
	}

	return agentapi.DefaultLogRetentionDays, nil
}

// registerLogs adds the log routes (ADR-0053): reading a site's logs for
// everyone with a grant, the retention for admins in a browser session.
func registerLogs(mux *routeMux, a authn, logs SiteLogs, st Settings, log *slog.Logger) {
	mux.HandleFunc("GET /api/v1/sites/{name}/logs/{kind}", a.allow("sites.logs:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		req := agentapi.SiteLogsReadRequest{Site: r.PathValue("name"), Kind: agentapi.LogKind(r.PathValue("kind"))}

		if !req.Kind.Valid() {
			writeError(w, http.StatusNotFound, "not_found", "no such log")

			return
		}

		q := r.URL.Query()

		if c := q.Get("cursor"); c != "" {
			cur, ok := parseCursor(c)
			if !ok || q.Has("lines") {
				writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "cursor must come from an earlier answer, without lines")

				return
			}

			req.After = &cur
		} else {
			req.Lines = 200

			if v := q.Get("lines"); v != "" {
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 || n > agentapi.MaxLogTailLines {
					writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "lines must be 1 to "+strconv.Itoa(agentapi.MaxLogTailLines))

					return
				}

				req.Lines = n
			}
		}

		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		resp, err := logs.Read(ctx, req)
		if err != nil {
			var ae *agentrpc.Error
			if errors.As(err, &ae) && ae.Code == agentrpc.CodeNotFound {
				writeError(w, http.StatusNotFound, "not_found", "no such site")

				return
			}

			siteFail(log, w, err, "read site log")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, siteLogsJSON{Lines: resp.Lines, Cursor: formatCursor(resp.Cursor), Rotated: resp.Rotated, More: resp.More})
	}))

	mux.HandleFunc("GET /api/v1/settings/logs", func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.sessionAllow(w, r, authz.SettingsRead); !ok {
			return
		}

		d, err := logRetention(r.Context(), st)
		if err != nil {
			log.ErrorContext(r.Context(), "read log retention", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		writeJSON(w, http.StatusOK, retentionJSON(d))
	})

	mux.HandleFunc("PUT /api/v1/settings/logs", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.sessionAllow(w, r, authz.SettingsWrite)
		if !ok {
			return
		}

		uid := u.ID

		var in struct {
			Days *int `json:"days"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.Days == nil || *in.Days < agentapi.MinLogRetentionDays || *in.Days > agentapi.MaxLogRetentionDays {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument",
				"days must be "+strconv.Itoa(agentapi.MinLogRetentionDays)+" to "+strconv.Itoa(agentapi.MaxLogRetentionDays))

			return
		}

		days := *in.Days

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		// The agent first: a stored value the server does not use would
		// promise a retention that does not hold.
		if err := logs.SetRetention(ctx, days); err != nil {
			siteFail(log, w, err, "set log retention")

			return
		}

		if err := st.SetSetting(ctx, logRetentionKey, strconv.Itoa(days)); err != nil {
			log.ErrorContext(ctx, "store log retention", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		_ = st.Audit(ctx, &uid, "settings.logs", "", map[string]string{"retention_days": strconv.Itoa(days), "ip": clientIP(r)})

		writeJSON(w, http.StatusOK, retentionJSON(days))
	})
}

func retentionJSON(d int) logRetentionJSON {
	return logRetentionJSON{
		Days: d, MinDays: agentapi.MinLogRetentionDays, MaxDays: agentapi.MaxLogRetentionDays, DefaultDays: agentapi.DefaultLogRetentionDays,
	}
}

// SyncLogRetention sends the stored retention to the agent once it
// answers, so a reinstalled or restored server keeps the promised value.
// It gives up after attempts tries, waiting wait between them.
func SyncLogRetention(ctx context.Context, logs SiteLogs, st Settings, attempts int, wait time.Duration, log *slog.Logger) {
	d, err := logRetention(ctx, st)
	if err != nil {
		log.ErrorContext(ctx, "read log retention", "err", err)

		return
	}

	for i := range attempts {
		if i > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}

		c, cancel := context.WithTimeout(ctx, agentTimeout)
		err = logs.SetRetention(c, d)

		cancel()

		if err == nil {
			return
		}
	}

	log.WarnContext(ctx, "log retention not sent to the agent", "days", d, "err", err)
}
