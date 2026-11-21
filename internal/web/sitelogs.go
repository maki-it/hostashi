// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// siteLogMode keeps the logs away from the site users: only root writes
// and reads them; the panel reads them through the agent (ADR-0053).
const siteLogMode = 0o750

// PrepareLogs creates the log directory of every site in users below
// SiteLogDir. nginx opens the log files when it tests, starts or reloads a
// configuration and refuses the whole configuration if a directory is
// missing, so this runs before each of them. An existing entry that is not a
// plain directory is an error, never followed.
func (n *Nginx) PrepareLogs(users map[string]bool) error {
	if n.SiteLogDir == "" {
		return nil
	}

	// The server-wide logs may sit in a directory of their own.
	for _, p := range []string{n.ErrorLog, n.AccessLog} {
		if p != "" {
			if err := os.MkdirAll(filepath.Dir(p), siteLogMode); err != nil {
				return fmt.Errorf("web: server log directory: %w", err)
			}
		}
	}

	if err := ensureLogDir(n.SiteLogDir); err != nil {
		return err
	}

	for _, u := range sortedUsers(users) {
		if err := agentapi.ValidateSiteUser(u); err != nil {
			return fmt.Errorf("web: site log directory: %w", err)
		}

		if err := ensureLogDir(filepath.Join(n.SiteLogDir, u)); err != nil {
			return err
		}
	}

	return nil
}

// PruneLogs removes the log directories of sites that are not in users,
// that is of deleted sites. Entries that are not directories or have no
// site name are left alone.
func (n *Nginx) PruneLogs(users map[string]bool) error {
	if n.SiteLogDir == "" {
		return nil
	}

	entries, err := os.ReadDir(n.SiteLogDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("web: site log directory: %w", err)
	}

	var errs []error

	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || users[name] || agentapi.ValidateSiteUser(name) != nil {
			continue
		}

		// RemoveAll does not follow symbolic links inside the directory.
		if err := os.RemoveAll(filepath.Join(n.SiteLogDir, name)); err != nil {
			errs = append(errs, fmt.Errorf("web: remove logs of %s: %w", name, err))
		}
	}

	return errors.Join(errs...)
}

func ensureLogDir(p string) error {
	fi, err := os.Lstat(p)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(p, siteLogMode); err != nil && !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("web: create log directory: %w", err)
		}

		fi, err = os.Lstat(p)
	}

	if err != nil {
		return fmt.Errorf("web: log directory: %w", err)
	}

	if !fi.IsDir() {
		return fmt.Errorf("web: log directory %s is not a directory", p)
	}

	// The umask may have cut the mode; an older directory may be wider.
	if fi.Mode().Perm() != siteLogMode {
		if err := os.Chmod(p, siteLogMode); err != nil {
			return fmt.Errorf("web: log directory mode: %w", err)
		}
	}

	return nil
}

func sortedUsers(users map[string]bool) []string {
	out := make([]string, 0, len(users))
	for u, ok := range users {
		if ok {
			out = append(out, u)
		}
	}

	sort.Strings(out)

	return out
}
