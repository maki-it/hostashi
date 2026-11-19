// SPDX-License-Identifier: AGPL-3.0-or-later

package backup

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/maki-it/hostashi/internal/mariadb"
	"github.com/maki-it/hostashi/internal/resticctl"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/statefile"
)

const maxFileSize = 1 << 20

// ReadSettings reads dir/settings.json. A missing directory or file means
// the defaults without a target.
func ReadSettings(dir string, ownerUID uint32) (Settings, error) {
	if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
		return DefaultSettings(), nil
	}

	raw, err := statefile.Read(dir, SettingsFile, ownerUID, maxFileSize)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return DefaultSettings(), nil
	case err != nil:
		return Settings{}, err
	}

	var s Settings
	if err := decodeStrict(raw, &s); err != nil {
		return Settings{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := s.Validate(); err != nil {
		return Settings{}, err
	}

	return s, nil
}

// WriteSettings validates s and writes it atomically.
func WriteSettings(dir string, s Settings) error {
	if err := s.Validate(); err != nil {
		return err
	}

	return writeJSON(dir, SettingsFile, s)
}

// ReadStatus reads dir/status.json. A missing file is the zero status.
func ReadStatus(dir string, ownerUID uint32) (Status, error) {
	raw, err := statefile.Read(dir, StatusFile, ownerUID, 8*maxFileSize)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return Status{}, nil
	case err != nil:
		return Status{}, err
	}

	var s Status
	if err := decodeStrict(raw, &s); err != nil {
		return Status{}, fmt.Errorf("backup: status file: %w", err)
	}

	if s.Version != StatusVersion {
		return Status{}, fmt.Errorf("backup: status file version %d", s.Version)
	}

	return s, nil
}

// ReadSnapshots reads the snapshot list of the last run. A missing file is
// an empty list.
func ReadSnapshots(dir string, ownerUID uint32) ([]SnapshotEntry, error) {
	raw, err := statefile.Read(dir, SnapshotsFile, ownerUID, 32*maxFileSize)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return []SnapshotEntry{}, nil
	case err != nil:
		return nil, err
	}

	var s []SnapshotEntry
	if err := decodeStrict(raw, &s); err != nil {
		return nil, fmt.Errorf("backup: snapshots file: %w", err)
	}

	return s, nil
}

// WriteSnapshots writes the snapshot list atomically.
func WriteSnapshots(dir string, s []SnapshotEntry) error {
	return writeJSON(dir, SnapshotsFile, s)
}

// WriteStatus writes the status atomically.
func WriteStatus(dir string, s Status) error {
	return writeJSON(dir, StatusFile, s)
}

func writeJSON(dir, name string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("backup: encode %s: %w", name, err)
	}

	return statefile.Write(dir, name, append(raw, '\n'))
}

func decodeStrict(raw []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil || dec.More() {
		return errors.New("not a single valid document")
	}

	return nil
}

// NewRepo builds the restic repository of the settings' target, with the
// secrets in configDir. host names this server in the snapshots.
func NewRepo(s Settings, configDir, cacheDir, host string) (*resticctl.Repo, error) {
	if s.Target == nil {
		return nil, errors.New("backup: no target is set up")
	}

	if err := s.Validate(); err != nil {
		return nil, err
	}

	return &resticctl.Repo{
		Repository:   s.Target.Repository(),
		PasswordFile: filepath.Join(configDir, PasswordFile),
		SSHKey:       filepath.Join(configDir, SSHKeyFile),
		KnownHosts:   filepath.Join(configDir, KnownHostsFile),
		CacheDir:     cacheDir,
		Host:         host,
	}, nil
}

// Sites reads the agent's site and database registries in stateDir
// without writing them.
func Sites(stateDir string, ownerUID uint32) ([]Site, error) {
	homes, err := sites.ReadHomes(stateDir, ownerUID)
	if err != nil {
		return nil, fmt.Errorf("site registry: %w", err)
	}

	dbs, err := mariadb.ReadDatabases(stateDir, ownerUID)
	if err != nil {
		return nil, fmt.Errorf("database registry: %w", err)
	}

	bySite := map[string][]string{}

	for _, d := range dbs {
		if _, ok := homes[d.Site]; !ok {
			return nil, fmt.Errorf("database %q belongs to unknown site %q", d.Name, d.Site)
		}

		bySite[d.Site] = append(bySite[d.Site], d.Name)
	}

	out := make([]Site, 0, len(homes))
	for name, home := range homes {
		out = append(out, Site{Name: name, Home: home.Path, UID: home.UID, GID: home.GID, Databases: bySite[name]})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out, nil
}
