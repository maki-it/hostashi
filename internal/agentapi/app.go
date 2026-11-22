// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

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
	// AppStartBinary runs an executable file below the home with its
	// arguments (the app type, ADR-0054 addendum 2).
	AppStartBinary = "binary"
	// AppStartWSGI runs `python -m gunicorn` with a module:callable target
	// of a Python app (ADR-0061); Hostashi adds the bind address.
	AppStartWSGI = "wsgi"
	// AppStartASGI runs `python -m uvicorn` the same way.
	AppStartASGI = "asgi"
)

// PythonVenvDir is a Python site's virtual environment below its home, made
// by Hostashi (ADR-0061).
const PythonVenvDir = ".venv"

var pythonTargetRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*:[A-Za-z_][A-Za-z0-9_]*$`)

// pythonBindArgs are the options of gunicorn and uvicorn that set where
// they listen; Hostashi sets that itself (ADR-0061).
var pythonBindArgs = []string{"-b", "--bind", "--host", "--port", "--fd", "--uds"}

// ValidatePythonStart checks the start of a Python app: kind "" with an
// empty value (not set yet), wsgi or asgi with a module:callable target,
// or script with a .py file below the working directory.
func ValidatePythonStart(kind, start string) error {
	switch kind {
	case "":
		if start != "" {
			return errors.New("app: a start command without its kind")
		}

		return nil
	case AppStartWSGI, AppStartASGI:
		if len(start) > 255 || !pythonTargetRe.MatchString(start) {
			return fmt.Errorf("app: target %q must look like module.path:callable", start)
		}

		return nil
	case AppStartScript:
		if path.Ext(start) != ".py" {
			return fmt.Errorf("app: start file %q must end in .py", start)
		}

		if err := ValidateWebDir(start); err != nil {
			return fmt.Errorf("app start file: %w", err)
		}

		return nil
	}

	return fmt.Errorf("app: a Python app cannot start with %q", kind)
}

// ValidatePythonArgs checks the extra arguments of a Python app with the
// rules of ValidateAppArgs. For wsgi and asgi it refuses the options that
// set the address: this catches mistakes, it is no boundary.
func ValidatePythonArgs(kind string, args []string) error {
	if err := ValidateAppArgs(args); err != nil {
		return err
	}

	if kind != AppStartWSGI && kind != AppStartASGI {
		return nil
	}

	for _, a := range args {
		for _, o := range pythonBindArgs {
			short := len(o) == 2 && strings.HasPrefix(a, o)
			if a == o || strings.HasPrefix(a, o+"=") || short {
				return fmt.Errorf("app: %s is set by Hostashi: the app listens on 127.0.0.1 and its port", o)
			}
		}
	}

	return nil
}

// The bounds of a binary app's arguments (ADR-0054 addendum 2).
const (
	MaxAppArgs     = 16
	MaxAppArgBytes = 256
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
	case AppStartBinary:
		if start == "" {
			return errors.New("app: the program file is empty")
		}

		if err := ValidateWebDir(start); err != nil {
			return fmt.Errorf("app program file: %w", err)
		}

		return nil
	}

	return fmt.Errorf("app: unknown start kind %q", kind)
}

// ValidateAppArgs checks the arguments of a binary app: at most
// MaxAppArgs, each at most MaxAppArgBytes of UTF-8 without NUL, newline or
// carriage return. Anything else is allowed; the agent quotes every
// argument for systemd.
func ValidateAppArgs(args []string) error {
	if len(args) > MaxAppArgs {
		return fmt.Errorf("app: at most %d arguments", MaxAppArgs)
	}

	for i, a := range args {
		switch {
		case len(a) > MaxAppArgBytes:
			return fmt.Errorf("app: argument %d is longer than %d bytes", i+1, MaxAppArgBytes)
		case !utf8.ValidString(a):
			return fmt.Errorf("app: argument %d is not UTF-8", i+1)
		case strings.ContainsAny(a, "\x00\n\r"):
			return fmt.Errorf("app: argument %d holds a NUL, newline or carriage return", i+1)
		}
	}

	return nil
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
// hostashi-node@<site>.service (Node.js), hostashi-app@<site>.service
// (a binary, ADR-0054 addendum 2) or hostashi-python@<site>.service
// (ADR-0061).
type AppSpec struct {
	Site string `json:"site"`
	// Node is the Node.js line (NodeReleases) the app runs on; "" for a
	// binary or Python app.
	Node string `json:"node"`
	// Python is the Python line (PythonReleases) of a Python app; "" for
	// the others. At most one of Node and Python is set.
	Python    string `json:"python,omitempty"`
	Workdir   string `json:"workdir"`
	StartKind string `json:"startKind"`
	// Start is the script file or npm script below the working directory,
	// the program file of a binary app below the home, or the target or
	// .py file of a Python app.
	Start string `json:"start"`
	// Args are the arguments of a binary or Python app.
	Args []string `json:"args,omitempty"`
	Port int      `json:"port"`
	// Stopped keeps the app stopped (stopped in the panel, or no start
	// command yet).
	Stopped bool `json:"stopped,omitempty"`
	// Env are the app's own environment variables (ValidateAppEnv). The
	// agent keeps them only in a root-only file that systemd reads.
	Env []AppEnvVar `json:"env,omitempty"`
}

// Validate checks every field.
func (a AppSpec) Validate() error {
	if err := ValidateSiteUser(a.Site); err != nil {
		return err
	}

	switch {
	case a.Node != "" && a.Python != "":
		return errors.New("app: both a Node.js and a Python line")
	case a.IsPython():
		if err := ValidatePythonLine(a.Python); err != nil {
			return fmt.Errorf("app: %w", err)
		}

		if err := ValidatePythonStart(a.StartKind, a.Start); err != nil {
			return err
		}

		if a.StartKind == "" && len(a.Args) > 0 {
			return errors.New("app: arguments need a start command")
		}

		if err := ValidatePythonArgs(a.StartKind, a.Args); err != nil {
			return err
		}
	case a.Binary():
		if a.StartKind != "" && a.StartKind != AppStartBinary {
			return fmt.Errorf("app: a binary app cannot start with %q", a.StartKind)
		}

		if a.StartKind == "" && len(a.Args) > 0 {
			return errors.New("app: arguments need a program")
		}

		if err := ValidateAppArgs(a.Args); err != nil {
			return err
		}
	default:
		if err := ValidateNodeMajor(a.Node); err != nil {
			return fmt.Errorf("app: %w", err)
		}

		if a.StartKind == AppStartBinary {
			return errors.New("app: a Node.js app cannot start a binary")
		}

		if len(a.Args) > 0 {
			return errors.New("app: only a binary app takes arguments")
		}
	}

	if err := ValidateAppWorkdir(a.Workdir); err != nil {
		return err
	}

	if !a.IsPython() {
		if err := ValidateAppStart(a.StartKind, a.Start); err != nil {
			return err
		}
	}

	if a.StartKind == "" && !a.Stopped {
		return errors.New("app: an app without a start command must stay stopped")
	}

	if err := ValidateAppEnv(a.Env); err != nil {
		return err
	}

	return ValidateAppPort(a.Port)
}

// Binary reports whether the app is a binary (no Node.js or Python line).
func (a AppSpec) Binary() bool { return a.Node == "" && a.Python == "" }

// IsPython reports whether the app is a Python app (ADR-0061).
func (a AppSpec) IsPython() bool { return a.Python != "" }

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
	// Problem says why a binary or Python app that should run is not
	// started: its program file is missing, not executable or not the site
	// user's, or the Python environment or its app server is missing.
	Problem string `json:"problem,omitempty"`
}

// The app operations (ADR-0054).
var (
	AppApply     = agentrpc.Op[AppApplyRequest, AppStatus]{Name: "site.app.apply"}
	AppRemove    = agentrpc.Op[AppRemoveRequest, AppStatus]{Name: "site.app.remove"}
	AppAction    = agentrpc.Op[AppActionRequest, AppStatus]{Name: "site.app.action"}
	AppStatusOp  = agentrpc.Op[AppStatusRequest, AppStatus]{Name: "site.app.status"}
	PythonVenvOp = agentrpc.Op[PythonVenvRequest, AppStatus]{Name: "site.python.venv"}
)

// PythonVenvRequest creates a site's Python environment <home>/.venv with
// a line (site.python.venv, ADR-0061). Clear rebuilds an existing one
// (venv --clear): its packages are gone. The panel asks for Clear only
// after the typed confirmation.
type PythonVenvRequest struct {
	Site   string `json:"site"`
	Python string `json:"python"`
	Clear  bool   `json:"clear,omitempty"`
}

// Validate implements agentrpc's request check.
func (r PythonVenvRequest) Validate() error {
	if err := ValidateSiteUser(r.Site); err != nil {
		return err
	}

	return ValidatePythonLine(r.Python)
}
