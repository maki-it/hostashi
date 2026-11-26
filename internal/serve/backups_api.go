// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/backup"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Backups is what the backup routes need (ADR-0008). The agent keeps the
// settings and the secrets; the panel only passes them on, so a password or
// private key never reaches the web process.
type Backups interface {
	Get(ctx context.Context) (agentapi.BackupInfo, error)
	Set(ctx context.Context, s agentapi.BackupSettings) (agentapi.BackupInfo, error)
	SetHostKey(ctx context.Context, key string) (agentapi.BackupInfo, error)
	Run(ctx context.Context) error
	Status(ctx context.Context) (agentapi.BackupStatus, error)
	Snapshots(ctx context.Context, site string) ([]agentapi.BackupSnapshot, error)
	// SnapshotSites lists the site names with snapshots, also of deleted
	// sites (ADR-0074).
	SnapshotSites(ctx context.Context) (agentapi.BackupSitesResponse, error)
	// Forget marks or unmarks the snapshots of a deleted site for removal
	// by the next run.
	Forget(ctx context.Context, req agentapi.BackupForgetRequest) ([]agentapi.BackupForgetMark, error)
	// ScanHostKey connects to the target and returns its host key, for
	// the administrator to confirm.
	ScanHostKey(ctx context.Context, t agentapi.BackupTarget) (ssh.PublicKey, error)
	// Restore starts restoring a snapshot of a site into a new directory
	// in the home of target (empty: the site itself); GetRestore reports
	// how it goes.
	Restore(ctx context.Context, site, snapshot, target string) (agentapi.BackupRestore, error)
	GetRestore(ctx context.Context, id string) (agentapi.BackupRestore, error)
	// Import reads a dump below a site's home into a database (the
	// agent's db.import); ImportJob reports how it goes.
	Import(ctx context.Context, database, path string) (agentapi.DBJob, error)
	ImportJob(ctx context.Context, id string) (agentapi.DBJob, error)
}

// DatabaseJobs starts and reports imports into databases on external
// servers (ADR-0041 addendum 1, ADR-0051); hosting.Service implements it.
type DatabaseJobs interface {
	ImportDatabase(ctx context.Context, actor int64, database, path string) (agentapi.DBJob, error)
	RemoteJob(ctx context.Context, id string) (agentapi.DBJob, error)
}

// SiteDatabases lists a site's databases as the panel records them.
type SiteDatabases func(ctx context.Context, site string) ([]store.Database, error)

// RPCBackups implements Backups with the agent. BeforeRun, if set, runs
// before a run on demand (the panel's database copy).
type RPCBackups struct {
	C         *agentrpc.Client
	BeforeRun func(ctx context.Context) error
}

// Get implements Backups.
func (b RPCBackups) Get(ctx context.Context) (agentapi.BackupInfo, error) {
	return agentapi.BackupGet.Call(ctx, b.C, agentapi.BackupGetRequest{})
}

// Set implements Backups.
func (b RPCBackups) Set(ctx context.Context, s agentapi.BackupSettings) (agentapi.BackupInfo, error) {
	return agentapi.BackupSet.Call(ctx, b.C, agentapi.BackupSetRequest{Settings: s})
}

// SetHostKey implements Backups.
func (b RPCBackups) SetHostKey(ctx context.Context, key string) (agentapi.BackupInfo, error) {
	return agentapi.BackupHostKey.Call(ctx, b.C, agentapi.BackupHostKeyRequest{Key: key})
}

// Run implements Backups.
func (b RPCBackups) Run(ctx context.Context) error {
	if b.BeforeRun != nil {
		if err := b.BeforeRun(ctx); err != nil {
			return err
		}
	}

	_, err := agentapi.BackupRun.Call(ctx, b.C, agentapi.BackupRunRequest{})

	return err
}

// Status implements Backups.
func (b RPCBackups) Status(ctx context.Context) (agentapi.BackupStatus, error) {
	return agentapi.BackupStatusGet.Call(ctx, b.C, agentapi.BackupStatusRequest{})
}

// Snapshots implements Backups.
func (b RPCBackups) Snapshots(ctx context.Context, site string) ([]agentapi.BackupSnapshot, error) {
	r, err := agentapi.BackupSnapshots.Call(ctx, b.C, agentapi.BackupSnapshotsRequest{Site: site})

	return r.Snapshots, err
}

// SnapshotSites implements Backups.
func (b RPCBackups) SnapshotSites(ctx context.Context) (agentapi.BackupSitesResponse, error) {
	return agentapi.BackupSites.Call(ctx, b.C, agentapi.BackupSitesRequest{})
}

// Forget implements Backups.
func (b RPCBackups) Forget(ctx context.Context, req agentapi.BackupForgetRequest) ([]agentapi.BackupForgetMark, error) {
	r, err := agentapi.BackupForget.Call(ctx, b.C, req)

	return r.Marks, err
}

// Restore implements Backups.
func (b RPCBackups) Restore(ctx context.Context, site, snapshot, target string) (agentapi.BackupRestore, error) {
	return agentapi.BackupRestoreStart.Call(ctx, b.C, agentapi.BackupRestoreRequest{Site: site, Snapshot: snapshot, Target: target})
}

// GetRestore implements Backups.
func (b RPCBackups) GetRestore(ctx context.Context, id string) (agentapi.BackupRestore, error) {
	return agentapi.BackupRestoreGet.Call(ctx, b.C, agentapi.BackupRestoreGetRequest{ID: id})
}

// Import implements Backups.
func (b RPCBackups) Import(ctx context.Context, database, path string) (agentapi.DBJob, error) {
	return agentapi.DBImport.Call(ctx, b.C, agentapi.DBDumpRequest{Database: database, Path: path})
}

// ImportJob implements Backups.
func (b RPCBackups) ImportJob(ctx context.Context, id string) (agentapi.DBJob, error) {
	return agentapi.DBJobStatus.Call(ctx, b.C, agentapi.DBJobRequest{ID: id})
}

// ScanHostKey implements Backups.
func (RPCBackups) ScanHostKey(ctx context.Context, t agentapi.BackupTarget) (ssh.PublicKey, error) {
	return backup.FetchHostKey(ctx, t)
}

type backupJSON struct {
	agentapi.BackupInfo
	Status agentapi.BackupStatus `json:"status"`
	// Overdue: no run finished within the interval plus two hours
	// (ADR-0008, recommendation 8).
	Overdue bool `json:"overdue"`
}

// hostKeyJSON is a host key an SSH server presented, to confirm.
type hostKeyJSON struct {
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

type backupSnapshotListJSON struct {
	Snapshots []agentapi.BackupSnapshot `json:"snapshots"`
}

// PanelSites lists the panel's sites; hosting.Service.List implements it.
type PanelSites func(ctx context.Context) ([]store.Site, error)

// deletedSiteJSON is a deleted site with snapshots, or (Earlier) the
// snapshots of an earlier site whose name a newer site took (ADR-0074).
type deletedSiteJSON struct {
	Name      string    `json:"name"`
	Earlier   bool      `json:"earlier"`
	Snapshots int       `json:"snapshots"`
	Oldest    time.Time `json:"oldest"`
	Newest    time.Time `json:"newest"`
	// MarkedAt: the snapshots are marked for removal by the next run
	// since then; null when not.
	MarkedAt *time.Time `json:"markedAt"`
}

// deleteBackupsRequest confirms deleting the backups of a deleted site
// with its name.
type deleteBackupsRequest struct {
	Confirm string `json:"confirm"`
}

type forgetMarksJSON struct {
	Marks []agentapi.BackupForgetMark `json:"marks"`
}

type deletedSiteListJSON struct {
	Sites []deletedSiteJSON `json:"sites"`
}

type deletedRestoreRequest struct {
	// Target is the existing site to restore into.
	Target string `json:"target"`
}

func hasSnapshot(list []agentapi.BackupSnapshot, id string) bool {
	for _, s := range list {
		if s.ID == id {
			return true
		}
	}

	return false
}

func minTime(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}

	return a
}

func maxTime(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}

type hostKeyRequest struct {
	Key string `json:"key"`
}

type restoreImportRequest struct {
	Database string `json:"database"`
}

func backupFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	var ae *agentrpc.Error

	switch {
	case errors.Is(err, backup.ErrSnapshotNotFound), errors.Is(err, backup.ErrRestoreNotFound),
		errors.As(err, &ae) && ae.Code == agentrpc.CodeNotFound:
		writeError(w, http.StatusNotFound, "not_found", "no such snapshot or restore")
	case errors.Is(err, backup.ErrRestoreBusy):
		writeError(w, http.StatusConflict, "restore_running", "a restore is in progress")
	case errors.Is(err, agentapi.ErrBackupInvalid):
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())
	case errors.Is(err, backup.ErrBadHostKey):
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "the server offers no supported host key (Ed25519, ECDSA or RSA of 3072 bits and more)")
	case errors.Is(err, backup.ErrNotReady):
		writeError(w, http.StatusConflict, "not_ready", "set a backup target and confirm its host key first")
	case errors.Is(err, backup.ErrRunning):
		writeError(w, http.StatusConflict, "running", "a backup run is in progress")
	default:
		siteFail(log, w, err, what)
	}
}

// registerBackups adds the backup routes. Restores need the high-risk
// scope backups:restore; nothing here returns a secret. dbs is nil when
// the panel manages no sites; the dump import is then unavailable.
//
// sites lists the panel's sites; with it, a site shows only the snapshots
// taken since it was created, and older ones and those of deleted sites
// are reached through /api/v1/backups/deleted (ADR-0074). aud records
// restores of deleted sites; both may be nil, which turns that off.
func registerBackups(mux *routeMux, a authn, b Backups, dbs SiteDatabases, jobs DatabaseJobs, sites PanelSites, aud auditor, log *slog.Logger) {
	// importJob finds an import job: on this machine's MariaDB, or else
	// on an external server.
	importJob := func(ctx context.Context, id string) (agentapi.DBJob, error) {
		j, err := b.ImportJob(ctx, id)
		if err == nil || jobs == nil {
			return j, err
		}

		if rj, rerr := jobs.RemoteJob(ctx, id); rerr == nil {
			return rj, nil
		}

		return j, err
	}

	// restoreSite and importSite name the site of a restore or of an import
	// job, so a user follows only those of granted sites (ADR-0035).
	restoreSite := func(r *http.Request) (string, bool) {
		id := r.PathValue("id")
		if (agentapi.BackupRestoreGetRequest{ID: id}).Validate() != nil {
			return "", false
		}

		res, err := b.GetRestore(r.Context(), id)
		if err != nil {
			return "", false
		}

		return res.Site, true
	}

	importSite := func(r *http.Request) (string, bool) {
		id := r.PathValue("id")
		if dbs == nil || (agentapi.DBJobRequest{ID: id}).Validate() != nil {
			return "", false
		}

		job, err := importJob(r.Context(), id)
		if err != nil {
			return "", false
		}

		all, err := dbs(r.Context(), "")
		if err != nil {
			return "", false
		}

		for _, d := range all {
			if d.Name == job.Database {
				return d.Site, true
			}
		}

		return "", false
	}

	info := func(w http.ResponseWriter, r *http.Request, code int, i agentapi.BackupInfo) {
		st, err := b.Status(r.Context())
		if err != nil {
			backupFail(log, w, err, "backup status")

			return
		}

		writeJSON(w, code, backupJSON{BackupInfo: i, Status: st, Overdue: agentapi.BackupOverdue(i.Settings, i.Ready, st, time.Now())})
	}

	mux.Op("GET /api/v1/backups", openapi.Operation{
		ID: "getBackups", Tag: "backups", Summary: "Backup settings and the state of the last run", Out: backupJSON{},
	}, a.allow("backups.server:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		i, err := b.Get(r.Context())
		if err != nil {
			backupFail(log, w, err, "backup settings")

			return
		}

		info(w, r, http.StatusOK, i)
	}))

	mux.Op("PUT /api/v1/backups/settings", openapi.Operation{
		ID: "setBackupSettings", Tag: "backups", Summary: "Set the backup target, retention and window", In: agentapi.BackupSettings{}, Out: backupJSON{},
	}, a.allow("backups.server:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		var s agentapi.BackupSettings
		if !decodeJSON(w, r, &s) {
			return
		}

		if err := s.Validate(); err != nil {
			backupFail(log, w, err, "backup settings")

			return
		}

		i, err := b.Set(r.Context(), s)
		if err != nil {
			backupFail(log, w, err, "set backup settings")

			return
		}

		info(w, r, http.StatusOK, i)
	}))

	// The scan only reads what the server presents; nothing is trusted
	// until PUT /hostkey with the key the administrator confirmed.
	mux.Op("POST /api/v1/backups/hostkey/scan", openapi.Operation{
		ID: "scanBackupHostKey", Tag: "backups", Summary: "Read the host key the backup target presents", Out: hostKeyJSON{},
	}, a.allow("backups.server:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		i, err := b.Get(r.Context())
		if err != nil {
			backupFail(log, w, err, "backup settings")

			return
		}

		if i.Settings.Target == nil {
			writeError(w, http.StatusConflict, "no_target", "set a backup target first")

			return
		}

		key, err := b.ScanHostKey(r.Context(), *i.Settings.Target)
		if err != nil {
			if errors.Is(err, backup.ErrBadHostKey) {
				backupFail(log, w, err, "scan host key")

				return
			}

			log.WarnContext(r.Context(), "scan host key", "err", err)
			writeError(w, http.StatusBadGateway, "unreachable", "the backup target did not answer over SSH")

			return
		}

		writeJSON(w, http.StatusOK, hostKeyJSON{
			Key:         strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
			Fingerprint: ssh.FingerprintSHA256(key),
		})
	}))

	mux.Op("PUT /api/v1/backups/hostkey", openapi.Operation{
		ID: "setBackupHostKey", Tag: "backups", Summary: "Trust a confirmed host key of the backup target", In: hostKeyRequest{}, Out: backupJSON{},
	}, a.allow("backups.server:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		var in hostKeyRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		if _, err := backup.ParseHostKey(in.Key); err != nil {
			backupFail(log, w, err, "set host key")

			return
		}

		i, err := b.SetHostKey(r.Context(), in.Key)
		if err != nil {
			backupFail(log, w, err, "set host key")

			return
		}

		info(w, r, http.StatusOK, i)
	}))

	mux.Op("POST /api/v1/backups/run", openapi.Operation{
		ID: "runBackup", Tag: "backups", Summary: "Start a backup now", Status: http.StatusAccepted,
	}, a.allow("backups.server:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		if err := b.Run(r.Context()); err != nil {
			backupFail(log, w, err, "start backup")

			return
		}

		w.WriteHeader(http.StatusAccepted)
	}))

	// since is when the panel's site of that name was created; ok is
	// false when there is none. Without a site list every name counts as
	// existing since ever.
	since := func(ctx context.Context, name string) (created time.Time, ok bool, err error) {
		if sites == nil {
			return time.Time{}, true, nil
		}

		all, err := sites(ctx)
		if err != nil {
			return time.Time{}, false, err
		}

		for _, st := range all {
			if st.Name == name {
				return st.CreatedAt.Truncate(time.Second), true, nil
			}
		}

		return time.Time{}, false, nil
	}

	// siteSnapshots are the snapshots of the site of that name taken since
	// it was created; deleted the others of that name (ADR-0074).
	siteSnapshots := func(ctx context.Context, name string) (current, deleted []agentapi.BackupSnapshot, exists bool, err error) {
		created, exists, err := since(ctx, name)
		if err != nil {
			return nil, nil, false, err
		}

		all, err := b.Snapshots(ctx, name)
		if err != nil {
			return nil, nil, false, err
		}

		current, deleted = []agentapi.BackupSnapshot{}, []agentapi.BackupSnapshot{}

		for _, sn := range all {
			if exists && !sn.Time.Before(created) {
				current = append(current, sn)
			} else {
				deleted = append(deleted, sn)
			}
		}

		return current, deleted, exists, nil
	}

	snapshots := func(w http.ResponseWriter, r *http.Request, site string) {
		var (
			s      []agentapi.BackupSnapshot
			exists = true
			err    error
		)

		if site == "" {
			s, err = b.Snapshots(r.Context(), "")
		} else {
			s, _, exists, err = siteSnapshots(r.Context(), site)
		}

		if err != nil {
			backupFail(log, w, err, "list snapshots")

			return
		}

		if !exists {
			writeError(w, http.StatusNotFound, "not_found", "no such site")

			return
		}

		if s == nil {
			s = []agentapi.BackupSnapshot{}
		}

		writeJSON(w, http.StatusOK, backupSnapshotListJSON{Snapshots: s})
	}

	mux.Op("GET /api/v1/sites/{name}/backups", openapi.Operation{
		ID: "listSiteBackups", Tag: "backups", Summary: "List the snapshots of a site", Out: backupSnapshotListJSON{},
	}, a.allow("backups:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		name := r.PathValue("name")
		if agentapi.ValidateSiteUser(name) != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such site")

			return
		}

		snapshots(w, r, name)
	}))

	mux.Op("GET /api/v1/backups/panel", openapi.Operation{
		ID: "listPanelBackups", Tag: "backups", Summary: "List the snapshots of the panel itself", Out: backupSnapshotListJSON{},
	}, a.allow("backups.server:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		snapshots(w, r, "")
	}))

	// The snapshots of deleted sites, and of an earlier site whose name a
	// newer site took, are for administrators only (ADR-0074).
	mux.Op("GET /api/v1/backups/deleted", openapi.Operation{
		ID: "listDeletedSiteBackups", Tag: "backups", Summary: "List deleted sites that still have snapshots", Out: deletedSiteListJSON{},
	}, a.allow("backups.server:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		resp, err := b.SnapshotSites(r.Context())
		if err != nil {
			backupFail(log, w, err, "list snapshot sites")

			return
		}

		out := deletedSiteListJSON{Sites: []deletedSiteJSON{}}

		marked := func(site string) *time.Time {
			for _, m := range resp.Marks {
				if m.Site == site {
					t := m.Requested

					return &t
				}
			}

			return nil
		}

		for _, sum := range resp.Sites {
			created, exists, err := since(r.Context(), sum.Site)
			if err != nil {
				backupFail(log, w, err, "list sites")

				return
			}

			switch {
			case !exists:
				out.Sites = append(out.Sites, deletedSiteJSON{Name: sum.Site, Snapshots: sum.Snapshots, Oldest: sum.Oldest, Newest: sum.Newest, MarkedAt: marked(sum.Site)})
			case sum.Oldest.Before(created):
				_, old, _, err := siteSnapshots(r.Context(), sum.Site)
				if err != nil {
					backupFail(log, w, err, "list snapshots")

					return
				}

				if len(old) > 0 {
					d := deletedSiteJSON{Name: sum.Site, Earlier: true, Snapshots: len(old), Oldest: old[0].Time, Newest: old[0].Time, MarkedAt: marked(sum.Site)}
					for _, sn := range old {
						d.Oldest, d.Newest = minTime(d.Oldest, sn.Time), maxTime(d.Newest, sn.Time)
					}

					out.Sites = append(out.Sites, d)
				}
			}
		}

		writeJSON(w, http.StatusOK, out)
	}))

	mux.Op("GET /api/v1/backups/deleted/{name}", openapi.Operation{
		ID: "listDeletedSiteSnapshots", Tag: "backups", Summary: "List the snapshots of a deleted site, or of an earlier site of that name", Out: backupSnapshotListJSON{},
	}, a.allow("backups.server:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		name := r.PathValue("name")
		if agentapi.ValidateSiteUser(name) != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such deleted site")

			return
		}

		_, old, _, err := siteSnapshots(r.Context(), name)
		if err != nil {
			backupFail(log, w, err, "list snapshots")

			return
		}

		if len(old) == 0 {
			writeError(w, http.StatusNotFound, "not_found", "no such deleted site")

			return
		}

		writeJSON(w, http.StatusOK, backupSnapshotListJSON{Snapshots: old})
	}))

	// Deleting the backups of a deleted site marks them for the next run,
	// which removes them (ADR-0074, decided by Kim 2026-11-26); until then
	// the mark can be taken back. For an earlier site of a reused name only
	// the snapshots from before the current site was created are marked.
	forget := func(w http.ResponseWriter, r *http.Request, p auth.Principal, cancel bool) {
		name := r.PathValue("name")
		if agentapi.ValidateSiteUser(name) != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such deleted site")

			return
		}

		if !cancel {
			var in deleteBackupsRequest
			if !decodeJSON(w, r, &in) {
				return
			}

			if in.Confirm != name {
				writeError(w, http.StatusUnprocessableEntity, "confirm_mismatch", "type the site's name to confirm")

				return
			}
		}

		created, exists, err := since(r.Context(), name)
		if err != nil {
			backupFail(log, w, err, "list sites")

			return
		}

		req := agentapi.BackupForgetRequest{Site: name, Cancel: cancel}
		if exists {
			if sites == nil {
				writeError(w, http.StatusConflict, "source_exists", "this site exists; its backups are not deleted here")

				return
			}

			req.Before = created
		}

		marks, err := b.Forget(r.Context(), req)

		var ae *agentrpc.Error

		switch {
		case err == nil:
		case errors.Is(err, backup.ErrSourceExists), errors.As(err, &ae) && ae.Code == agentrpc.CodeConflict:
			writeError(w, http.StatusConflict, "source_exists", "this site exists; its backups are not deleted here")

			return
		default:
			backupFail(log, w, err, "mark backups for removal")

			return
		}

		action := "backup.delete"
		if cancel {
			action = "backup.delete.cancel"
		}

		if aud != nil {
			uid := p.User.ID
			if err := aud.Audit(r.Context(), &uid, action, name, nil); err != nil {
				log.ErrorContext(r.Context(), "audit log", "action", action, "err", err)
			}
		}

		log.InfoContext(r.Context(), "backups of a deleted site marked", "site", name, "cancel", cancel)
		writeJSON(w, http.StatusOK, forgetMarksJSON{Marks: marks})
	}

	mux.Op("PUT /api/v1/backups/deleted/{name}/removal", openapi.Operation{
		ID: "deleteDeletedSiteBackups", Tag: "backups", Summary: "Delete the backups of a deleted site with the next backup run",
		In: deleteBackupsRequest{}, Out: forgetMarksJSON{},
	}, a.allow("backups.server:delete", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		forget(w, r, p, false)
	}))

	mux.Op("DELETE /api/v1/backups/deleted/{name}/removal", openapi.Operation{
		ID: "keepDeletedSiteBackups", Tag: "backups", Summary: "Keep the backups of a deleted site that were marked for deletion", Out: forgetMarksJSON{},
	}, a.allow("backups.server:delete", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		forget(w, r, p, true)
	}))

	// A deleted site has no home, so its snapshot goes into a site the
	// administrator picks, as restore-<time> and later a new database of
	// that site. An earlier site's snapshot goes only into the site that
	// now has its name; the agent refuses a foreign target while the
	// snapshot's site exists.
	mux.Op("POST /api/v1/backups/deleted/{name}/{snapshot}/restore", openapi.Operation{
		ID: "restoreDeletedSiteBackup", Tag: "backups", Summary: "Restore a snapshot of a deleted site into a new directory of another site",
		In: deletedRestoreRequest{}, Out: agentapi.BackupRestore{}, Status: http.StatusAccepted,
	}, a.allow("backups.server:restore", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in deletedRestoreRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		req := agentapi.BackupRestoreRequest{Site: r.PathValue("name"), Snapshot: r.PathValue("snapshot"), Target: in.Target}
		if (agentapi.BackupRestoreRequest{Site: req.Site, Snapshot: req.Snapshot}).Validate() != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such snapshot or restore")

			return
		}

		if agentapi.ValidateSiteUser(in.Target) != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "target: name the site to restore into")

			return
		}

		_, old, exists, err := siteSnapshots(r.Context(), req.Site)
		if err != nil {
			backupFail(log, w, err, "list snapshots")

			return
		}

		if !hasSnapshot(old, req.Snapshot) {
			writeError(w, http.StatusNotFound, "not_found", "no such snapshot or restore")

			return
		}

		if _, ok, err := since(r.Context(), in.Target); err != nil {
			backupFail(log, w, err, "list sites")

			return
		} else if !ok {
			writeError(w, http.StatusNotFound, "target_not_found", "no such site to restore into")

			return
		}

		if exists && in.Target != req.Site {
			writeError(w, http.StatusConflict, "source_exists", "a site of this name exists; restore its earlier snapshots into that site")

			return
		}

		target := in.Target
		if target == req.Site {
			target = ""
		}

		res, err := b.Restore(r.Context(), req.Site, req.Snapshot, target)
		if err != nil {
			backupFail(log, w, err, "start restore")

			return
		}

		if aud != nil {
			uid := p.User.ID
			detail := map[string]string{"source": req.Site, "snapshot": req.Snapshot, "kind": res.Kind, "dir": res.Dir}

			if err := aud.Audit(r.Context(), &uid, "backup.restore.deleted", in.Target, detail); err != nil {
				log.ErrorContext(r.Context(), "audit log", "action", "backup.restore.deleted", "err", err)
			}
		}

		log.InfoContext(r.Context(), "restore of a deleted site started", "id", res.ID, "source", req.Site, "site", res.Site, "snapshot", res.Snapshot, "dir", res.Dir)
		writeJSON(w, http.StatusAccepted, res)
	}))

	// A restore writes a new directory restore-<time> into the site's
	// home and never touches live files; it is still high-risk, because
	// it puts old data back into reach of the site.
	mux.Op("POST /api/v1/sites/{name}/backups/{snapshot}/restore", openapi.Operation{
		ID: "restoreSiteBackup", Tag: "backups", Summary: "Restore a snapshot into a new directory of the site", Out: agentapi.BackupRestore{}, Status: http.StatusAccepted,
	}, a.allow("backups:restore", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		req := agentapi.BackupRestoreRequest{Site: r.PathValue("name"), Snapshot: r.PathValue("snapshot")}
		if req.Validate() != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such snapshot or restore")

			return
		}

		// Only a snapshot this site shows: one of an earlier site of the
		// same name is for administrators (ADR-0074).
		current, _, _, err := siteSnapshots(r.Context(), req.Site)
		if err != nil {
			backupFail(log, w, err, "list snapshots")

			return
		}

		if !hasSnapshot(current, req.Snapshot) {
			writeError(w, http.StatusNotFound, "not_found", "no such snapshot or restore")

			return
		}

		res, err := b.Restore(r.Context(), req.Site, req.Snapshot, "")
		if err != nil {
			backupFail(log, w, err, "start restore")

			return
		}

		log.InfoContext(r.Context(), "restore started", "id", res.ID, "site", res.Site, "snapshot", res.Snapshot, "dir", res.Dir)
		writeJSON(w, http.StatusAccepted, res)
	}))

	// A restored dump goes into a database of the same site that was
	// created after the restore was asked for, so a restore never reaches
	// data that existed before it (ADR-0008, recommendation 7). The import
	// itself is the agent's db.import, with a job user that holds rights on
	// that database only.
	mux.Op("POST /api/v1/backups/restores/{id}/import", openapi.Operation{
		ID: "importRestoredDump", Tag: "backups", Summary: "Import a restored dump into a new database of the site", In: restoreImportRequest{}, Out: agentapi.DBJob{}, Status: http.StatusAccepted,
	}, a.allowSite("backups:restore", restoreSite, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		id := r.PathValue("id")
		if (agentapi.BackupRestoreGetRequest{ID: id}).Validate() != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such snapshot or restore")

			return
		}

		var in restoreImportRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		res, err := b.GetRestore(r.Context(), id)
		if err != nil {
			backupFail(log, w, err, "restore status")

			return
		}

		switch {
		case dbs == nil:
			writeError(w, http.StatusConflict, "unavailable", "databases are not managed by this panel")

			return
		case res.Kind != "db":
			writeError(w, http.StatusConflict, "not_a_dump", "this restore holds files, not a database dump")

			return
		case res.State != agentapi.RestoreDone:
			writeError(w, http.StatusConflict, "restore_not_done", "the restore has not finished")

			return
		}

		list, err := dbs(r.Context(), res.Site)
		if err != nil {
			siteFail(log, w, err, "list databases")

			return
		}

		var target *store.Database

		for i := range list {
			if list[i].Name == in.Database {
				target = &list[i]
			}
		}

		switch {
		case target == nil:
			writeError(w, http.StatusNotFound, "not_found", "no such database of this site")

			return
		case target.CreatedAt.Before(res.Created.Truncate(time.Second)):
			writeError(w, http.StatusConflict, "database_not_new", "import into a database created after the restore, never over existing data")

			return
		}

		// The dump's file name gives its format; restores recorded
		// before DumpFile existed hold a MariaDB dump.
		file := res.DumpFile
		if file == "" {
			file = res.Database + ".sql"
		}

		if file != res.Database+".sql" && file != res.Database+agentapi.PGCustomSuffix {
			writeError(w, http.StatusConflict, "not_a_dump", "this restore holds no database dump")

			return
		}

		var job agentapi.DBJob

		switch {
		case target.Server != store.LocalDBServer && jobs != nil:
			// hosting checks that the format fits the server: a .dump
			// loads only into PostgreSQL.
			job, err = jobs.ImportDatabase(r.Context(), p.User.ID, target.Name, res.Dir+"/"+file)
			if err != nil {
				dbFail(log, w, err, "import restored dump")

				return
			}
		case target.Server != store.LocalDBServer || strings.HasSuffix(file, agentapi.PGCustomSuffix):
			writeError(w, http.StatusConflict, "wrong_server", "this dump does not load into a database on this server")

			return
		default:
			job, err = b.Import(r.Context(), target.Name, res.Dir+"/"+file)
			if err != nil {
				backupFail(log, w, err, "import restored dump")

				return
			}
		}

		log.InfoContext(r.Context(), "restored dump import started", "restore", res.ID, "database", target.Name, "job", job.ID)
		writeJSON(w, http.StatusAccepted, job)
	}))

	mux.Op("GET /api/v1/backups/imports/{id}", openapi.Operation{
		ID: "getRestoreImport", Tag: "backups", Summary: "State of a restored dump import", Out: agentapi.DBJob{},
	}, a.allowSite("backups:read", importSite, func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		id := r.PathValue("id")
		if (agentapi.DBJobRequest{ID: id}).Validate() != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such import")

			return
		}

		job, err := importJob(r.Context(), id)
		if err != nil {
			backupFail(log, w, err, "import status")

			return
		}

		writeJSON(w, http.StatusOK, job)
	}))

	mux.Op("GET /api/v1/backups/restores/{id}", openapi.Operation{
		ID: "getRestore", Tag: "backups", Summary: "State of a restore", Out: agentapi.BackupRestore{},
	}, a.allowSite("backups:read", restoreSite, func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		id := r.PathValue("id")
		if (agentapi.BackupRestoreGetRequest{ID: id}).Validate() != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such snapshot or restore")

			return
		}

		res, err := b.GetRestore(r.Context(), id)
		if err != nil {
			backupFail(log, w, err, "restore status")

			return
		}

		writeJSON(w, http.StatusOK, res)
	}))
}
