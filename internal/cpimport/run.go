// SPDX-License-Identifier: AGPL-3.0-or-later

package cpimport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/transfer"
	"github.com/maki-it/hostashi/internal/cpsource"
	"github.com/maki-it/hostashi/internal/sites"
)

// RunConfig is what the job needs. The agent's paths are fixed in the
// unit; tests point them elsewhere.
type RunConfig struct {
	Dir      string // import records
	StateDir string // the site registry
	Source   *cpsource.Source
	Log      *slog.Logger
	// Limits bound the copy; zero takes the transfer defaults.
	Limits transfer.Limits
}

// ErrFailed is returned after a failed import was recorded.
var ErrFailed = errors.New("cpimport: the import failed, see its record")

// RunOnce is hostashi cloudpanel import-run ID, one import as root,
// started by the agent in hostashi-import@ID.service. It writes the
// outcome to the import's record whatever happens after the lock.
func RunOnce(ctx context.Context, cfg RunConfig, id string) error {
	if os.Geteuid() != 0 {
		return errors.New("cpimport: must run as root")
	}

	if !agentapi.ValidImportID(id) {
		return fmt.Errorf("%w: import id", ErrInvalid)
	}

	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}

	if err := ensureDir(cfg.Dir); err != nil {
		return err
	}

	unlock, err := lock(filepath.Join(cfg.Dir, lockFile))
	if err != nil {
		return err
	}
	defer unlock()

	r, err := readRecord(cfg.Dir, id, 0)
	if err != nil {
		return err
	}

	if r.State != agentapi.ImportQueued {
		return fmt.Errorf("cpimport: import %s is %s, not queued", id, r.State)
	}

	r.State, r.Started = agentapi.ImportRunning, time.Now().UTC()
	if err := writeRecord(cfg.Dir, r); err != nil {
		return err
	}

	// The record is written through this fd from now on: after the drop
	// the job can no longer replace files in the root-owned directory.
	rec, err := openRecord(cfg.Dir, id)
	if err != nil {
		return err
	}
	defer rec.Close()

	st, err := run(ctx, cfg, r)

	r.Files, r.Bytes, r.Unchanged, r.Deleted, r.Skipped = st.Files, st.Bytes, st.Unchanged, st.Deleted, st.Skipped
	r.SkippedPaths = st.SkippedPaths
	r.Finished, r.State = time.Now().UTC(), agentapi.ImportDone

	if err != nil {
		r.State, r.Error = agentapi.ImportFailed, err.Error()
		cfg.Log.ErrorContext(ctx, "import failed", "id", id, "site", r.Site, "err", err)
	}

	if werr := rewrite(rec, r); werr != nil {
		return fmt.Errorf("write import record: %w", werr)
	}

	if err != nil {
		return ErrFailed
	}

	return nil
}

func run(ctx context.Context, cfg RunConfig, r Import) (transfer.Stats, error) {
	home, err := siteHome(cfg.StateDir, r.Site, 0)
	if err != nil {
		return transfer.Stats{}, err
	}

	src, sshCfg, err := cfg.Source.ClientConfig()
	if err != nil {
		return transfer.Stats{}, fmt.Errorf("migration source: %w", err)
	}

	d := net.Dialer{Timeout: sshCfg.Timeout}

	nc, err := d.DialContext(ctx, "tcp", src.Addr())
	if err != nil {
		return transfer.Stats{}, fmt.Errorf("connect to %s: %w", src.Addr(), err)
	}

	c, chans, reqs, err := ssh.NewClientConn(nc, src.Addr(), sshCfg)
	if err != nil {
		_ = nc.Close()

		return transfer.Stats{}, fmt.Errorf("ssh to %s: %w", src.Addr(), err)
	}

	conn := ssh.NewClient(c, chans, reqs)
	defer conn.Close()

	// Canceling the job closes the connection, which ends every read.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	client, err := sftp.NewClient(conn)
	if err != nil {
		return transfer.Stats{}, fmt.Errorf("sftp: %w", err)
	}
	defer client.Close()

	// Everything that needs root is done: the key is read and the session
	// is open. From here on the job is the site user.
	if err := Drop(int(home.UID), int(home.GID)); err != nil {
		return transfer.Stats{}, err
	}

	dst, err := openTarget(home)
	if err != nil {
		return transfer.Stats{}, err
	}
	defer unix.Close(dst)

	return transfer.Copy(ctx, transfer.SFTP(client), r.DocRoot, dst, cfg.Limits)
}

// Drop makes the whole process uid:gid with no supplementary groups. Go
// applies these calls to every thread on Linux. A process that started as
// root keeps no capabilities after setresuid to another user (no
// SECBIT_KEEP_CAPS, and the unit gives no ambient capabilities).
func Drop(uid, gid int) error {
	if uid <= 0 || gid <= 0 {
		return errors.New("cpimport: refusing to run as root")
	}

	if err := syscall.Setgroups([]int{}); err != nil {
		return fmt.Errorf("cpimport: setgroups: %w", err)
	}

	if err := syscall.Setresgid(gid, gid, gid); err != nil {
		return fmt.Errorf("cpimport: setresgid: %w", err)
	}

	if err := syscall.Setresuid(uid, uid, uid); err != nil {
		return fmt.Errorf("cpimport: setresuid: %w", err)
	}

	ru, eu, su := unix.Getresuid()
	rg, eg, sg := unix.Getresgid()

	groups, err := unix.Getgroups()
	if err != nil || ru != uid || eu != uid || su != uid || rg != gid || eg != gid || sg != gid || len(groups) != 0 {
		return errors.New("cpimport: the process did not drop to the site user")
	}

	// Going back must fail.
	if err := syscall.Setresuid(0, 0, 0); err == nil {
		return errors.New("cpimport: could become root again")
	}

	return nil
}

// openTarget opens <home>/cloudpanel-import as the site user, creating it,
// without following any symlink. The home must be the site user's.
func openTarget(home sites.Home) (int, error) {
	how := &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	}

	hfd, err := unix.Openat2(unix.AT_FDCWD, home.Path, how)
	if err != nil {
		return -1, fmt.Errorf("open home %s: %w", home.Path, err)
	}
	defer unix.Close(hfd)

	var st unix.Stat_t
	if err := unix.Fstat(hfd, &st); err != nil || st.Uid != home.UID {
		return -1, fmt.Errorf("home %s is not the site user's", home.Path)
	}

	if err := unix.Mkdirat(hfd, agentapi.CloudPanelImportDir, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return -1, fmt.Errorf("create %s: %w", agentapi.CloudPanelImportDir, err)
	}

	how.Resolve |= unix.RESOLVE_BENEATH

	fd, err := unix.Openat2(hfd, agentapi.CloudPanelImportDir, how)
	if err != nil {
		return -1, fmt.Errorf("open %s: %w", agentapi.CloudPanelImportDir, err)
	}

	if err := unix.Fstat(fd, &st); err != nil || st.Uid != home.UID {
		_ = unix.Close(fd)

		return -1, fmt.Errorf("%s is not the site user's", agentapi.CloudPanelImportDir)
	}

	return fd, nil
}

func openRecord(dir, id string) (*os.File, error) {
	fd, err := unix.Openat2(unix.AT_FDCWD, filepath.Join(dir, importFile(id)), &unix.OpenHow{
		Flags:   unix.O_WRONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, fmt.Errorf("cpimport: open record: %w", err)
	}

	return os.NewFile(uintptr(fd), importFile(id)), nil
}

// rewrite replaces the record's content in place. A crash in between
// leaves a damaged record, which Get reports and Start no longer counts as
// running.
func rewrite(f *os.File, r Import) error {
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}

	if err := f.Truncate(0); err != nil {
		return err
	}

	if _, err := f.WriteAt(append(b, '\n'), 0); err != nil {
		return err
	}

	return f.Sync()
}
