// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// Git deploy runs of a site (ADR-0062). The panel stores the settings and
// the history; the agent writes the deploy unit's drop-in, scripts and
// credentials, starts the unit, reports its state and switches the live
// link.

// The deploy operations.
var (
	DeployStart    = agentrpc.Op[DeployStartRequest, DeployStatus]{Name: "site.deploy.start"}
	DeployStatusOp = agentrpc.Op[DeployStatusRequest, DeployStatus]{Name: "site.deploy.status"}
	DeploySwitch   = agentrpc.Op[DeploySwitchRequest, DeploySwitchResult]{Name: "site.deploy.switch"}
	DeployPrune    = agentrpc.Op[DeployPruneRequest, DeployReleases]{Name: "site.deploy.prune"}
	DeployLogOp    = agentrpc.Op[DeployStatusRequest, DeployLog]{Name: "site.deploy.log"}
)

// The deploy files of a site below its home, all the site user's: the
// bare cache, the releases, the shared paths and git-sync's results.
// Releases are named by the deploy id, or InitialRelease for the files
// that were in the deploy directory before the first deploy.
const (
	DeployBase        = ".deploy"
	DeployCacheDir    = ".deploy/repo"
	DeployReleasesDir = ".deploy/releases"
	DeploySharedDir   = ".deploy/shared"
	DeployResultsDir  = ".deploy/results"
	InitialRelease    = "0-initial"
)

var releaseRe = regexp.MustCompile(`^(?:[1-9][0-9]{0,18}|0-initial)$`)

// ValidateRelease checks a release name.
func ValidateRelease(r string) error {
	if !releaseRe.MatchString(r) {
		return fmt.Errorf("invalid release %q", r)
	}

	return nil
}

// ReleaseName is the release of a deploy id.
func ReleaseName(id int64) string { return strconv.FormatInt(id, 10) }

// DeployStartRequest starts the deploy unit of a site (site.deploy.start):
// git-sync into a new release, then the build commands in it.
type DeployStartRequest struct {
	Site string `json:"site"`
	// ID is the deploy's id in the panel; the release gets its name.
	ID        int64    `json:"id"`
	URL       string   `json:"url"`
	Branch    string   `json:"branch"`
	Auth      string   `json:"auth"`
	TokenUser string   `json:"tokenUser,omitempty"`
	Token     string   `json:"token,omitempty"`
	Key       string   `json:"key,omitempty"`
	HostKey   string   `json:"hostKey,omitempty"`
	Shared    []string `json:"shared,omitempty"`
	Commands  []string `json:"commands,omitempty"`
	// Node or Python is the site's line, whose bin directory comes first
	// in the PATH of the build commands; at most one is set.
	Node   string `json:"node,omitempty"`
	Python string `json:"python,omitempty"`
	// TimeoutSeconds bounds the build commands.
	TimeoutSeconds int `json:"timeoutSeconds"`
}

// Validate implements agentrpc's request check.
func (r DeployStartRequest) Validate() error {
	checks := []error{
		ValidateSiteUser(r.Site), ValidateGitBranch(r.Branch), ValidateGitShared(r.Shared), ValidateGitCommands(r.Commands),
		ValidateGitBuildTimeout(time.Duration(r.TimeoutSeconds) * time.Second),
	}

	if r.ID < 1 {
		checks = append(checks, errors.New("deploy id must be positive"))
	}

	switch {
	case r.Node != "" && r.Python != "":
		checks = append(checks, errors.New("both a Node.js and a Python line"))
	case r.Node != "":
		checks = append(checks, ValidateNodeMajor(r.Node))
	case r.Python != "":
		checks = append(checks, ValidatePythonLine(r.Python))
	}

	rem, err := ParseGitURL(r.URL)
	checks = append(checks, err)

	switch r.Auth {
	case GitAuthNone:
	case GitAuthToken:
		checks = append(checks, ValidateGitToken(r.Token), ValidateGitTokenUser(r.TokenUser))
	case GitAuthSSH:
		if r.Key == "" || len(r.Key) > 8192 {
			checks = append(checks, errors.New("deploy key missing or too large"))
		}

		if r.HostKey == "" || len(r.HostKey) > 8192 {
			checks = append(checks, errors.New("the host key is not confirmed"))
		}

		if err == nil && rem.Scheme != "ssh" {
			checks = append(checks, errors.New("the deploy key needs an SSH address"))
		}
	default:
		checks = append(checks, fmt.Errorf("unknown auth %q", r.Auth))
	}

	return errors.Join(checks...)
}

// DeployStatusRequest asks for the state of a deploy run.
type DeployStatusRequest struct {
	Site string `json:"site"`
	ID   int64  `json:"id"`
}

// Validate implements agentrpc's request check.
func (r DeployStatusRequest) Validate() error {
	if r.ID < 1 {
		return errors.New("deploy id must be positive")
	}

	return ValidateSiteUser(r.Site)
}

// The states of a deploy run.
const (
	DeployRunning   = "running"
	DeploySucceeded = "succeeded"
	DeployFailed    = "failed"
	// DeployUnknown: the unit ran another deploy since, or never ran this
	// one.
	DeployUnknown = "unknown"
)

// DeployStatus is the state of a deploy run.
type DeployStatus struct {
	Site  string `json:"site"`
	ID    int64  `json:"id"`
	State string `json:"state"`
	// Commit, Author and Message come from git-sync once it finished.
	Commit  string `json:"commit,omitempty"`
	Author  string `json:"author,omitempty"`
	Message string `json:"message,omitempty"`
	// Error says why a failed run failed, when the agent knows.
	Error string `json:"error,omitempty"`
	// Log is the end of the run's log, at most MaxDeployLogTail bytes.
	Log string `json:"log,omitempty"`
}

// MaxDeployLogTail is how much of a run's log a status carries.
const MaxDeployLogTail = 8 << 10

// DeploySwitchRequest makes a release live: the deploy directory becomes a
// link to it.
type DeploySwitchRequest struct {
	Site    string `json:"site"`
	Release string `json:"release"`
	// Dir is the deploy directory below the home.
	Dir string `json:"dir"`
}

// Validate implements agentrpc's request check.
func (r DeploySwitchRequest) Validate() error {
	return errors.Join(ValidateSiteUser(r.Site), ValidateRelease(r.Release), ValidateGitDeployDir(r.Dir))
}

// DeploySwitchResult names the release that was live before; "" for none
// and InitialRelease when the directory's own files were moved aside.
type DeploySwitchResult struct {
	Previous string `json:"previous"`
}

// DeployPruneRequest deletes the releases of a site that are not in Keep,
// and the live one never.
type DeployPruneRequest struct {
	Site string   `json:"site"`
	Dir  string   `json:"dir"`
	Keep []string `json:"keep"`
}

// Validate implements agentrpc's request check.
func (r DeployPruneRequest) Validate() error {
	checks := []error{ValidateSiteUser(r.Site), ValidateGitDeployDir(r.Dir)}

	if len(r.Keep) > MaxGitKeep+1 {
		checks = append(checks, errors.New("too many releases to keep"))
	}

	for _, k := range r.Keep {
		checks = append(checks, ValidateRelease(k))
	}

	return errors.Join(checks...)
}

// DeployReleases lists a site's releases after a prune, and the live one.
type DeployReleases struct {
	Releases []string `json:"releases"`
	Live     string   `json:"live"`
}

// MaxDeployLog is how much of a run's log site.deploy.log returns: its
// end.
const MaxDeployLog = 256 << 10

// DeployLog is the log of a run (site.deploy.log), its end when it is
// longer than MaxDeployLog; Truncated says so. A run without a log has
// an empty one.
type DeployLog struct {
	Log       string `json:"log"`
	Truncated bool   `json:"truncated"`
}
