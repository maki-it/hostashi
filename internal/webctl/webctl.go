// SPDX-License-Identifier: AGPL-3.0-or-later

// Package webctl runs the distribution's nginx for package web: nginx -t on
// a generation, and reload or restart through systemd.
//
// Like internal/sysuser it is allowed to start processes (depguard rule in
// .golangci.yml), and the exception is just as narrow: two fixed binaries by
// absolute path, fixed argument lists (the only variable is the config path
// the reconciler built), no shell, a minimal environment, and output only in
// the agent's log and error values.
package webctl

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// Fixed paths (Debian 13 and Ubuntu 26.04 have merged /usr).
const (
	Nginx     = "/usr/sbin/nginx"
	Systemctl = "/usr/bin/systemctl"
	// Unit is the distribution's nginx unit. The installer adds a drop-in
	// that starts it on the reconciler's config.
	Unit = "nginx.service"
)

const commandTimeout = 30 * time.Second

var confRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]+/nginx\.conf$`)

// Controller implements web.Controller.
type Controller struct{}

// Test runs nginx -t -q on conf.
func (Controller) Test(ctx context.Context, conf string) error {
	if !confRe.MatchString(conf) {
		return fmt.Errorf("webctl: refusing config path %q", conf)
	}

	return run(ctx, Nginx, "-t", "-q", "-c", conf)
}

// Reload asks systemd to reload nginx (the unit's ExecReload sends SIGHUP).
func (Controller) Reload(ctx context.Context) error {
	return run(ctx, Systemctl, "reload", Unit)
}

// Restart starts nginx or restarts it onto the reconciler's config.
func (Controller) Restart(ctx context.Context) error {
	return run(ctx, Systemctl, "restart", Unit)
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
			return fmt.Errorf("webctl: %s %s exited %d: %s", path, args[0], ee.ExitCode(), strings.TrimSpace(out.String()))
		}

		return fmt.Errorf("webctl: %s: %w", path, err)
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
