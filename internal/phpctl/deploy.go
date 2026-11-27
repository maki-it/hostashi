// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// The sites' Git deploys (ADR-0062) go through the same exception as the
// apps: systemctl by absolute path, fixed verbs, and only unit names that
// match deployUnitRe. The agent never runs git or a build command itself;
// systemd runs `hostashi git-sync` (hostashi-deploy@) and the build script
// (hostashi-build@) as the site user from the fixed templates and the
// agent's drop-ins.
var (
	deployUnitRe = regexp.MustCompile(`^hostashi-(?:deploy|build)@[a-z][a-z0-9_-]{1,31}\.service$`)
	deployPropRe = regexp.MustCompile(`^(?:ActiveState|SubState|Result)$`)
)

// DeployController implements deployunit.Controller.
type DeployController struct{}

// DaemonReload makes systemd read changed drop-ins.
func (DeployController) DaemonReload(ctx context.Context) error {
	return run(ctx, Systemctl, "daemon-reload")
}

// ResetFailed clears the failed state of the last run.
func (DeployController) ResetFailed(ctx context.Context, unit string) error {
	return deployctl(ctx, unit, "reset-failed")
}

// StartNoBlock queues the start of a deploy and returns at once.
func (DeployController) StartNoBlock(ctx context.Context, unit string) error {
	return deployctl(ctx, unit, "start", "--no-block")
}

// Stop ends a running deploy. A unit that never ran is stopped already.
func (DeployController) Stop(ctx context.Context, unit string) error {
	err := deployctl(ctx, unit, "stop")

	var ee *ExitError
	if errors.As(err, &ee) && ee.Code == systemctlNotLoaded {
		return nil
	}

	return err
}

// Show returns properties of a deploy unit.
func (DeployController) Show(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	if !deployUnitRe.MatchString(unit) {
		return nil, fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	args := []string{"show"}

	for _, p := range props {
		if !deployPropRe.MatchString(p) {
			return nil, fmt.Errorf("phpctl: refusing property %q", p)
		}

		args = append(args, "--property="+p)
	}

	out, err := output(ctx, Systemctl, append(args, unit)...)
	if err != nil {
		return nil, err
	}

	m := map[string]string{}

	for l := range strings.SplitSeq(out, "\n") {
		if k, v, ok := strings.Cut(l, "="); ok {
			m[k] = v
		}
	}

	return m, nil
}

func deployctl(ctx context.Context, unit string, args ...string) error {
	if !deployUnitRe.MatchString(unit) {
		return fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	return run(ctx, Systemctl, append(args, unit)...)
}
