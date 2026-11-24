// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

// scanEngineJSON is ClamAV on the server (ADR-0064).
type scanEngineJSON struct {
	// State is "off", "installing", "removing", "on" or "failed".
	State string `json:"state"`
	// Version is the installed ClamAV package, "" while off.
	Version string `json:"version"`
	// Signatures is when the newest signature file was written, RFC 3339;
	// "" before freshclam's first download.
	Signatures string `json:"signatures"`
	// Error is the end of the install log when State is "failed".
	Error string `json:"error"`
}

type scanEngineRequest struct {
	// On installs ClamAV (true) or removes it (false).
	On *bool `json:"on"`
}

// scanRunJSON is one malware scan of a site.
type scanRunJSON struct {
	ID int64 `json:"id"`
	// Source is "panel", "api", "schedule" or "realtime".
	Source string `json:"source"`
	// State is "queued", "running", "succeeded" or "failed".
	State string `json:"state"`
	// Error says why a scan failed, or (succeeded) that some files could
	// not be scanned.
	Error string `json:"error"`
	// Found counts the flagged files, NewFound those not reported before.
	Found    int `json:"found"`
	NewFound int `json:"newFound"`
	// Dropped counts log lines the server did not accept as findings.
	Dropped    int    `json:"dropped"`
	CreatedAt  string `json:"createdAt"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}

// scanFindingJSON is one file ClamAV flagged.
type scanFindingJSON struct {
	ID int64 `json:"id"`
	// Path is relative to the site's home.
	Path      string `json:"path"`
	Signature string `json:"signature"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
	// State is "open", "ignored", "quarantined", "gone" or "restored".
	State     string `json:"state"`
	FirstSeen string `json:"firstSeen"`
	LastSeen  string `json:"lastSeen"`
	DecidedAt string `json:"decidedAt"`
}

// siteScanJSON is the malware scan of one site.
type siteScanJSON struct {
	// Engine is the state of ClamAV on the server ("off", "on", ...).
	Engine string `json:"engine"`
	// Schedule is "off", "daily" or "weekly".
	Schedule string `json:"schedule"`
	// NextScheduled is the next scheduled scan, RFC 3339; "" when off.
	NextScheduled string            `json:"nextScheduled"`
	Runs          []scanRunJSON     `json:"runs"`
	Findings      []scanFindingJSON `json:"findings"`
}

type scanScheduleRequest struct {
	Schedule string `json:"schedule"`
}

func toScanEngineJSON(s agentapi.ScanEngineStatus) scanEngineJSON {
	return scanEngineJSON{State: s.State, Version: s.Version, Signatures: s.Signatures, Error: s.Error}
}

func toScanRunJSON(r store.ScanRun) scanRunJSON {
	return scanRunJSON{
		ID: r.ID, Source: r.Source, State: r.State, Error: r.Error, Found: r.Found, NewFound: r.NewFound, Dropped: r.Dropped,
		CreatedAt: rfc3339(r.Created), StartedAt: rfc3339(r.Started), FinishedAt: rfc3339(r.Finished),
	}
}

func toScanFindingJSON(f store.ScanFinding) scanFindingJSON {
	return scanFindingJSON{
		ID: f.ID, Path: f.Path, Signature: f.Signature, SHA256: f.SHA256, Size: f.Size, State: f.State,
		FirstSeen: rfc3339(f.FirstSeen), LastSeen: rfc3339(f.LastSeen), DecidedAt: rfc3339(f.DecidedAt),
	}
}

func scanSource(p auth.Principal) string {
	if p.TokenID != 0 {
		return store.ScanSourceAPI
	}

	return store.ScanSourcePanel
}

// scanFail answers the errors of the malware scan routes.
func scanFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrNoScans):
		writeError(w, http.StatusConflict, "scan_unavailable", "the malware scan is not available on this panel")
	case errors.Is(err, hosting.ErrScanEngineOff):
		writeError(w, http.StatusConflict, "scan_off", "switch the malware scan on first")
	case errors.Is(err, hosting.ErrScanBusy):
		writeError(w, http.StatusConflict, "scan_running", "a scan of this site is queued or running")
	case errors.Is(err, hosting.ErrFindingNotFound):
		writeError(w, http.StatusNotFound, "finding_not_found", "no such finding")
	case errors.Is(err, hosting.ErrFindingState):
		writeError(w, http.StatusConflict, "finding_state", "the finding is not in a state for this")
	case errors.Is(err, hosting.ErrScanSchedule):
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "schedule must be off, daily or weekly")
	default:
		siteFail(log, w, err, what)
	}
}

func findingID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "finding_not_found", "no such finding")

		return 0, false
	}

	return id, true
}

// registerScans adds the routes of the malware scan (ADR-0064).
func registerScans(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/malware-scan", openapi.Operation{
		ID: "getMalwareScan", Tag: "malware", Summary: "ClamAV on the server: installed or not, its version and how fresh its signatures are", Out: scanEngineJSON{},
	}, a.allow("malware.engine:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		st, err := svc.ScanEngine(r.Context())
		if err != nil {
			scanFail(log, w, err, "scan engine")

			return
		}

		writeJSON(w, http.StatusOK, toScanEngineJSON(st))
	}))

	mux.Op("PUT /api/v1/malware-scan", openapi.Operation{
		ID: "setMalwareScan", Tag: "malware", Summary: "Install or remove ClamAV for the malware scan; the result follows in getMalwareScan",
		In: scanEngineRequest{}, Out: scanEngineJSON{}, Status: http.StatusAccepted,
	}, a.allow("malware.engine:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in scanEngineRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if in.On == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "on is required")

			return
		}

		st, err := svc.SetScanEngine(r.Context(), p.User.ID, *in.On)
		if err != nil {
			scanFail(log, w, err, "set scan engine")

			return
		}

		writeJSON(w, http.StatusAccepted, toScanEngineJSON(st))
	}))

	mux.Op("GET /api/v1/sites/{name}/malware-scan", openapi.Operation{
		ID: "getSiteMalwareScan", Tag: "malware", Summary: "The malware scan of a site: schedule, newest runs and flagged files", Out: siteScanJSON{},
	}, a.allow("sites.malware:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		v, err := svc.SiteScan(r.Context(), r.PathValue("name"))
		if err != nil {
			scanFail(log, w, err, "site scan")

			return
		}

		out := siteScanJSON{
			Engine: agentapi.ScanEngineOff, Schedule: v.Schedule, NextScheduled: rfc3339(v.NextScheduled),
			Runs: make([]scanRunJSON, 0, len(v.Runs)), Findings: make([]scanFindingJSON, 0, len(v.Findings)),
		}

		if eng, err := svc.ScanEngine(r.Context()); err == nil {
			out.Engine = eng.State
		}

		for _, run := range v.Runs {
			out.Runs = append(out.Runs, toScanRunJSON(run))
		}

		for _, f := range v.Findings {
			out.Findings = append(out.Findings, toScanFindingJSON(f))
		}

		writeJSON(w, http.StatusOK, out)
	}))

	mux.Op("PUT /api/v1/sites/{name}/malware-scan", openapi.Operation{
		ID: "setSiteMalwareSchedule", Tag: "malware", Summary: "Scan a site daily, weekly or only on request", In: scanScheduleRequest{},
		Status: http.StatusNoContent,
	}, a.allow("sites.malware:manage", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in scanScheduleRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if err := svc.SetScanSchedule(r.Context(), p.User.ID, r.PathValue("name"), in.Schedule); err != nil {
			scanFail(log, w, err, "set scan schedule")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	mux.Op("POST /api/v1/sites/{name}/malware-scans", openapi.Operation{
		ID: "startSiteMalwareScan", Tag: "malware", Summary: "Scan a site now; it waits while another site is scanned", Out: scanRunJSON{},
		Status: http.StatusAccepted,
	}, a.allow("sites.malware:scan", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		run, err := svc.StartScan(r.Context(), p.User.ID, r.PathValue("name"), scanSource(p))
		if err != nil {
			scanFail(log, w, err, "start scan")

			return
		}

		writeJSON(w, http.StatusAccepted, toScanRunJSON(run))
	}))

	finding := func(path, id, summary string, action authz.Action, do func(r *http.Request, p auth.Principal, name string, id int64) (store.ScanFinding, error)) {
		mux.Op("POST /api/v1/sites/{name}/malware-findings/{id}/"+path, openapi.Operation{
			ID: id, Tag: "malware", Summary: summary, Out: scanFindingJSON{},
		}, a.allow(action, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			fid, ok := findingID(w, r)
			if !ok {
				return
			}

			f, err := do(r, p, r.PathValue("name"), fid)
			if err != nil {
				scanFail(log, w, err, path+" finding")

				return
			}

			writeJSON(w, http.StatusOK, toScanFindingJSON(f))
		}))
	}

	finding("quarantine", "quarantineSiteMalwareFinding", "Move a flagged file into the quarantine, only if it is unchanged since the scan", "sites.malware:scan",
		func(r *http.Request, p auth.Principal, name string, id int64) (store.ScanFinding, error) {
			return svc.QuarantineFinding(r.Context(), p.User.ID, name, id)
		})
	finding("restore", "restoreSiteMalwareFinding", "Put a quarantined file back where it was", "sites.malware:manage",
		func(r *http.Request, p auth.Principal, name string, id int64) (store.ScanFinding, error) {
			return svc.RestoreFinding(r.Context(), p.User.ID, name, id)
		})
	finding("ignore", "ignoreSiteMalwareFinding", "Mark a flagged file as a false positive: the same content with the same signature is not reported again", "sites.malware:manage",
		func(r *http.Request, p auth.Principal, name string, id int64) (store.ScanFinding, error) {
			return svc.IgnoreFinding(r.Context(), p.User.ID, name, id, true)
		})
	finding("reopen", "reopenSiteMalwareFinding", "Report an ignored file again", "sites.malware:manage",
		func(r *http.Request, p auth.Principal, name string, id int64) (store.ScanFinding, error) {
			return svc.IgnoreFinding(r.Context(), p.User.ID, name, id, false)
		})
}
