// SPDX-License-Identifier: AGPL-3.0-or-later

package scanunit

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// The quarantine of a site is <QuarantineDir>/<site>/, root 0700. Each
// file is <id>.bin (the content, root 0600) with <id>.json (where it was).
// The site user can neither read nor change it.

// item is the stored record of a quarantined file.
type item struct {
	Path   string      `json:"path"`
	SHA256 string      `json:"sha256"`
	Size   int64       `json:"size"`
	Mode   fs.FileMode `json:"mode"`
	When   time.Time   `json:"when"`
}

const maxItem = 16 << 10

func (m *Manager) siteQDir(site string) (string, error) {
	dir := filepath.Join(m.qDir(), site)

	for _, d := range []string{m.qDir(), dir} {
		if err := os.Mkdir(d, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
			return "", fmt.Errorf("scanunit: %w", err)
		}

		fi, err := os.Lstat(d)
		if err != nil {
			return "", fmt.Errorf("scanunit: %w", err)
		}

		if !fi.IsDir() || fi.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("scanunit: %s is not a private directory", d)
		}
	}

	return dir, nil
}

// Quarantine moves a flagged file of a site into the quarantine
// (site.scan.quarantine): it copies the file while hashing it, keeps the
// copy only if the hash matches the finding, then deletes the original.
func (m *Manager) Quarantine(ctx context.Context, req agentapi.ScanQuarantineRequest) (agentapi.QuarantineItem, error) {
	if err := req.Validate(); err != nil {
		return agentapi.QuarantineItem{}, err
	}

	acct, err := m.Site(req.Site)
	if err != nil {
		return agentapi.QuarantineItem{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dir, err := m.siteQDir(req.Site)
	if err != nil {
		return agentapi.QuarantineItem{}, err
	}

	root, err := os.OpenRoot(acct.Home)
	if err != nil {
		return agentapi.QuarantineItem{}, fmt.Errorf("scanunit: %w", err)
	}
	defer root.Close()

	// Gone, a link, out of the home or no longer a regular file: not what
	// the scan saw.
	src, fi, err := openRegular(root, req.Path)
	if err != nil {
		return agentapi.QuarantineItem{}, fmt.Errorf("%w: %w", ErrChanged, err)
	}
	defer src.Close()

	id := newID()
	bin := filepath.Join(dir, id+".bin")

	dst, err := os.OpenFile(bin, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return agentapi.QuarantineItem{}, fmt.Errorf("scanunit: %w", err)
	}

	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(dst, h), src)
	err = errors.Join(err, dst.Sync(), dst.Close())

	if err == nil && hex.EncodeToString(h.Sum(nil)) != req.SHA256 {
		err = ErrChanged
	}

	if err != nil {
		_ = os.Remove(bin)

		if errors.Is(err, ErrChanged) {
			return agentapi.QuarantineItem{}, err
		}

		return agentapi.QuarantineItem{}, fmt.Errorf("scanunit: %w", err)
	}

	rec := item{Path: req.Path, SHA256: req.SHA256, Size: fi.Size(), Mode: fi.Mode().Perm(), When: m.clock().UTC()}

	b, err := json.Marshal(rec)
	if err == nil {
		err = writePrivate(filepath.Join(dir, id+".json"), b)
	}

	if err != nil {
		_ = os.Remove(bin)

		return agentapi.QuarantineItem{}, err
	}

	if err := root.Remove(req.Path); err != nil {
		_ = os.Remove(bin)
		_ = os.Remove(filepath.Join(dir, id+".json"))

		return agentapi.QuarantineItem{}, fmt.Errorf("scanunit: %w", err)
	}

	m.log().InfoContext(ctx, "file quarantined", "site", req.Site, "path", req.Path, "id", id)

	return agentapi.QuarantineItem{ID: id, Path: req.Path, SHA256: req.SHA256, Size: fi.Size()}, nil
}

// Restore puts a quarantined file back (site.scan.restore), owned by the
// site user with its old mode (without set-id bits). It refuses when
// something is at the path or its directory is gone.
func (m *Manager) Restore(ctx context.Context, req agentapi.ScanRestoreRequest) (agentapi.ScanRestored, error) {
	if err := req.Validate(); err != nil {
		return agentapi.ScanRestored{}, err
	}

	acct, err := m.Site(req.Site)
	if err != nil {
		return agentapi.ScanRestored{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	dir, err := m.siteQDir(req.Site)
	if err != nil {
		return agentapi.ScanRestored{}, err
	}

	rec, err := readItem(filepath.Join(dir, req.ID+".json"))
	if err != nil {
		return agentapi.ScanRestored{}, err
	}

	root, err := os.OpenRoot(acct.Home)
	if err != nil {
		return agentapi.ScanRestored{}, fmt.Errorf("scanunit: %w", err)
	}
	defer root.Close()

	if parent := path.Dir(rec.Path); parent != "." {
		fi, err := root.Lstat(parent)
		if err != nil || !fi.IsDir() {
			return agentapi.ScanRestored{}, ErrNoParent
		}
	}

	src, err := os.Open(filepath.Join(dir, req.ID+".bin"))
	if err != nil {
		return agentapi.ScanRestored{}, ErrUnknownItem
	}
	defer src.Close()

	dst, err := root.OpenFile(rec.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|unix.O_NOFOLLOW, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return agentapi.ScanRestored{}, ErrExists
	}

	if err != nil {
		return agentapi.ScanRestored{}, fmt.Errorf("scanunit: %w", err)
	}

	_, err = io.Copy(dst, src)

	err = errors.Join(err, dst.Chown(int(acct.UID), int(acct.GID)), dst.Chmod(rec.Mode.Perm()&0o777), dst.Sync(), dst.Close())
	if err != nil {
		_ = root.Remove(rec.Path)

		return agentapi.ScanRestored{}, fmt.Errorf("scanunit: %w", err)
	}

	_ = os.Remove(filepath.Join(dir, req.ID+".bin"))
	_ = os.Remove(filepath.Join(dir, req.ID+".json"))

	m.log().InfoContext(ctx, "file restored from quarantine", "site", req.Site, "path", rec.Path, "id", req.ID)

	return agentapi.ScanRestored{Path: rec.Path}, nil
}

func readItem(name string) (item, error) {
	f, err := os.Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return item{}, ErrUnknownItem
	}

	if err != nil {
		return item{}, fmt.Errorf("scanunit: %w", err)
	}

	defer f.Close()

	var rec item
	if err := json.NewDecoder(io.LimitReader(f, maxItem)).Decode(&rec); err != nil {
		return item{}, fmt.Errorf("scanunit: quarantine record: %w", err)
	}

	if agentapi.ValidateScanPath(rec.Path) != nil {
		return item{}, errors.New("scanunit: quarantine record has an invalid path")
	}

	return rec, nil
}

func writePrivate(name string, data []byte) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("scanunit: %w", err)
	}

	_, err = f.Write(data)
	if err = errors.Join(err, f.Sync(), f.Close()); err != nil {
		_ = os.Remove(name)

		return fmt.Errorf("scanunit: %w", err)
	}

	return nil
}

func newID() string {
	var b [16]byte

	_, _ = rand.Read(b[:])

	return hex.EncodeToString(b[:])
}
