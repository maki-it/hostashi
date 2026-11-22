// SPDX-License-Identifier: AGPL-3.0-or-later

package firewall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/statefile"
)

// AppsFile, in Dir, fills the set app_owners of the base file: the loopback
// port of each app and the uid of its site user (ADR-0054). The base file's
// chain app_ports lets only nginx, root and that uid connect to the port.
const AppsFile = "apps.nft"

// AppOwner is the port of one app and its site user's uid.
type AppOwner struct {
	Port int
	UID  uint32
}

// RenderApps returns the apps file for owners, sorted by port.
func RenderApps(owners []AppOwner) (string, error) {
	o := slices.Clone(owners)
	slices.SortFunc(o, func(a, b AppOwner) int { return a.Port - b.Port })

	var b strings.Builder

	b.WriteString("# Written by the Hostashi agent: the apps of Node.js sites (ADR-0054).\n")

	if len(o) == 0 {
		return b.String(), nil
	}

	elems := make([]string, 0, len(o))

	for i, a := range o {
		if err := agentapi.ValidateAppPort(a.Port); err != nil {
			return "", fmt.Errorf("firewall: %w", err)
		}

		if i > 0 && o[i-1].Port == a.Port {
			return "", fmt.Errorf("firewall: app port %d listed twice", a.Port)
		}

		if a.UID < 1000 {
			return "", fmt.Errorf("firewall: app port %d: uid %d is not a site user", a.Port, a.UID)
		}

		elems = append(elems, strconv.Itoa(a.Port)+" . "+strconv.FormatUint(uint64(a.UID), 10))
	}

	b.WriteString("add element inet hostashi app_owners { " + strings.Join(elems, ", ") + " }\n")

	return b.String(), nil
}

// SetApps writes the apps file and, when it changed and the firewall is
// on, reloads the firewall. nft loads the files in one transaction, so a
// file that does not load leaves the old rules and SetApps fails. With
// the firewall off (installed with --no-firewall) the file is written and
// ErrDisabled returned: nothing filters the app ports then.
func (m *Manager) SetApps(ctx context.Context, owners []AppOwner) error {
	content, err := RenderApps(owners)
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	old, err := os.ReadFile(filepath.Join(m.Dir, AppsFile))
	changed := err != nil || !bytes.Equal(old, []byte(content))

	if changed {
		if err := statefile.Write(m.Dir, AppsFile, []byte(content)); err != nil {
			return err
		}
	}

	if !m.Enabled() {
		return ErrDisabled
	}

	if !changed {
		return nil
	}

	if err := m.Reload(ctx); err != nil {
		if old != nil {
			// The old file loaded before; put it back for the next reload.
			err = errors.Join(err, statefile.Write(m.Dir, AppsFile, old))
		}

		return fmt.Errorf("firewall: apps: %w", err)
	}

	return nil
}
