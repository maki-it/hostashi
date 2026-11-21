// SPDX-License-Identifier: AGPL-3.0-or-later

package dbjob

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/mariadb"
	"github.com/maki-it/hostashi/internal/mariadbctl"
)

// Tools runs the client programs; package mariadbctl is the real one.
type Tools interface {
	Import(ctx context.Context, r mariadbctl.Run, in io.Reader) error
	Export(ctx context.Context, r mariadbctl.Run, out io.Writer) error
}

// RunConfig is what the job needs. The agent's paths are fixed in the
// unit; tests point them elsewhere.
type RunConfig struct {
	Dir      string // job records
	StateDir string // the site registry
	TempDir  string // for the CA file; the unit makes /tmp private
	Tools    Tools  // nil: mariadb.RealTools
	Log      *slog.Logger
	// NotRoot skips the check for euid 0 (tests).
	NotRoot bool
}

// ErrFailed is returned after a failed job was recorded.
var ErrFailed = errors.New("dbjob: the job failed, see its record")

// RunOnce is hostashi dbjob run ID, one job as root, started by the agent
// in hostashi-dbjob@ID.service. It removes the credential from the record
// before it connects and writes the outcome whatever happens after that.
func RunOnce(ctx context.Context, cfg RunConfig, id string) error {
	if !cfg.NotRoot && os.Geteuid() != 0 {
		return errors.New("dbjob: must run as root")
	}

	if !jobIDRe.MatchString(id) {
		return fmt.Errorf("%w: job id", ErrInvalid)
	}

	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}

	if cfg.Tools == nil {
		cfg.Tools = mariadb.RealTools{}
	}

	r, err := readRecord(cfg.Dir, id, owner())
	if err != nil {
		return err
	}

	if r.State != agentapi.JobRunning || r.Request == nil {
		return fmt.Errorf("dbjob: job %s has run already", id)
	}

	req := *r.Request
	r.Request = nil

	if err := writeRecord(cfg.Dir, r); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()

	err = run(ctx, cfg, req)

	r.Finished, r.State = time.Now().UTC(), agentapi.JobDone
	if err != nil {
		r.State, r.Error = agentapi.JobFailed, jobError(err)
		cfg.Log.ErrorContext(ctx, "database job failed", "id", id, "kind", r.Kind, "database", r.Database, "err", err)
	}

	if werr := writeRecord(cfg.Dir, r); werr != nil {
		return fmt.Errorf("write job record: %w", werr)
	}

	if err != nil {
		return ErrFailed
	}

	return nil
}

// Finish is hostashi dbjob finish ID, the unit's ExecStopPost: a job that
// ended without writing its outcome (killed, timed out) is marked failed,
// and a credential that is still in the record is removed.
func Finish(cfg RunConfig, id string) error {
	if !jobIDRe.MatchString(id) {
		return fmt.Errorf("%w: job id", ErrInvalid)
	}

	r, err := readRecord(cfg.Dir, id, owner())
	if err != nil {
		return err
	}

	if r.State != agentapi.JobRunning && r.Request == nil {
		return nil
	}

	r.Request = nil
	if r.State == agentapi.JobRunning {
		r.State, r.Error, r.Finished = agentapi.JobFailed, "stopped", time.Now().UTC()
	}

	return writeRecord(cfg.Dir, r)
}

func run(ctx context.Context, cfg RunConfig, req agentapi.DBRemoteJobRequest) (err error) {
	if err := req.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	home, err := siteHome(cfg.StateDir, req.Site, owner())
	if err != nil {
		return err
	}

	h := mariadb.SiteHome{Home: home.Path, UID: home.UID, GID: home.GID}

	remote := &mariadbctl.Remote{Host: req.Host, Port: req.Port, TLS: req.TLS}

	if req.TLS == "verify-ca" {
		dir, cleanup, err := caFile(cfg.TempDir, req.CAPEM, home.UID, home.GID)
		if err != nil {
			return err
		}
		defer cleanup()

		remote.CAFile = filepath.Join(dir, "ca.pem")
	}

	rn := mariadbctl.Run{Database: req.Database, UID: home.UID, GID: home.GID, User: req.User, Password: req.Password, Remote: remote}

	if req.Kind == "import" {
		return importDump(ctx, cfg.Tools, h, req.Path, rn)
	}

	return exportDump(ctx, cfg.Tools, h, req.Path, rn)
}

func importDump(ctx context.Context, tools Tools, h mariadb.SiteHome, rel string, rn mariadbctl.Run) error {
	f, err := mariadb.OpenDumpFile(h, rel)
	if err != nil {
		return err
	}
	defer f.Close()

	var in io.Reader = f

	if strings.HasSuffix(rel, ".gz") {
		zr, err := gzip.NewReader(f)
		if err != nil {
			return fmt.Errorf("%w: not a gzip file", mariadb.ErrFileUnsafe)
		}
		defer zr.Close()

		in = zr
	}

	return tools.Import(ctx, rn, in)
}

// exportDump writes a new file; a failed or partial one is removed again.
func exportDump(ctx context.Context, tools Tools, h mariadb.SiteHome, rel string, rn mariadbctl.Run) (err error) {
	f, err := mariadb.CreateDumpFile(h, rel)
	if err != nil {
		return err
	}

	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}

		if err != nil {
			_ = mariadb.RemoveDumpFile(h, rel)
		}
	}()

	var (
		out io.Writer = f
		zw  *gzip.Writer
	)

	if strings.HasSuffix(rel, ".gz") {
		zw = gzip.NewWriter(f)
		out = zw
	}

	if err := tools.Export(ctx, rn, out); err != nil {
		return err
	}

	if zw != nil {
		if err := zw.Close(); err != nil {
			return fmt.Errorf("dbjob: finish gzip: %w", err)
		}
	}

	return f.Sync()
}

// caFile writes the CA into a new directory that only the site user can
// read: the client runs as that user and opens the file by path.
func caFile(tempDir, pemData string, uid, gid uint32) (string, func(), error) {
	dir, err := os.MkdirTemp(tempDir, "hostashi-dbjob-")
	if err != nil {
		return "", nil, fmt.Errorf("dbjob: CA directory: %w", err)
	}

	cleanup := func() { _ = os.RemoveAll(dir) }

	if err := os.WriteFile(filepath.Join(dir, "ca.pem"), []byte(pemData), 0o600); err != nil {
		cleanup()

		return "", nil, fmt.Errorf("dbjob: CA file: %w", err)
	}

	for _, p := range []string{filepath.Join(dir, "ca.pem"), dir} {
		if err := unix.Lchown(p, int(uid), int(gid)); err != nil {
			cleanup()

			return "", nil, fmt.Errorf("dbjob: CA file owner: %w", err)
		}
	}

	return dir, cleanup, nil
}

// jobError is the text a caller sees: fixed, at most a line number and an
// error code from mariadbctl.
func jobError(err error) string {
	switch {
	case errors.Is(err, mariadbctl.ErrFailed):
		return strings.TrimPrefix(err.Error(), "mariadbctl: failed: ")
	case errors.Is(err, mariadb.ErrFileUnsafe):
		return "the dump file is missing, not a regular file of the site user, or not a gzip file"
	case errors.Is(err, mariadb.ErrFileExists):
		return "the export file exists already"
	case errors.Is(err, ErrNoSite):
		return "the site does not exist any more"
	default:
		return "internal error"
	}
}
