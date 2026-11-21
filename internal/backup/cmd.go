// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/extdb"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/statefile"
)

// RunConfig is the configuration of one run of hostashi backup run.
type RunConfig struct {
	ConfigDir string // default ConfigDir
	StatusDir string // default StatusDir
	StateDir  string // the agent's state directory with the registries
	CacheDir  string // default CacheDir
	// PanelDir holds the panel's list of external databases, owned by
	// PanelUser; default PanelBackupDir. KeyFile opens their passwords;
	// default PanelKeyFile.
	PanelDir string
	KeyFile  string
	// PanelUID returns the uid that must own PanelDir; default: the uid
	// of PanelUser.
	PanelUID func() (uint32, error)
	Host     string // default: the lowercase host name
	Log      *slog.Logger
}

// ErrBusy means another run holds the lock.
var ErrBusy = errors.New("backup: another run is in progress")

// ErrFailed means the run finished, but not everything succeeded; the
// details are in the status file.
var ErrFailed = errors.New("backup: run finished with errors")

// RunOnce is hostashi backup run: one complete run as root, normally
// started by hostashi-backup.service. It writes the status file whatever
// happens after the lock is taken.
func RunOnce(ctx context.Context, cfg RunConfig) error {
	if os.Geteuid() != 0 {
		return errors.New("backup: must run as root")
	}

	cfg = cfg.withDefaults()

	if err := statefile.CheckDir(cfg.StatusDir, 0); err != nil {
		return fmt.Errorf("status directory: %w", err)
	}

	unlock, err := lock(filepath.Join(cfg.StatusDir, runLock))
	if err != nil {
		return err
	}
	defer unlock()

	prev, err := ReadStatus(cfg.StatusDir, 0)
	if err != nil {
		cfg.Log.WarnContext(ctx, "previous status unreadable, starting fresh", "err", err)

		prev = Status{}
	}

	st := run(ctx, cfg, prev)

	if st.Snapshots != nil {
		if err := WriteSnapshots(cfg.StatusDir, st.Snapshots); err != nil {
			cfg.Log.ErrorContext(ctx, "write snapshot list", "err", err)
		}
	}

	if err := WriteStatus(cfg.StatusDir, st); err != nil {
		return fmt.Errorf("write status: %w", err)
	}

	if !st.OK {
		return ErrFailed
	}

	return nil
}

func run(ctx context.Context, cfg RunConfig, prev Status) Status {
	fail := func(err error) Status {
		st := newStatus(prev, time.Now())
		st.Error, st.Finished = err.Error(), time.Now().UTC()

		return st
	}

	settings, err := ReadSettings(cfg.ConfigDir, 0)
	if err != nil {
		return fail(fmt.Errorf("settings: %w", err))
	}

	sites, err := Sites(cfg.StateDir, 0)
	if err != nil {
		return fail(err)
	}

	r := &Runner{Settings: settings, Prev: prev, Log: cfg.Log}

	// External databases (ADR-0041). A problem here does not stop the
	// local part: the run saves everything else and reports it.
	if sites, err = external(cfg, sites); err != nil {
		cfg.Log.ErrorContext(ctx, "external databases", "err", err)

		r.Warning = err.Error()
	}

	if settings.Target != nil {
		repo, err := NewRepo(settings, cfg.ConfigDir, cfg.CacheDir, cfg.Host)
		if err != nil {
			return fail(err)
		}

		repo.Log = cfg.Log
		r.Repo = repo
	}

	return r.Run(ctx, Plan(sites, PanelPaths))
}

// external adds the databases on external servers from the panel's list.
func external(cfg RunConfig, sites []Site) ([]Site, error) {
	uid, err := cfg.PanelUID()
	if err != nil {
		return sites, err
	}

	m, err := extdb.Read(cfg.PanelDir, uid)
	if err != nil {
		return sites, fmt.Errorf("read the list of external databases: %w", err)
	}

	if len(m.Databases) == 0 {
		return sites, nil
	}

	box, err := secrets.Load(cfg.KeyFile)
	if err != nil {
		return sites, fmt.Errorf("external databases not saved: %w", err)
	}

	return AddExternal(sites, m, box)
}

func (c RunConfig) withDefaults() RunConfig {
	if c.ConfigDir == "" {
		c.ConfigDir = ConfigDir
	}

	if c.StatusDir == "" {
		c.StatusDir = StatusDir
	}

	if c.CacheDir == "" {
		c.CacheDir = CacheDir
	}

	if c.Host == "" {
		h, _ := os.Hostname()
		c.Host = strings.ToLower(h)
	}

	if c.Log == nil {
		c.Log = slog.New(slog.DiscardHandler)
	}

	if c.PanelDir == "" {
		c.PanelDir = PanelBackupDir
	}

	if c.KeyFile == "" {
		c.KeyFile = PanelKeyFile
	}

	if c.PanelUID == nil {
		c.PanelUID = func() (uint32, error) { return panelUID(PanelUser) }
	}

	return c
}

// lock takes an exclusive, non-blocking lock on path.
func lock(path string) (func(), error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("backup: lock file: %w", err)
	}

	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)

		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrBusy
		}

		return nil, fmt.Errorf("backup: lock: %w", err)
	}

	return func() { _ = unix.Close(fd) }, nil
}
