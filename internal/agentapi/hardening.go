// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// WebHardening are the hardening rules of a site (ADR-0017 Part 4,
// ADR-0069): typed switches and path lists that nginx answers with 404
// before the site's files or PHP see the request. Hidden files (dot paths
// except /.well-known/) always answer 404 and need no switch.
//
// Every path is relative to the site's document root, without a leading or
// trailing slash, with the rules of ValidateWebDir for each segment.
type WebHardening struct {
	// ConfigFiles answers 404 for dump, backup, log and editor files
	// (ConfigFileSuffixes), wherever they are below the document root.
	ConfigFiles bool `json:"configFiles,omitempty"`
	// NoPHPDirs are folders in which PHP never runs: a PHP file there
	// answers 404 instead of reaching PHP-FPM, for example
	// "wp-content/uploads". They need PHP.
	NoPHPDirs []string `json:"noPhpDirs,omitempty"`
	// DenyDirs are folders that are never served (404), with everything in
	// them.
	DenyDirs []string `json:"denyDirs,omitempty"`
	// DenyFiles are files that are never served (404), for example
	// "xmlrpc.php".
	DenyFiles []string `json:"denyFiles,omitempty"`
	// LimitFiles are PHP files that answer at most LimitRate requests per
	// minute and client address, with a burst of LimitBurst, and 429 above
	// it, for example "wp-login.php". They need PHP.
	LimitFiles []string `json:"limitFiles,omitempty"`
}

// MaxHardeningPaths bounds each path list of WebHardening.
const MaxHardeningPaths = 20

// Rate limit of WebHardening.LimitFiles: requests per minute per client
// address, and the requests above the rate that are let through at once.
const (
	LimitRate  = 12
	LimitBurst = 10
)

// ConfigFileSuffixes are the file name endings that WebHardening.ConfigFiles
// answers with 404: database dumps (also compressed), backups and editor
// copies, logs and environment or ini files. Lowercase; nginx compares
// them without case.
var ConfigFileSuffixes = []string{
	".sql", ".sql.gz", ".sql.bz2", ".sql.xz", ".sql.zst", ".dump",
	".bak", ".backup", ".old", ".orig", ".save", ".swp", ".swo", "~",
	".log", ".env", ".ini",
}

// IsZero reports whether no rule beyond the fixed ones is set.
func (h WebHardening) IsZero() bool {
	return !h.ConfigFiles && len(h.NoPHPDirs) == 0 && len(h.DenyDirs) == 0 && len(h.DenyFiles) == 0 && len(h.LimitFiles) == 0
}

// Validate checks every path list. php tells whether the site runs PHP,
// which NoPHPDirs and LimitFiles need.
func (h WebHardening) Validate(php bool) error {
	if !php && (len(h.NoPHPDirs) > 0 || len(h.LimitFiles) > 0) {
		return errors.New("hardening: folders without PHP and rate limited files need PHP")
	}

	lists := []struct {
		name  string
		paths []string
	}{
		{"folder without PHP", h.NoPHPDirs},
		{"blocked folder", h.DenyDirs},
		{"blocked file", h.DenyFiles},
		{"rate limited file", h.LimitFiles},
	}

	for _, l := range lists {
		if len(l.paths) > MaxHardeningPaths {
			return fmt.Errorf("hardening: more than %d entries of %ss", MaxHardeningPaths, l.name)
		}

		seen := map[string]bool{}

		for _, p := range l.paths {
			if p == "" {
				return fmt.Errorf("hardening: empty %s", l.name)
			}

			if err := ValidateWebDir(p); err != nil {
				return fmt.Errorf("hardening: %s %q: use names of letters, digits, '.', '_' and '-', separated by '/', without a leading slash", l.name, p)
			}

			if seen[p] {
				return fmt.Errorf("hardening: %s %q is listed twice", l.name, p)
			}

			seen[p] = true
		}
	}

	for _, f := range h.LimitFiles {
		if !strings.HasSuffix(f, ".php") {
			return fmt.Errorf("hardening: rate limited file %q: only PHP files can be rate limited", f)
		}

		// nginx refuses two exact locations for the same path.
		if slices.Contains(h.DenyFiles, f) {
			return fmt.Errorf("hardening: %q is blocked and rate limited; choose one", f)
		}
	}

	return nil
}
