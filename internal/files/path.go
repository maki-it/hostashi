// SPDX-License-Identifier: AGPL-3.0-or-later

// Package files is the file manager's helper (ADR-0040). It runs as the
// site user in hostashi-files@<site>.service, opens the site's home once and
// resolves every path beneath it with openat2 and RESOLVE_BENEATH,
// RESOLVE_NO_SYMLINKS, RESOLVE_NO_MAGICLINKS and RESOLVE_NO_XDEV, the
// pattern of spike S4. Symlinks are listed, never followed; device files,
// sockets and FIFOs are listed, never opened.
//
// The panel reaches the helper over a Unix socket that systemd creates for
// the instance; only the panel's group may connect. The panel checks the
// role and the site grant; the helper trusts its socket and nothing else.
// It never runs as root (the subcommand refuses), so every write is the site
// user's and counts against the site's quota and slice (ADR-0015).
package files

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Limits of a path. Linux allows 255 bytes per name and 4096 per path.
const (
	MaxNameBytes = 255
	MaxPathBytes = 4096
)

// ErrInvalidPath is returned for a path the helper does not accept.
var ErrInvalidPath = errors.New("files: invalid path")

// ValidName checks one path component: valid UTF-8, not empty, not "." or
// "..", no "/" or NUL, at most MaxNameBytes bytes.
func ValidName(name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return fmt.Errorf("%w: name %q", ErrInvalidPath, name)
	case len(name) > MaxNameBytes:
		return fmt.Errorf("%w: name longer than %d bytes", ErrInvalidPath, MaxNameBytes)
	case !utf8.ValidString(name):
		return fmt.Errorf("%w: name is not valid UTF-8", ErrInvalidPath)
	case strings.ContainsAny(name, "/\x00"):
		return fmt.Errorf("%w: name contains / or NUL", ErrInvalidPath)
	}

	return nil
}

// CleanPath checks a path relative to the home and returns it in its one
// accepted spelling: components joined by "/", no leading or trailing "/".
// "" and "/" name the home itself. Empty components ("a//b") are refused
// rather than merged, so the panel and the helper always mean the same path.
func CleanPath(p string) (string, error) {
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return "", nil
	}

	if len(p) > MaxPathBytes {
		return "", fmt.Errorf("%w: longer than %d bytes", ErrInvalidPath, MaxPathBytes)
	}

	p = strings.TrimSuffix(p, "/")

	for _, c := range strings.Split(p, "/") {
		if err := ValidName(c); err != nil {
			return "", err
		}
	}

	return p, nil
}

// split returns the parent and the last component of a cleaned, non-empty
// path. The parent of a top-level entry is "".
func split(p string) (parent, name string) {
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return "", p
	}

	return p[:i], p[i+1:]
}

// within reports whether p is q or lies below q (both cleaned).
func within(p, q string) bool {
	return q == "" || p == q || strings.HasPrefix(p, q+"/")
}
