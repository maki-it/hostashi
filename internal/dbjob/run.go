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
	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/mariadb"
	"github.com/maki-it/hostashi/internal/mariadbctl"
	"github.com/maki-it/hostashi/internal/pgclient"
	"github.com/maki-it/hostashi/internal/pgctl"
)

// PGTools runs the PostgreSQL client programs; package pgctl is the real
// one (PGRealTools).
type PGTools interface {
	Dump(ctx context.Context, r pgctl.Run, out io.Writer) error
	DumpSQL(ctx context.Context, r pgctl.Run, out io.Writer) error
	Restore(ctx context.Context, r pgctl.Run, role string, in io.Reader) error
	Exec(ctx context.Context, r pgctl.Run, role string, in io.Reader) error
}

// PGRealTools are the functions of package pgctl.
type PGRealTools struct{}

// Dump is pgctl.Dump.
func (PGRealTools) Dump(ctx context.Context, r pgctl.Run, out io.Writer) error {
	return pgctl.Dump(ctx, r, out)
}

// DumpSQL is pgctl.DumpSQL.
func (PGRealTools) DumpSQL(ctx context.Context, r pgctl.Run, out io.Writer) error {
	return pgctl.DumpSQL(ctx, r, out)
}

// Restore is pgctl.Restore.
func (PGRealTools) Restore(ctx context.Context, r pgctl.Run, role string, in io.Reader) error {
	return pgctl.Restore(ctx, r, role, in)
}

// Exec is pgctl.Exec.
func (PGRealTools) Exec(ctx context.Context, r pgctl.Run, role string, in io.Reader) error {
	return pgctl.Exec(ctx, r, role, in)
}

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
	PGTools  PGTools
	// PGRoot is prefixed to /usr/lib/postgresql, where the newest
	// installed client is picked (tests); empty means /.
	PGRoot string
	Log    *slog.Logger
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

	if cfg.PGTools == nil {
		cfg.PGTools = PGRealTools{}
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

	if req.Engine == agentapi.RemoteEnginePostgreSQL {
		return runPG(ctx, cfg, req, h)
	}

	remote := &mariadbctl.Remote{Host: req.Host, Port: req.Port, TLS: req.TLS}

	if req.TLS == "verify-ca" {
		dir, cleanup, err := jobDir(cfg.TempDir, req.CAPEM, home.UID, home.GID)
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

// runPG runs a job against a PostgreSQL server with the newest installed
// client. The passfile and the CA go into a directory of the site user in
// the private /tmp: the tools run as that user.
func runPG(ctx context.Context, cfg RunConfig, req agentapi.DBRemoteJobRequest, h mariadb.SiteHome) error {
	majors := (&pgclient.Manager{Root: cfg.PGRoot}).Installed()
	if len(majors) == 0 {
		return ErrNoPGClient
	}

	dir, cleanup, err := jobDir(cfg.TempDir, req.CAPEM, h.UID, h.GID)
	if err != nil {
		return err
	}
	defer cleanup()

	rn := pgctl.Run{
		Major: majors[len(majors)-1], Database: req.Database, UID: h.UID, GID: h.GID,
		User: req.User, Password: req.Password, Host: req.Host, Port: req.Port, TLS: req.TLS,
		TempDir: dir, Root: cfg.PGRoot,
	}

	if req.TLS == "verify-ca" {
		rn.CAFile = filepath.Join(dir, "ca.pem")
	}

	role := dbserver.OwnerRole(req.Database)
	custom := strings.HasSuffix(req.Path, agentapi.PGCustomSuffix)

	if req.Kind == "import" {
		f, err := mariadb.OpenDumpFile(h, req.Path)
		if err != nil {
			return err
		}
		defer f.Close()

		if custom {
			return cfg.PGTools.Restore(ctx, rn, role, f)
		}

		in, done, err := maybeGunzip(f, req.Path)
		if err != nil {
			return err
		}
		defer done()

		return cfg.PGTools.Exec(ctx, rn, role, in)
	}

	return writeDump(h, req.Path, func(out io.Writer) error {
		if custom {
			return cfg.PGTools.Dump(ctx, rn, out)
		}

		return cfg.PGTools.DumpSQL(ctx, rn, out)
	})
}

// ErrNoPGClient: no PostgreSQL client is installed.
var ErrNoPGClient = errors.New("dbjob: no PostgreSQL client is installed")

func maybeGunzip(f io.Reader, rel string) (io.Reader, func(), error) {
	if !strings.HasSuffix(rel, ".gz") {
		return f, func() {}, nil
	}

	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: not a gzip file", mariadb.ErrFileUnsafe)
	}

	return zr, func() { _ = zr.Close() }, nil
}

func importDump(ctx context.Context, tools Tools, h mariadb.SiteHome, rel string, rn mariadbctl.Run) error {
	f, err := mariadb.OpenDumpFile(h, rel)
	if err != nil {
		return err
	}
	defer f.Close()

	in, done, err := maybeGunzip(f, rel)
	if err != nil {
		return err
	}
	defer done()

	return tools.Import(ctx, rn, in)
}

func exportDump(ctx context.Context, tools Tools, h mariadb.SiteHome, rel string, rn mariadbctl.Run) error {
	return writeDump(h, rel, func(out io.Writer) error { return tools.Export(ctx, rn, out) })
}

// writeDump writes a new file with dump, gzipped for .gz; a failed or
// partial one is removed again.
func writeDump(h mariadb.SiteHome, rel string, dump func(io.Writer) error) (err error) {
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

	if err := dump(out); err != nil {
		return err
	}

	if zw != nil {
		if err := zw.Close(); err != nil {
			return fmt.Errorf("dbjob: finish gzip: %w", err)
		}
	}

	return f.Sync()
}

// jobDir makes a new directory that only the site user can read, with the
// CA in it if there is one: the client runs as that user and opens the
// files by path.
func jobDir(tempDir, pemData string, uid, gid uint32) (string, func(), error) {
	dir, err := os.MkdirTemp(tempDir, "hostashi-dbjob-")
	if err != nil {
		return "", nil, fmt.Errorf("dbjob: CA directory: %w", err)
	}

	cleanup := func() { _ = os.RemoveAll(dir) }

	paths := []string{dir}

	if pemData != "" {
		if err := os.WriteFile(filepath.Join(dir, "ca.pem"), []byte(pemData), 0o600); err != nil {
			cleanup()

			return "", nil, fmt.Errorf("dbjob: CA file: %w", err)
		}

		paths = []string{filepath.Join(dir, "ca.pem"), dir}
	}

	for _, p := range paths {
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
	case errors.Is(err, pgctl.ErrFailed):
		return strings.TrimPrefix(err.Error(), "pgctl: failed: ")
	case errors.Is(err, ErrNoPGClient):
		return "no PostgreSQL client is installed"
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
