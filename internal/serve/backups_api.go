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
	// ScanHostKey connects to the target and returns its host key, for
	// the administrator to confirm.
	ScanHostKey(ctx context.Context, t agentapi.BackupTarget) (ssh.PublicKey, error)
	// Restore starts restoring a snapshot of a site into a new directory
	// in its home; GetRestore reports how it goes.
	Restore(ctx context.Context, site, snapshot string) (agentapi.BackupRestore, error)
	GetRestore(ctx context.Context, id string) (agentapi.BackupRestore, error)
	// Import reads a dump below a site's home into a database (the
	// agent's db.import); ImportJob reports how it goes.
	Import(ctx context.Context, database, path string) (agentapi.DBJob, error)
	ImportJob(ctx context.Context, id string) (agentapi.DBJob, error)
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

// Restore implements Backups.
func (b RPCBackups) Restore(ctx context.Context, site, snapshot string) (agentapi.BackupRestore, error) {
	return agentapi.BackupRestoreStart.Call(ctx, b.C, agentapi.BackupRestoreRequest{Site: site, Snapshot: snapshot})
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
func registerBackups(mux *routeMux, a authn, b Backups, dbs SiteDatabases, log *slog.Logger) {
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

		job, err := b.ImportJob(r.Context(), id)
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

		writeJSON(w, code, backupJSON{BackupInfo: i, Status: st})
	}

	mux.HandleFunc("GET /api/v1/backups", a.allow("backups.server:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		i, err := b.Get(r.Context())
		if err != nil {
			backupFail(log, w, err, "backup settings")

			return
		}

		info(w, r, http.StatusOK, i)
	}))

	mux.HandleFunc("PUT /api/v1/backups/settings", a.allow("backups.server:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
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
	mux.HandleFunc("POST /api/v1/backups/hostkey/scan", a.allow("backups.server:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
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

		writeJSON(w, http.StatusOK, map[string]string{
			"key":         strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
			"fingerprint": ssh.FingerprintSHA256(key),
		})
	}))

	mux.HandleFunc("PUT /api/v1/backups/hostkey", a.allow("backups.server:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		var in struct {
			Key string `json:"key"`
		}
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

	mux.HandleFunc("POST /api/v1/backups/run", a.allow("backups.server:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		if err := b.Run(r.Context()); err != nil {
			backupFail(log, w, err, "start backup")

			return
		}

		w.WriteHeader(http.StatusAccepted)
	}))

	snapshots := func(w http.ResponseWriter, r *http.Request, site string) {
		s, err := b.Snapshots(r.Context(), site)
		if err != nil {
			backupFail(log, w, err, "list snapshots")

			return
		}

		if s == nil {
			s = []agentapi.BackupSnapshot{}
		}

		writeJSON(w, http.StatusOK, map[string][]agentapi.BackupSnapshot{"snapshots": s})
	}

	mux.HandleFunc("GET /api/v1/sites/{name}/backups", a.allow("backups:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		name := r.PathValue("name")
		if agentapi.ValidateSiteUser(name) != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such site")

			return
		}

		snapshots(w, r, name)
	}))

	mux.HandleFunc("GET /api/v1/backups/panel", a.allow("backups.server:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		snapshots(w, r, "")
	}))

	// A restore writes a new directory restore-<time> into the site's
	// home and never touches live files; it is still high-risk, because
	// it puts old data back into reach of the site.
	mux.HandleFunc("POST /api/v1/sites/{name}/backups/{snapshot}/restore", a.allow("backups:restore", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		req := agentapi.BackupRestoreRequest{Site: r.PathValue("name"), Snapshot: r.PathValue("snapshot")}
		if req.Validate() != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such snapshot or restore")

			return
		}

		res, err := b.Restore(r.Context(), req.Site, req.Snapshot)
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
	mux.HandleFunc("POST /api/v1/backups/restores/{id}/import", a.allowSite("backups:restore", restoreSite, func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		id := r.PathValue("id")
		if (agentapi.BackupRestoreGetRequest{ID: id}).Validate() != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such snapshot or restore")

			return
		}

		var in struct {
			Database string `json:"database"`
		}
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

		job, err := b.Import(r.Context(), target.Name, res.Dir+"/"+res.Database+".sql")
		if err != nil {
			backupFail(log, w, err, "import restored dump")

			return
		}

		log.InfoContext(r.Context(), "restored dump import started", "restore", res.ID, "database", target.Name, "job", job.ID)
		writeJSON(w, http.StatusAccepted, job)
	}))

	mux.HandleFunc("GET /api/v1/backups/imports/{id}", a.allowSite("backups:read", importSite, func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		id := r.PathValue("id")
		if (agentapi.DBJobRequest{ID: id}).Validate() != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such import")

			return
		}

		job, err := b.ImportJob(r.Context(), id)
		if err != nil {
			backupFail(log, w, err, "import status")

			return
		}

		writeJSON(w, http.StatusOK, job)
	}))

	mux.HandleFunc("GET /api/v1/backups/restores/{id}", a.allowSite("backups:read", restoreSite, func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
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
