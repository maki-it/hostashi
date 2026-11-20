// SPDX-License-Identifier: AGPL-3.0-or-later

// Package phpctl runs the distribution's PHP-FPM for package php: php-fpm -t
// on a pool file, and the per-site units through systemd. It also drives the
// units of the sites' cron jobs (cron.go, ADR-0028), which are per-site
// units of the same kind, and reloads systemd after the agent changed a
// site's slice and applies its limits (ADR-0015). For hostashi doctor it
// reads whether Hostashi's firewall unit is active (system.go, ADR-0033),
// and it reloads that unit after the panel changed its rules (ADR-0034).
//
// Like internal/webctl it is allowed to start processes (depguard rule in
// .golangci.yml), with the same narrow exception: fixed binaries by absolute
// path (php-fpm<version> for a version from agentapi.PHPVersions, and
// systemctl), fixed argument lists whose only variables are the config path
// and a unit name that match fixed patterns, no shell, a minimal
// environment, and output only in the agent's log and error values.
package phpctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Systemctl is systemd's control tool (Debian 13 and Ubuntu 26.04 have
// merged /usr).
const Systemctl = "/usr/bin/systemctl"

// Binary returns the FPM binary of a PHP version, for example
// /usr/sbin/php-fpm8.4.
func Binary(version string) (string, error) {
	if err := agentapi.ValidatePHPVersion(version); err != nil {
		return "", err
	}

	return "/usr/sbin/php-fpm" + version, nil
}

// Installed returns the versions of agentapi.PHPVersions whose FPM binary
// exists, in that order.
func Installed() []string {
	out := []string{}

	for _, v := range agentapi.PHPVersions {
		b, _ := Binary(v)
		if st, err := os.Stat(b); err == nil && st.Mode().IsRegular() {
			out = append(out, v)
		}
	}

	return out
}

const commandTimeout = 60 * time.Second

var (
	confRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]+\.conf$`)
	unitRe = regexp.MustCompile(`^hostashi-php-[0-9]\.[0-9]@[a-z][a-z0-9_-]{1,31}\.service$`)
	// A site slice: the user with "-" escaped as \x2d (siteslice.Name), or
	// the user slice of a site user, uid 1000 and up (siteslice.UserSlice).
	sliceRe = regexp.MustCompile(`^(?:hostashi-site-[a-z](?:[a-z0-9_]|\\x2d){1,31}|user-[1-9][0-9]{3,9})\.slice$`)
	// The slice limits siteslice sets: fixed names, a number, a percentage
	// for CPUQuota, infinity, or empty to reset.
	slicePropRe = regexp.MustCompile(`^(?:CPUWeight=[0-9]{1,5}|CPUQuota=(?:[0-9]{1,6}%)?|(?:MemoryHigh|MemoryMax|TasksMax)=(?:[0-9]{1,20}|infinity))$`)
)

// Controller implements php.Controller.
type Controller struct{}

// Test runs php-fpm<version> -t on the FPM config file conf.
func (Controller) Test(ctx context.Context, version, conf string) error {
	bin, err := Binary(version)
	if err != nil {
		return err
	}

	if !confRe.MatchString(conf) || strings.Contains(conf, "..") {
		return fmt.Errorf("phpctl: refusing config path %q", conf)
	}

	return run(ctx, bin, "--test", "--fpm-config", conf)
}

// Start enables the unit and starts it now.
func (Controller) Start(ctx context.Context, unit string) error {
	return systemctl(ctx, unit, "enable", "--now")
}

// Reload makes a running unit re-read its config (php-fpm USR2: graceful,
// workers finish their requests) and starts it if it is not running.
func (Controller) Reload(ctx context.Context, unit string) error {
	return systemctl(ctx, unit, "reload-or-restart")
}

// Restart restarts a unit, which moves it into a changed slice (ADR-0015).
func (Controller) Restart(ctx context.Context, unit string) error {
	return systemctl(ctx, unit, "restart")
}

// DaemonReload makes systemd read changed unit files and drop-ins: the
// sites' slices and the PHP units' slice drop-ins (ADR-0015).
func (Controller) DaemonReload(ctx context.Context) error {
	return run(ctx, Systemctl, "daemon-reload")
}

// SetSliceProperties applies limits to a site's running slice for this
// boot (systemctl set-property --runtime); the slice file written before
// keeps them across reboots (ADR-0015).
func (Controller) SetSliceProperties(ctx context.Context, slice string, props []string) error {
	if !sliceRe.MatchString(slice) {
		return fmt.Errorf("phpctl: refusing slice name %q", slice)
	}

	if len(props) == 0 {
		return errors.New("phpctl: no slice properties")
	}

	for _, p := range props {
		if !slicePropRe.MatchString(p) {
			return fmt.Errorf("phpctl: refusing slice property %q", p)
		}
	}

	return run(ctx, Systemctl, append([]string{"set-property", "--runtime", slice}, props...)...)
}

// Stop stops the unit now and disables it.
func (Controller) Stop(ctx context.Context, unit string) error {
	return systemctl(ctx, unit, "disable", "--now")
}

func systemctl(ctx context.Context, unit string, args ...string) error {
	if !unitRe.MatchString(unit) {
		return fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	return run(ctx, Systemctl, append(args, unit)...)
}

func run(ctx context.Context, path string, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var out bytes.Buffer

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	cmd.Dir = "/"
	cmd.Stdout = &limitedWriter{buf: &out, max: 4096}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("phpctl: %s %s exited %d: %s", path, args[0], ee.ExitCode(), strings.TrimSpace(out.String()))
		}

		return fmt.Errorf("phpctl: %s: %w", path, err)
	}

	return nil
}

// limitedWriter keeps the first max bytes and drops the rest.
type limitedWriter struct {
	buf *bytes.Buffer
	max int
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); room > 0 {
		_, _ = w.buf.Write(p[:min(room, len(p))])
	}

	return len(p), nil
}
