// SPDX-License-Identifier: AGPL-3.0-or-later

// Package plan turns a CloudPanel scan report into a migration plan for this
// server (ADR-0017, ADR-0026). The report comes from another machine and is
// untrusted input: Parse checks its size, layout and every name and path
// again, and Build only describes what would happen. Nothing here changes the
// server.
package plan

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
)

// Limits for an uploaded report. web2 gives about 200 KiB for 21 sites with
// every snippet [V: S6 second run, estimate from the JSON size].
const (
	MaxReportBytes = 16 << 20
	MaxSites       = 2000
	maxCustom      = 500
	maxCron        = 200
	maxFindings    = 500
	maxSSHKeys     = 200
)

// ErrInvalid marks a report that is not usable at all.
var ErrInvalid = errors.New("plan: invalid report")

// Parse reads one report. Unknown fields, another format, trailing data, too
// many sites and values that the scanner would never write are refused, so a
// hand-edited or foreign file cannot smuggle names or paths into the import.
func Parse(r io.Reader) (*scan.Report, error) {
	b, err := io.ReadAll(io.LimitReader(r, MaxReportBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if len(b) > MaxReportBytes {
		return nil, fmt.Errorf("%w: larger than %d bytes", ErrInvalid, MaxReportBytes)
	}

	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()

	var rep scan.Report
	if err := dec.Decode(&rep); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if dec.More() {
		return nil, fmt.Errorf("%w: data after the report", ErrInvalid)
	}

	if rep.Format != scan.Format {
		return nil, fmt.Errorf("%w: format %q, want %q (scan again with this version of hostashi)", ErrInvalid, rep.Format, scan.Format)
	}

	if len(rep.Sites) == 0 || len(rep.Sites) > MaxSites {
		return nil, fmt.Errorf("%w: between 1 and %d sites required", ErrInvalid, MaxSites)
	}

	for i := range rep.Sites {
		if err := checkSite(&rep.Sites[i]); err != nil {
			return nil, fmt.Errorf("%w: site %d: %w", ErrInvalid, i+1, err)
		}
	}

	return &rep, nil
}

// checkSite checks the parts of a site that end up in names, paths or
// commands. A blocked site may lack them; its values are still bounded.
func checkSite(s *scan.Site) error {
	if len(s.Custom) > maxCustom || len(s.Cron) > maxCron || len(s.Findings) > maxFindings || len(s.SSHKeys) > maxSSHKeys {
		return errors.New("too many entries")
	}

	if !slices.Contains([]scan.Status{scan.StatusClean, scan.StatusReview, scan.StatusBlocked}, s.Status) {
		return fmt.Errorf("unknown status %q", s.Status)
	}

	if s.Status == scan.StatusBlocked {
		return nil
	}

	if err := agentapi.ValidateSiteUser(s.User); err != nil {
		return err
	}

	if len(s.Domains) == 0 || len(s.Domains)+len(s.RedirectFrom) > agentapi.MaxDomainsPerSite {
		return fmt.Errorf("between 1 and %d domains required", agentapi.MaxDomainsPerSite)
	}

	for _, d := range append(slices.Clone(s.Domains), s.RedirectFrom...) {
		if err := agentapi.ValidateDomain(d); err != nil {
			return err
		}
	}

	if err := CheckDocRoot(s.User, s.DocRoot); err != nil {
		return err
	}

	if s.Pool != nil {
		if s.Pool.User != s.User {
			return fmt.Errorf("pool runs as %q, not as the site user", s.Pool.User)
		}

		if s.Pool.Version != "" && !validVersion(s.Pool.Version) {
			return fmt.Errorf("php version %q", s.Pool.Version)
		}
	}

	return nil
}

// CheckDocRoot accepts a clean absolute path below /home/<user>/ on the old
// server. The transfer reads only below it (ADR-0026).
func CheckDocRoot(user, p string) error { return agentapi.ValidateRemoteDocRoot(user, p) }

// validVersion accepts "<major>.<minor>" with one digit and one or two digits.
func validVersion(v string) bool {
	maj, minor, ok := strings.Cut(v, ".")

	return ok && len(maj) == 1 && maj[0] >= '5' && maj[0] <= '9' &&
		len(minor) >= 1 && len(minor) <= 2 && strings.Trim(minor, "0123456789") == ""
}
