// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// The malware scan (ADR-0064). ClamAV comes from the distribution and is
// installed on opt-in by the fixed unit hostashi-clamav-install@; one scan
// of a site runs in hostashi-scan@<site> as the site user, read only. The
// panel stores schedule, runs and findings; the agent starts the units,
// reads a finished run's log and moves files into and out of the
// quarantine.

// The scan operations.
var (
	ScanEngineStatusOp = agentrpc.Op[ScanEngineStatusRequest, ScanEngineStatus]{Name: "scan.engine.status"}
	ScanEngineSet      = agentrpc.Op[ScanEngineSetRequest, ScanEngineStatus]{Name: "scan.engine.set"}
	ScanStart          = agentrpc.Op[ScanStartRequest, ScanStatus]{Name: "site.scan.start"}
	ScanStatusOp       = agentrpc.Op[ScanStatusRequest, ScanStatus]{Name: "site.scan.status"}
	ScanQuarantine     = agentrpc.Op[ScanQuarantineRequest, QuarantineItem]{Name: "site.scan.quarantine"}
	ScanRestore        = agentrpc.Op[ScanRestoreRequest, ScanRestored]{Name: "site.scan.restore"}
)

// The states of the scan engine (ClamAV on the server).
const (
	ScanEngineOff        = "off"
	ScanEngineInstalling = "installing"
	ScanEngineRemoving   = "removing"
	ScanEngineOn         = "on"
	ScanEngineFailed     = "failed"
)

// ScanEngineStatusRequest asks for the state of the scan engine.
type ScanEngineStatusRequest struct{}

// Validate implements agentrpc's request check.
func (ScanEngineStatusRequest) Validate() error { return nil }

// ScanEngineSetRequest installs (On) or removes ClamAV.
type ScanEngineSetRequest struct {
	On bool `json:"on"`
}

// Validate implements agentrpc's request check.
func (ScanEngineSetRequest) Validate() error { return nil }

// ScanEngineStatus is the state of ClamAV on the server.
type ScanEngineStatus struct {
	State string `json:"state"`
	// Version is clamscan's version, "" while not installed.
	Version string `json:"version,omitempty"`
	// Signatures is when the newest signature file was written, RFC 3339,
	// "" if there is none yet (freshclam has not run).
	Signatures string `json:"signatures,omitempty"`
	// Error is the end of the install log when State is failed.
	Error string `json:"error,omitempty"`
}

// ScanStartRequest starts a scan of a site (site.scan.start).
type ScanStartRequest struct {
	Site string `json:"site"`
	// ID is the run's id in the panel.
	ID int64 `json:"id"`
}

// Validate implements agentrpc's request check.
func (r ScanStartRequest) Validate() error {
	if r.ID <= 0 {
		return errors.Join(ValidateSiteUser(r.Site), fmt.Errorf("invalid scan id %d", r.ID))
	}

	return ValidateSiteUser(r.Site)
}

// ScanStatusRequest asks for the state of a scan run.
type ScanStatusRequest = ScanStartRequest

// The states of a scan run.
const (
	ScanRunning   = "running"
	ScanSucceeded = "succeeded"
	ScanFailed    = "failed"
	// ScanUnknown: the unit ran another scan since, or never ran this one.
	ScanUnknown = "unknown"
)

// MaxScanFindings bounds the findings one run reports; the rest are
// counted in Dropped.
const MaxScanFindings = 1000

// ScanStatus is the state of a scan run. Findings are only set once it
// has finished.
type ScanStatus struct {
	Site     string        `json:"site"`
	ID       int64         `json:"id"`
	State    string        `json:"state"`
	Error    string        `json:"error,omitempty"`
	Findings []ScanFinding `json:"findings,omitempty"`
	// Dropped counts findings over MaxScanFindings and lines that did not
	// name a regular file inside the home.
	Dropped int `json:"dropped,omitempty"`
}

// ScanFinding is one file ClamAV flagged. Path is relative to the site's
// home.
type ScanFinding struct {
	Path      string `json:"path"`
	Signature string `json:"signature"`
	SHA256    string `json:"sha256"`
	Size      int64  `json:"size"`
}

// MaxScanPath bounds the relative path of a finding.
const MaxScanPath = 4096

var (
	sha256Re    = regexp.MustCompile(`^[0-9a-f]{64}$`)
	signatureRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:/+-]{0,199}$`)
)

// ValidateScanPath checks the relative path of a finding: clean, inside
// the home, no control characters.
func ValidateScanPath(p string) error {
	if p == "" || len(p) > MaxScanPath || path.IsAbs(p) || path.Clean(p) != p || p == "." || p == ".." || strings.HasPrefix(p, "../") {
		return fmt.Errorf("invalid path %q", p)
	}

	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("invalid path %q", p)
		}
	}

	return nil
}

// ValidateSignature checks a ClamAV signature name.
func ValidateSignature(s string) error {
	if !signatureRe.MatchString(s) {
		return fmt.Errorf("invalid signature %q", s)
	}

	return nil
}

// ValidateSHA256 checks a hex SHA-256.
func ValidateSHA256(s string) error {
	if !sha256Re.MatchString(s) {
		return fmt.Errorf("invalid SHA-256 %q", s)
	}

	return nil
}

// ScanQuarantineRequest moves one flagged file of a site into the
// quarantine, only while it still has the SHA-256 of the finding.
type ScanQuarantineRequest struct {
	Site   string `json:"site"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// Validate implements agentrpc's request check.
func (r ScanQuarantineRequest) Validate() error {
	return errors.Join(ValidateSiteUser(r.Site), ValidateScanPath(r.Path), ValidateSHA256(r.SHA256))
}

var quarantineIDRe = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ValidateQuarantineID checks the id of a quarantined file.
func ValidateQuarantineID(id string) error {
	if !quarantineIDRe.MatchString(id) {
		return fmt.Errorf("invalid quarantine id %q", id)
	}

	return nil
}

// QuarantineItem is a file in the quarantine.
type QuarantineItem struct {
	ID     string `json:"id"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// ScanRestoreRequest puts a quarantined file back where it was.
type ScanRestoreRequest struct {
	Site string `json:"site"`
	ID   string `json:"id"`
}

// Validate implements agentrpc's request check.
func (r ScanRestoreRequest) Validate() error {
	return errors.Join(ValidateSiteUser(r.Site), ValidateQuarantineID(r.ID))
}

// ScanRestored names the restored file.
type ScanRestored struct {
	Path string `json:"path"`
}
