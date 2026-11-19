// SPDX-License-Identifier: AGPL-3.0-or-later

package cron

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Limits of ADR-0028.
const (
	MaxCommand     = 4096
	MaxJobsPerSite = 50
	DefaultTimeout = time.Hour
	MinTimeout     = time.Minute
	MaxTimeout     = 24 * time.Hour
)

// UnitPrefix starts the name of every unit this package renders.
const UnitPrefix = "hostashi-cron-"

// ErrCommand marks a command the validator does not accept.
var ErrCommand = errors.New("cron: invalid command")

// ValidateCommand accepts one line of at most MaxCommand bytes of UTF-8
// without control characters other than tab. The shell reads it as
// written: "%" is not turned into a newline as cron does.
func ValidateCommand(c string) error {
	switch {
	case strings.TrimSpace(c) == "":
		return fmt.Errorf("%w: empty", ErrCommand)
	case len(c) > MaxCommand:
		return fmt.Errorf("%w: longer than %d bytes", ErrCommand, MaxCommand)
	case !utf8.ValidString(c):
		return fmt.Errorf("%w: not UTF-8", ErrCommand)
	}

	for _, r := range c {
		if r != '\t' && (unicode.IsControl(r) || r == ' ' || r == ' ') {
			return fmt.Errorf("%w: control character %U (one line only)", ErrCommand, r)
		}
	}

	return nil
}

// ValidateTimeout accepts whole seconds from MinTimeout to MaxTimeout.
func ValidateTimeout(d time.Duration) error {
	if d < MinTimeout || d > MaxTimeout || d%time.Second != 0 {
		return fmt.Errorf("cron: timeout must be whole seconds from %s to %s", MinTimeout, MaxTimeout)
	}

	return nil
}

// Script is the content of the file a job's service runs.
func Script(command string) ([]byte, error) {
	if err := ValidateCommand(command); err != nil {
		return nil, err
	}

	return []byte("#!/bin/sh\n# Written by the Hostashi agent; changes are overwritten.\n" + command + "\n"), nil
}

// Unit describes the units of one job. Every field reaches unit syntax, so
// each is checked against a narrow pattern before rendering.
type Unit struct {
	Site   string // site user, also the unit's User= and Group=
	ID     int64  // job id, > 0
	Home   string // the site's home, absolute
	Script string // the job's script file, absolute
	Slice  string // optional, a slice unit name
	// Nice is the job's niceness, 0 to 19; 0 leaves it unset (ADR-0015:
	// 10 by default for cron jobs).
	Nice    int
	Timeout time.Duration
	// OnCalendar are the timer's values, from OnCalendar.
	OnCalendar []string
}

var (
	// The same characters as agentapi's site user pattern; checked again
	// here because the name ends up in unit files.
	unitSiteRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)
	unitPathRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)
	// Slice names may carry systemd-escape's \x2d for a "-" in a site user
	// name (ADR-0015: hostashi-site-<escaped user>.slice).
	unitSliceRe = regexp.MustCompile(`^(?:[A-Za-z0-9_-]|\\x2d)+\.slice$`)
	calendarRe  = regexp.MustCompile(`^[0-9A-Za-z*:,. -]+$`)
)

// Name is the unit name without suffix: hostashi-cron-<site>-<id>.
func (u Unit) Name() string { return UnitPrefix + u.Site + "-" + strconv.FormatInt(u.ID, 10) }

func (u Unit) check() error {
	if !unitSiteRe.MatchString(u.Site) || u.Site == "root" {
		return fmt.Errorf("cron: site %q", u.Site)
	}

	if u.ID < 1 {
		return fmt.Errorf("cron: job id %d", u.ID)
	}

	for _, p := range []string{u.Home, u.Script} {
		if !unitPathRe.MatchString(p) || path.Clean(p) != p || p == "/" {
			return fmt.Errorf("cron: path %q", p)
		}
	}

	if u.Slice != "" && !unitSliceRe.MatchString(u.Slice) {
		return fmt.Errorf("cron: slice %q", u.Slice)
	}

	if u.Nice < 0 || u.Nice > 19 {
		return fmt.Errorf("cron: nice %d", u.Nice)
	}

	if err := ValidateTimeout(u.Timeout); err != nil {
		return err
	}

	if len(u.OnCalendar) < 1 || len(u.OnCalendar) > 2 {
		return fmt.Errorf("cron: %d OnCalendar values", len(u.OnCalendar))
	}

	for _, c := range u.OnCalendar {
		if !calendarRe.MatchString(c) {
			return fmt.Errorf("cron: OnCalendar %q", c)
		}
	}

	return nil
}

// Service renders the job's service unit. It runs the script with /bin/sh
// as the site user, sandboxed like the PHP masters, with only the home
// writable.
func (u Unit) Service() ([]byte, error) {
	if err := u.check(); err != nil {
		return nil, err
	}

	var b strings.Builder

	w := func(s ...string) { b.WriteString(strings.Join(s, "") + "\n") }

	w("# Written by the Hostashi agent (ADR-0028); changes are overwritten.")
	w("[Unit]")
	w("Description=Hostashi cron job ", strconv.FormatInt(u.ID, 10), " of site ", u.Site)
	w("Documentation=https://github.com/maki-it/hostashi")
	w("ConditionPathExists=", u.Script)
	w("")
	w("[Service]")
	w("Type=oneshot")
	w("User=", u.Site)
	w("Group=", u.Site)
	w("ExecStart=/bin/sh ", u.Script)
	w("WorkingDirectory=", u.Home)
	w("Environment=HOME=", u.Home, " USER=", u.Site, " LOGNAME=", u.Site, " SHELL=/bin/sh PATH=/usr/local/bin:/usr/bin:/bin")
	w("TimeoutStartSec=", strconv.FormatInt(int64(u.Timeout/time.Second), 10), "s")

	if u.Slice != "" {
		w("Slice=", u.Slice)
	}

	if u.Nice > 0 {
		w("Nice=", strconv.Itoa(u.Nice))
	}

	w("SyslogIdentifier=", u.Name())
	w("")
	w("NoNewPrivileges=yes")
	w("PrivateTmp=yes")
	w("PrivateDevices=yes")
	w("ProtectSystem=strict")
	// Other homes below /home disappear; the site's own is mounted back.
	w("ProtectHome=tmpfs")
	w("BindPaths=", u.Home)
	w("ReadWritePaths=", u.Home)
	w("ProtectKernelTunables=yes")
	w("ProtectKernelModules=yes")
	w("ProtectKernelLogs=yes")
	w("ProtectControlGroups=yes")
	w("ProtectClock=yes")
	w("ProtectHostname=yes")
	w("RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6")
	w("RestrictNamespaces=yes")
	w("RestrictRealtime=yes")
	w("RestrictSUIDSGID=yes")
	w("LockPersonality=yes")
	w("SystemCallArchitectures=native")

	return []byte(b.String()), nil
}

// Timer renders the job's timer unit: exact minute, no catch-up of runs
// missed while the server was off, as cron.
func (u Unit) Timer() ([]byte, error) {
	if err := u.check(); err != nil {
		return nil, err
	}

	var b strings.Builder

	w := func(s ...string) { b.WriteString(strings.Join(s, "") + "\n") }

	w("# Written by the Hostashi agent (ADR-0028); changes are overwritten.")
	w("[Unit]")
	w("Description=Schedule of Hostashi cron job ", strconv.FormatInt(u.ID, 10), " of site ", u.Site)
	w("Documentation=https://github.com/maki-it/hostashi")
	w("")
	w("[Timer]")

	for _, c := range u.OnCalendar {
		w("OnCalendar=", c)
	}

	w("AccuracySec=1s")
	w("RandomizedDelaySec=0")
	w("Persistent=false")
	w("Unit=", u.Name(), ".service")
	w("")
	w("[Install]")
	w("WantedBy=timers.target")

	return []byte(b.String()), nil
}
