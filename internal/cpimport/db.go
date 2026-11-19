// SPDX-License-Identifier: AGPL-3.0-or-later

package cpimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/mariadb"
	"github.com/maki-it/hostashi/internal/mariadbctl"
	"github.com/maki-it/hostashi/internal/sites"
)

// ExportFunc runs mariadb-dump (mariadbctl.Export in production).
type ExportFunc func(ctx context.Context, r mariadbctl.Run, out io.Writer) error

// remoteDB is where the tunnel leads on the old server; the key there
// permits no other target (ADR-0026 Part 2).
const remoteDB = "127.0.0.1:3306"

type dumpConfig struct {
	Home      sites.Home
	Databases []agentapi.CloudPanelDBMap
	Password  string
	// Dial opens a channel to the old server (ssh.Client.Dial).
	Dial   func(network, addr string) (net.Conn, error)
	Export ExportFunc
	// TempDir holds the tunnel's socket; empty is os.TempDir, which the
	// unit makes private (PrivateTmp=).
	TempDir string
}

// dumpDatabases dumps each database of the old server into its own new
// file below <home>/cloudpanel-import-db. mariadb-dump runs as the site
// user and reaches the old server through a Unix socket that only the
// site user can open, forwarded over the SSH session. It returns the
// written files, relative to the home.
func dumpDatabases(ctx context.Context, c dumpConfig) ([]string, error) {
	h := mariadb.SiteHome{Home: c.Home.Path, UID: c.Home.UID, GID: c.Home.GID}

	if err := dumpDir(h); err != nil {
		return nil, err
	}

	sock, stop, err := tunnel(ctx, c)
	if err != nil {
		return nil, err
	}
	defer stop()

	stamp := time.Now().UTC().Format("20060102-150405")
	out := make([]string, 0, len(c.Databases))

	for _, d := range c.Databases {
		rel := path.Join(agentapi.CloudPanelImportDBDir, d.To+"-"+stamp+".sql")

		if err := dumpOne(ctx, c, h, sock, d.From, rel); err != nil {
			return out, fmt.Errorf("database %s: %w", d.From, err)
		}

		out = append(out, rel)
	}

	return out, nil
}

func dumpOne(ctx context.Context, c dumpConfig, h mariadb.SiteHome, sock, db, rel string) (err error) {
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

	run := mariadbctl.Run{
		Database: db, UID: c.Home.UID, GID: c.Home.GID,
		User: agentapi.CloudPanelDBUser, Password: c.Password, Socket: sock,
	}

	if err := c.Export(ctx, run, f); err != nil {
		return err
	}

	return f.Sync()
}

// dumpDir creates <home>/cloudpanel-import-db for the site user, without
// following symlinks. An existing directory must be the site user's.
func dumpDir(h mariadb.SiteHome) error {
	how := &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	}

	hfd, err := unix.Openat2(unix.AT_FDCWD, h.Home, how)
	if err != nil {
		return fmt.Errorf("open home %s: %w", h.Home, err)
	}
	defer unix.Close(hfd)

	var st unix.Stat_t
	if err := unix.Fstat(hfd, &st); err != nil || st.Uid != h.UID {
		return fmt.Errorf("home %s is not the site user's", h.Home)
	}

	if err := unix.Mkdirat(hfd, agentapi.CloudPanelImportDBDir, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("create %s: %w", agentapi.CloudPanelImportDBDir, err)
	}

	how.Resolve |= unix.RESOLVE_BENEATH

	fd, err := unix.Openat2(hfd, agentapi.CloudPanelImportDBDir, how)
	if err != nil {
		return fmt.Errorf("open %s: %w", agentapi.CloudPanelImportDBDir, err)
	}
	defer unix.Close(fd)

	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}

	switch st.Uid {
	case h.UID:
		return nil
	case owner():
		// Just made by this job: hand it over through the open fd.
		return unix.Fchown(fd, int(h.UID), int(h.GID))
	default:
		return fmt.Errorf("%s is not the site user's", agentapi.CloudPanelImportDBDir)
	}
}

// tunnel listens on a Unix socket in a new directory that only the site
// user can enter, and forwards every connection to MariaDB on the old
// server. stop closes the listener, waits for the connections and removes
// the directory.
func tunnel(ctx context.Context, c dumpConfig) (string, func(), error) {
	dir, err := os.MkdirTemp(c.TempDir, "hostashi-import-")
	if err != nil {
		return "", nil, fmt.Errorf("tunnel: %w", err)
	}

	if err := os.Chown(dir, int(c.Home.UID), int(c.Home.GID)); err != nil {
		_ = os.RemoveAll(dir)

		return "", nil, fmt.Errorf("tunnel: %w", err)
	}

	sock := filepath.Join(dir, "mysqld.sock")

	l, err := (&net.ListenConfig{}).Listen(ctx, "unix", sock)
	if err != nil {
		_ = os.RemoveAll(dir)

		return "", nil, fmt.Errorf("tunnel: %w", err)
	}

	if err := os.Lchown(sock, int(c.Home.UID), int(c.Home.GID)); err != nil {
		_ = l.Close()
		_ = os.RemoveAll(dir)

		return "", nil, fmt.Errorf("tunnel: %w", err)
	}

	var wg sync.WaitGroup

	wg.Go(func() {
		for {
			local, err := l.Accept()
			if err != nil {
				return
			}

			wg.Go(func() { forward(local, c.Dial) })
		}
	})

	stop := func() {
		_ = l.Close()

		wg.Wait()

		_ = os.RemoveAll(dir)
	}

	return sock, stop, nil
}

func forward(local net.Conn, dial func(network, addr string) (net.Conn, error)) {
	defer local.Close()

	remote, err := dial("tcp", remoteDB)
	if err != nil {
		return
	}
	defer remote.Close()

	done := make(chan struct{}, 2)

	go func() {
		_, _ = io.Copy(remote, local)

		if cw, ok := remote.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}

		done <- struct{}{}
	}()

	go func() {
		_, _ = io.Copy(local, remote)

		if cw, ok := local.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}

		done <- struct{}{}
	}()

	<-done
	<-done
}
