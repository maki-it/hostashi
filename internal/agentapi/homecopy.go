// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// SiteHomeCopy starts copying the home of one site into the home of
// another (ADR-0070: cloning and staging). The copy works on file
// descriptors, never follows a symbolic link and never leaves the source
// home's file system; every copied entry belongs to the target site. The
// source's SSH keys and its Git deploy cache and results are not copied.
// It answers at once with the running job; SiteHomeCopyStatus reports it.
var SiteHomeCopy = agentrpc.Op[SiteHomeCopyRequest, SiteHomeCopyJob]{Name: "site.home.copy"}

// SiteHomeCopyStatus reports a copy job the agent still remembers.
var SiteHomeCopyStatus = agentrpc.Op[SiteHomeCopyStatusRequest, SiteHomeCopyJob]{Name: "site.home.copy.status"}

// HomeCopySkippedDirs are the paths below a home that a copy leaves out:
// the site's SSH keys and the parts of a Git deploy that belong to the
// source (ADR-0070). Release folders, shared paths and the deploy link
// are copied.
var HomeCopySkippedDirs = []string{".ssh", DeployCacheDir, DeployResultsDir}

// SiteHomeCopyRequest names the source and the target site.
type SiteHomeCopyRequest struct {
	Source string `json:"source"`
	Target string `json:"target"`
	// Replace first removes everything in the target home except the
	// skipped paths ("refresh from source"). Without it, an entry that
	// exists in the target already fails the copy, folders excepted.
	Replace bool `json:"replace,omitempty"`
}

// Validate checks the names.
func (r SiteHomeCopyRequest) Validate() error {
	if err := ValidateSiteUser(r.Source); err != nil {
		return fmt.Errorf("source: %w", err)
	}

	if err := ValidateSiteUser(r.Target); err != nil {
		return fmt.Errorf("target: %w", err)
	}

	if r.Source == r.Target {
		return errors.New("target: must differ from the source")
	}

	return nil
}

// SiteHomeCopyStatusRequest names a job.
type SiteHomeCopyStatusRequest struct {
	ID string `json:"id"`
}

// Validate checks the id.
func (r SiteHomeCopyStatusRequest) Validate() error {
	if !jobIDRe.MatchString(r.ID) {
		return fmt.Errorf("id: must match %s", jobIDRe)
	}

	return nil
}

// SiteHomeCopyJob is the state of a copy. The counters grow while it runs.
type SiteHomeCopyJob struct {
	ID      string `json:"id"`
	Source  string `json:"source"`
	Target  string `json:"target"`
	Replace bool   `json:"replace,omitempty"`
	// State is JobRunning, JobDone or JobFailed.
	State string `json:"state"`
	// Error is a fixed text, never a path or a system message.
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started"`
	Finished time.Time `json:"finished,omitzero"`
	// Dirs, Files and Links count the copied entries; Bytes the file data.
	Dirs  int64 `json:"dirs"`
	Files int64 `json:"files"`
	Links int64 `json:"links"`
	Bytes int64 `json:"bytes"`
	// Skipped counts entries left out (other file system, device file,
	// socket, hard link to a foreign file, too deep); SkippedPaths names up
	// to MaxPermissionsSkipped of them. The skipped dirs are not counted.
	Skipped      int64             `json:"skipped"`
	SkippedPaths []PermissionsSkip `json:"skippedPaths,omitempty"`
}
