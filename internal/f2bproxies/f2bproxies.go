// SPDX-License-Identifier: AGPL-3.0-or-later

// Package f2bproxies keeps the trusted proxies of the sites (ADR-0059) out
// of fail2ban's bans. fail2ban bans the TCP peer on all ports for the
// panel and recidive jails, so a ban of a proxy edge would block every
// visitor who comes through it. The file is a jail.d ".local" file, which
// fail2ban reads after every jail.d ".conf" file, so its ignoreip replaces
// the one of the installer's hostashi.conf.
package f2bproxies

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
)

// DefaultPath is the file on the server.
const DefaultPath = "/etc/fail2ban/jail.d/hostashi-proxies.local"

// BaseIgnore is the installer's never-ban list (packaging/fail2ban/
// hostashi.conf); the file repeats it, because it replaces that value.
const BaseIgnore = "127.0.0.0/8 ::1"

// Render returns the file for prefixes, or nil when there are none. The
// prefixes come from agentapi.WebRealIP.Prefixes, so each is a parsed
// value; they are sorted and deduplicated so the file only changes when
// the set does.
func Render(prefixes []netip.Prefix) []byte {
	ps := slices.Clone(prefixes)
	for i, p := range ps {
		ps[i] = p.Masked()
	}

	slices.SortFunc(ps, func(a, b netip.Prefix) int {
		if c := a.Addr().Compare(b.Addr()); c != 0 {
			return c
		}

		return a.Bits() - b.Bits()
	})

	ps = slices.Compact(ps)
	if len(ps) == 0 {
		return nil
	}

	var b bytes.Buffer

	b.WriteString("# Managed by Hostashi (ADR-0059); web.apply rewrites this file.\n")
	b.WriteString("# Trusted proxies of the sites are never banned: they carry the\n")
	b.WriteString("# traffic of many visitors.\n\n[DEFAULT]\nignoreip = " + BaseIgnore)

	for _, p := range ps {
		if !p.IsValid() {
			continue
		}

		b.WriteString("\n           " + p.String())
	}

	b.WriteString("\n")

	return b.Bytes()
}

// File is the managed file and how fail2ban learns about a change.
type File struct {
	Path string
	// Reload makes fail2ban read its configuration again; nil skips it.
	Reload func(ctx context.Context) error
}

// Sync writes the file for prefixes (or removes it when there are none)
// and reloads fail2ban when the content changed. It reports whether it
// changed anything.
func (f *File) Sync(ctx context.Context, prefixes []netip.Prefix) (bool, error) {
	want := Render(prefixes)

	have, err := os.ReadFile(f.Path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		if want == nil {
			return false, nil
		}
	case err != nil:
		return false, fmt.Errorf("f2bproxies: %w", err)
	case bytes.Equal(have, want):
		return false, nil
	}

	if want == nil {
		if err := os.Remove(f.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("f2bproxies: %w", err)
		}
	} else if err := writeAtomic(f.Path, want); err != nil {
		return false, err
	}

	if f.Reload != nil {
		if err := f.Reload(ctx); err != nil {
			return true, fmt.Errorf("f2bproxies: reload fail2ban: %w", err)
		}
	}

	return true, nil
}

func writeAtomic(path string, b []byte) error {
	dir := filepath.Dir(path)

	tmp, err := os.CreateTemp(dir, ".hostashi-proxies-*")
	if err != nil {
		return fmt.Errorf("f2bproxies: %w", err)
	}

	defer os.Remove(tmp.Name())

	for _, step := range []func() error{
		func() error { _, err := tmp.Write(b); return err },
		func() error { return tmp.Chmod(0o644) },
		tmp.Sync,
	} {
		if err := step(); err != nil {
			_ = tmp.Close()

			return fmt.Errorf("f2bproxies: %w", err)
		}
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("f2bproxies: %w", err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("f2bproxies: %w", err)
	}

	return nil
}
