// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"path"
	"regexp"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// The loopback ports the panel hands to apps (ADR-0054). They lie below
// Linux's ephemeral range (32768-60999), so no outgoing connection takes
// one of them.
const (
	MinAppPort = 20000
	MaxAppPort = 29999
)

// The typed ways an app starts (ADR-0054): no free shell text.
const (
	// AppStartScript runs `node <file>`, a .js, .mjs or .cjs file below the
	// working directory.
	AppStartScript = "script"
	// AppStartNPM runs `npm run <script>`.
	AppStartNPM = "npm"
)

// DefaultAppWorkdir is the working directory of a new app, below the home.
const DefaultAppWorkdir = "htdocs"

var npmScriptRe = regexp.MustCompile(`^[a-z0-9:_-]{1,64}$`)

// ValidateAppWorkdir checks an app's working directory relative to the
// home, with the rules of ValidateWebDir; it must not be empty.
func ValidateAppWorkdir(dir string) error {
	if dir == "" {
		return errors.New("app: the working directory is empty")
	}

	if err := ValidateWebDir(dir); err != nil {
		return fmt.Errorf("app working directory: %w", err)
	}

	return nil
}

// ValidateAppStart checks a start command: kind "" with an empty value
// (not set yet), or one of the AppStart forms.
func ValidateAppStart(kind, start string) error {
	switch kind {
	case "":
		if start != "" {
			return errors.New("app: a start command without its kind")
		}

		return nil
	case AppStartScript:
		ext := path.Ext(start)
		if ext != ".js" && ext != ".mjs" && ext != ".cjs" {
			return fmt.Errorf("app: start file %q must end in .js, .mjs or .cjs", start)
		}

		if err := ValidateWebDir(start); err != nil {
			return fmt.Errorf("app start file: %w", err)
		}

		return nil
	case AppStartNPM:
		if !npmScriptRe.MatchString(start) {
			return fmt.Errorf("app: npm script %q must match %s", start, npmScriptRe)
		}

		return nil
	}

	return fmt.Errorf("app: unknown start kind %q", kind)
}

// ValidateAppPort checks a port from the app range.
func ValidateAppPort(port int) error {
	if port < MinAppPort || port > MaxAppPort {
		return fmt.Errorf("app: port %d is outside %d-%d", port, MinAppPort, MaxAppPort)
	}

	return nil
}

// WebApp makes nginx pass a site's requests to its app on 127.0.0.1:Port
// (ADR-0054).
type WebApp struct {
	Port int `json:"port"`
}

// AppSpec is the app of one site, as the agent runs it in
// hostashi-node@<site>.service.
type AppSpec struct {
	Site string `json:"site"`
	// Node is the Node.js line (NodeReleases) the app runs on.
	Node      string `json:"node"`
	Workdir   string `json:"workdir"`
	StartKind string `json:"startKind"`
	Start     string `json:"start"`
	Port      int    `json:"port"`
	// Stopped keeps the app stopped (stopped in the panel, or no start
	// command yet).
	Stopped bool `json:"stopped,omitempty"`
}

// Validate checks every field.
func (a AppSpec) Validate() error {
	if err := ValidateSiteUser(a.Site); err != nil {
		return err
	}

	if err := ValidateNodeMajor(a.Node); err != nil {
		return fmt.Errorf("app: %w", err)
	}

	if err := ValidateAppWorkdir(a.Workdir); err != nil {
		return err
	}

	if err := ValidateAppStart(a.StartKind, a.Start); err != nil {
		return err
	}

	if a.StartKind == "" && !a.Stopped {
		return errors.New("app: an app without a start command must stay stopped")
	}

	return ValidateAppPort(a.Port)
}

// AppApplyRequest sets up or changes a site's app (site.app.apply).
type AppApplyRequest struct {
	App AppSpec `json:"app"`
}

// Validate implements agentrpc's request check.
func (r AppApplyRequest) Validate() error { return r.App.Validate() }

// AppRemoveRequest stops a site's app and removes its unit files
// (site.app.remove), for a type change or the delete of the site.
type AppRemoveRequest struct {
	Site string `json:"site"`
}

// Validate implements agentrpc's request check.
func (r AppRemoveRequest) Validate() error { return ValidateSiteUser(r.Site) }

// The actions of site.app.action.
const (
	AppActionStart   = "start"
	AppActionStop    = "stop"
	AppActionRestart = "restart"
)

// AppActionRequest starts, stops or restarts a site's app now. It does not
// change whether the app stays stopped: the panel sends that with
// site.app.apply.
type AppActionRequest struct {
	Site   string `json:"site"`
	Action string `json:"action"`
}

// Validate implements agentrpc's request check.
func (r AppActionRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	switch r.Action {
	case AppActionStart, AppActionStop, AppActionRestart:
		return nil
	}

	return fmt.Errorf("app: unknown action %q", r.Action)
}

// AppStatusRequest asks for a site's app.
type AppStatusRequest struct {
	Site string `json:"site"`
}

// Validate implements agentrpc's request check.
func (r AppStatusRequest) Validate() error { return ValidateSiteUser(r.Site) }

// AppStatus is the state of a site's app: systemd's ActiveState and
// SubState ("" without an app), the restarts since it was set up and the
// memory it uses.
type AppStatus struct {
	Site      string `json:"site"`
	State     string `json:"state"`
	SubState  string `json:"subState,omitempty"`
	Restarts  int    `json:"restarts"`
	UsedBytes uint64 `json:"usedBytes,omitempty"`
	// Since is when the unit entered its state (RFC 3339), "" unknown.
	Since string `json:"since,omitempty"`
}

// The app operations (ADR-0054).
var (
	AppApply    = agentrpc.Op[AppApplyRequest, AppStatus]{Name: "site.app.apply"}
	AppRemove   = agentrpc.Op[AppRemoveRequest, AppStatus]{Name: "site.app.remove"}
	AppAction   = agentrpc.Op[AppActionRequest, AppStatus]{Name: "site.app.action"}
	AppStatusOp = agentrpc.Op[AppStatusRequest, AppStatus]{Name: "site.app.status"}
)
