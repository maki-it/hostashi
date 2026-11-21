// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/statefile"
)

// Restore is one restore as the agent and the restore unit see it.
type Restore = agentapi.BackupRestore

// Errors of restores.
var (
	ErrSnapshotNotFound = errors.New("backup: no such snapshot of this site")
	ErrRestoreBusy      = errors.New("backup: a restore is in progress")
	ErrRestoreNotFound  = errors.New("backup: no such restore")
	// ErrNoSpace means the snapshot does not fit into what is left under
	// the site's disk limits (ADR-0015). The restore runs as root, which
	// the quota does not stop, so it is checked before.
	ErrNoSpace = errors.New("backup: the snapshot does not fit under the site's disk limits")
)

// Space is what a site may still write under its disk limits.
type Space struct {
	Limited                 bool   // false: no limits, or no project quota
	FreeBytes               uint64 // under the byte limit; 0 when that is unlimited
	FreeInodes              uint64 // under the file limit; 0 when that is unlimited
	BytesLimit, InodesLimit uint64
}

// SpaceFunc reports the Space of a site.
type SpaceFunc func(site string) (Space, error)

// fits reports whether a snapshot of bytes and files fits into sp, and if
// not, why.
func fits(sp Space, bytes, files int64) (bool, string) {
	if !sp.Limited {
		return true, ""
	}

	if sp.BytesLimit > 0 && bytes > 0 && uint64(bytes) > sp.FreeBytes {
		return false, fmt.Sprintf("the snapshot holds %d bytes, the site has %d bytes left under its limit", bytes, sp.FreeBytes)
	}

	if sp.InodesLimit > 0 && files > 0 && uint64(files) > sp.FreeInodes {
		return false, fmt.Sprintf("the snapshot holds %d files, the site has %d files left under its limit", files, sp.FreeInodes)
	}

	return true, ""
}

// RestoreDirPrefix starts the name of every restore directory in a home.
const RestoreDirPrefix = "restore-"

// keepRestores is how many finished restore records the agent keeps.
const keepRestores = 50

var (
	restoreFileRe = regexp.MustCompile(`^restore-([0-9a-f]{16})\.json$`)
	// The home lands in a systemd drop-in line; only plain paths qualify.
	unitPathRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]{1,1000}$`)
)

func restoreFile(id string) string { return "restore-" + id + ".json" }

func (a *Agent) stateDir() string {
	if a.StateDir == "" {
		return AgentStateDir
	}

	return a.StateDir
}

func (a *Agent) unitDir() string {
	if a.UnitDir == "" {
		return UnitDir
	}

	return a.UnitDir
}

// restoreDropIn is the drop-in that opens the site's home, and nothing
// else, for writing to one restore's unit.
func (a *Agent) restoreDropIn(id string) string {
	return filepath.Join(a.unitDir(), "hostashi-restore@"+id+".service.d", "home.conf")
}

// StartRestore checks the request against the snapshot list of the last
// run, records the restore and starts its unit. Only one restore runs at a
// time.
func (a *Agent) StartRestore(ctx context.Context, site, snapshot string) (Restore, error) {
	if err := (agentapi.BackupRestoreRequest{Site: site, Snapshot: snapshot}).Validate(); err != nil {
		return Restore{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	info, err := a.info()
	if err != nil {
		return Restore{}, err
	}

	if !info.Ready {
		return Restore{}, ErrNotReady
	}

	snaps, err := ReadSnapshots(a.statusDir(), owner())
	if err != nil {
		return Restore{}, err
	}

	var snap *SnapshotEntry

	for i, s := range snaps {
		if s.ID == snapshot && s.Site == site && (s.Kind == KindFiles || s.Kind == KindDB) {
			snap = &snaps[i]
		}
	}

	if snap == nil {
		return Restore{}, ErrSnapshotNotFound
	}

	home, err := a.siteHome(site)
	if err != nil {
		return Restore{}, err
	}

	if a.Space != nil {
		sp, err := a.Space(site)
		if err != nil {
			return Restore{}, fmt.Errorf("backup: space of site %s: %w", site, err)
		}

		if ok, why := fits(sp, snap.Bytes, snap.Files); !ok {
			return Restore{}, fmt.Errorf("%w: %s", ErrNoSpace, why)
		}
	}

	all, err := a.restores()
	if err != nil {
		return Restore{}, err
	}

	if held(filepath.Join(a.statusDir(), restoreLock)) {
		return Restore{}, ErrRestoreBusy
	}

	for _, r := range all {
		// A queued restore whose unit never took the lock is stale after
		// a minute; anything newer still counts as starting.
		if r.State == agentapi.RestoreRunning || (r.State == agentapi.RestoreQueued && time.Since(r.Created) < time.Minute) {
			return Restore{}, ErrRestoreBusy
		}
	}

	a.pruneRestores(all)

	id := make([]byte, 8)
	_, _ = rand.Read(id)

	now := time.Now().UTC()
	r := Restore{
		ID: hex.EncodeToString(id), Site: site, Snapshot: snapshot, Kind: snap.Kind, Database: snap.Database,
		Dir: RestoreDirPrefix + now.Format("20060102-150405"), State: agentapi.RestoreQueued, Created: now,
	}

	if err := writeJSON(a.statusDir(), restoreFile(r.ID), r); err != nil {
		return Restore{}, err
	}

	if err := writeUnitFile(a.restoreDropIn(r.ID), restoreDropIn(home)); err != nil {
		return Restore{}, err
	}

	if err := a.Units.Reload(ctx); err != nil {
		return Restore{}, err
	}

	if err := a.Units.StartRestore(ctx, r.ID); err != nil {
		return Restore{}, err
	}

	return r, nil
}

func restoreDropIn(home string) string {
	return "# Written by the Hostashi agent for one restore.\n[Unit]\nRequiresMountsFor=" + home + "\n[Service]\nReadWritePaths=" + home + "\n"
}

// GetRestore returns one restore.
func (a *Agent) GetRestore(id string) (Restore, error) {
	if err := (agentapi.BackupRestoreGetRequest{ID: id}).Validate(); err != nil {
		return Restore{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return readRestore(a.statusDir(), id)
}

func (a *Agent) siteHome(site string) (string, error) {
	all, err := Sites(a.stateDir(), owner())
	if err != nil {
		return "", err
	}

	for _, s := range all {
		if s.Name != site {
			continue
		}

		if !unitPathRe.MatchString(s.Home) || strings.Contains(s.Home, "..") {
			return "", fmt.Errorf("backup: home %q cannot be restored into", s.Home)
		}

		return s.Home, nil
	}

	return "", ErrSnapshotNotFound
}

// restores reads every restore record, newest first.
func (a *Agent) restores() ([]Restore, error) {
	entries, err := os.ReadDir(a.statusDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("backup: status directory: %w", err)
	}

	var out []Restore

	for _, e := range entries {
		m := restoreFileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}

		r, err := readRestore(a.statusDir(), m[1])
		if err != nil {
			return nil, err
		}

		out = append(out, r)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })

	return out, nil
}

// pruneRestores removes the drop-ins of all earlier restores and the
// records beyond keepRestores. Errors only leave files behind.
func (a *Agent) pruneRestores(all []Restore) {
	for i, r := range all {
		_ = os.RemoveAll(filepath.Dir(a.restoreDropIn(r.ID)))

		if i >= keepRestores-1 {
			_ = os.Remove(filepath.Join(a.statusDir(), restoreFile(r.ID)))
		}
	}
}

func readRestore(dir, id string) (Restore, error) {
	raw, err := statefile.Read(dir, restoreFile(id), owner(), maxFileSize)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return Restore{}, ErrRestoreNotFound
	case err != nil:
		return Restore{}, err
	}

	var r Restore
	if err := decodeStrict(raw, &r); err != nil || r.ID != id {
		return Restore{}, fmt.Errorf("backup: restore record %s is damaged", id)
	}

	return r, nil
}

// writeUnitFile writes a world-readable systemd file atomically.
func writeUnitFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("backup: unit drop-in: %w", err)
	}

	tmp := path + ".tmp"
	_ = os.Remove(tmp)

	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil { //nolint:gosec // systemd files are world-readable like the packaged ones
		return fmt.Errorf("backup: unit drop-in: %w", err)
	}

	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("backup: unit drop-in: %w", err)
	}

	return nil
}
