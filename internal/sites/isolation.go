// SPDX-License-Identifier: AGPL-3.0-or-later

package sites

import (
	"fmt"
	"strings"

	"golang.org/x/sys/unix"
)

// homesRoot is what ProtectHome= hides in the site units; pools below it
// need no extra line.
const homesRoot = "/home"

// HiddenPaths returns the roots of the pools that site units must hide
// themselves: ProtectHome= covers only /home, so without this the units of
// every site would see the homes on the other volumes (ADR-0033, spike S9).
func (m *Manager) HiddenPaths() []string {
	return hiddenPaths(m.Pools())
}

func hiddenPaths(pools []Pool) []string {
	out := []string{}

	for _, p := range pools {
		if p.Path == homesRoot || strings.HasPrefix(p.Path, homesRoot+"/") {
			continue
		}

		out = append(out, p.Path)
	}

	return out
}

// UnitIsolation returns the lines that tie a site unit to the file system
// of its home and hide the other pools behind an empty, read only tmpfs:
// unit goes into [Unit], service into [Service]. The unit still has to
// mount the home back (BindPaths=). RequiresMountsFor= keeps the unit from
// starting while the volume of the home is missing, if the volume has a
// mount unit (an fstab entry, spike S9). Paths come from the registry,
// which allows only plain absolute paths.
func UnitIsolation(home string, hidden []string) (unit, service string) {
	var b strings.Builder

	for _, p := range hidden {
		b.WriteString("TemporaryFileSystem=" + p + ":ro\n")
	}

	return "RequiresMountsFor=" + home + "\n", b.String()
}

// CheckPool reports whether the pool of a site is usable: registered, its
// root safe, mounted if it must be and carrying its marker. Callers that
// create files below a pool outside the site's home (the page cache) ask
// first, so a missing volume never puts them on the root disk (spike S9).
func (m *Manager) CheckPool(name string) error {
	m.mu.Lock()
	p, ok := m.reg.Pool(name)
	m.mu.Unlock()

	if !ok {
		return fmt.Errorf("%w: pool %q is not registered", ErrPoolUnavailable, name)
	}

	fd, err := openPool(p, m.opt.OwnerUID)
	if err != nil {
		return err
	}

	return unix.Close(fd)
}
