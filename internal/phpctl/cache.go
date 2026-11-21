// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// The sites' object caches (ADR-0045) go through the same exception as the
// PHP units: systemctl by absolute path, fixed verbs, and unit names that
// match cacheUnitRe. The agent never runs valkey-server or valkey-cli
// itself; systemd starts the server from the fixed template.
var (
	cacheUnitRe = regexp.MustCompile(`^hostashi-valkey@[a-z][a-z0-9_-]{1,31}\.service$`)
	cachePropRe = regexp.MustCompile(`^(?:ActiveState|SubState|MemoryCurrent|MainPID)$`)
)

// CacheController implements objcache.Controller.
type CacheController struct{}

// DaemonReload makes systemd read changed drop-ins.
func (CacheController) DaemonReload(ctx context.Context) error {
	return run(ctx, Systemctl, "daemon-reload")
}

// Start enables the unit and starts it now.
func (CacheController) Start(ctx context.Context, unit string) error {
	return cachectl(ctx, unit, "enable", "--now")
}

// Stop stops the unit now and disables it.
func (CacheController) Stop(ctx context.Context, unit string) error {
	return cachectl(ctx, unit, "disable", "--now")
}

// Restart restarts the unit, which empties the cache.
func (CacheController) Restart(ctx context.Context, unit string) error {
	return cachectl(ctx, unit, "restart")
}

// Show returns properties of a cache unit.
func (CacheController) Show(ctx context.Context, unit string, props ...string) (map[string]string, error) {
	if !cacheUnitRe.MatchString(unit) {
		return nil, fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	args := []string{"show"}

	for _, p := range props {
		if !cachePropRe.MatchString(p) {
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

func cachectl(ctx context.Context, unit string, args ...string) error {
	if !cacheUnitRe.MatchString(unit) {
		return fmt.Errorf("phpctl: refusing unit name %q", unit)
	}

	return run(ctx, Systemctl, append(args, unit)...)
}
