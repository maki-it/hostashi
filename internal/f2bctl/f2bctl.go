// SPDX-License-Identifier: AGPL-3.0-or-later

// Package f2bctl reads the Hostashi jails of fail2ban and lifts the ban of
// one address (ADR-0078).
//
// Like internal/webctl it may start a process (depguard rule in
// .golangci.yml), and the exception is as narrow: one binary by absolute
// path, three fixed argument shapes, jail names only from
// agentapi.Fail2banJails, addresses only after agentapi's validation, no
// shell, a minimal environment, a timeout and a limit on the output.
package f2bctl

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Client is fail2ban's command line client (Debian 13 and Ubuntu 26.04).
const Client = "/usr/bin/fail2ban-client"

const (
	commandTimeout = 15 * time.Second
	outputLimit    = 256 << 10
)

// ErrUnavailable is returned when fail2ban does not answer.
var ErrUnavailable = errors.New("f2bctl: fail2ban does not answer")

// errNoJail marks a jail fail2ban does not know.
var errNoJail = errors.New("f2bctl: no such jail")

// Runner runs the client with args and returns its output. Tests replace it.
type Runner func(ctx context.Context, args ...string) (string, error)

// Controller talks to fail2ban.
type Controller struct {
	// Run is the client; nil runs Client.
	Run Runner
	// Location is the time zone of fail2ban's ban times; nil is
	// time.Local, which the agent shares with fail2ban.
	Location *time.Location
}

func (c *Controller) run(ctx context.Context, args ...string) (string, error) {
	if c.Run != nil {
		return c.Run(ctx, args...)
	}

	return run(ctx, args...)
}

func (c *Controller) loc() *time.Location {
	if c.Location != nil {
		return c.Location
	}

	return time.Local
}

// Status returns every jail of agentapi.Fail2banJails. A jail fail2ban does
// not know is reported as not running; a fail2ban that does not answer is
// ErrUnavailable.
func (c *Controller) Status(ctx context.Context) (agentapi.Fail2banStatus, error) {
	if _, err := c.run(ctx, "ping"); err != nil {
		return agentapi.Fail2banStatus{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	out := agentapi.Fail2banStatus{Jails: make([]agentapi.Fail2banJail, 0, len(agentapi.Fail2banJails))}

	for _, name := range agentapi.Fail2banJails {
		j, err := c.jail(ctx, name)
		if errors.Is(err, errNoJail) {
			out.Jails = append(out.Jails, agentapi.Fail2banJail{Name: name, Bans: []agentapi.Fail2banBan{}})

			continue
		}

		if err != nil {
			return agentapi.Fail2banStatus{}, err
		}

		out.Jails = append(out.Jails, j)
	}

	return out, nil
}

func (c *Controller) jail(ctx context.Context, name string) (agentapi.Fail2banJail, error) {
	st, err := c.run(ctx, "status", name)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return agentapi.Fail2banJail{}, errNoJail
		}

		return agentapi.Fail2banJail{}, err
	}

	j, err := ParseStatus(name, st)
	if err != nil {
		return agentapi.Fail2banJail{}, err
	}

	bans, err := c.run(ctx, "get", name, "banip", "--with-time")
	if err != nil {
		return agentapi.Fail2banJail{}, err
	}

	j.Bans, err = ParseBans(bans, c.loc())
	if err != nil {
		return agentapi.Fail2banJail{}, err
	}

	return j, nil
}

// Unban lifts the ban of addr in every running Hostashi jail and returns
// the jails that had banned it. req must have passed Validate.
func (c *Controller) Unban(ctx context.Context, req agentapi.Fail2banUnbanRequest) (agentapi.Fail2banUnbanResponse, error) {
	if err := req.Validate(); err != nil {
		return agentapi.Fail2banUnbanResponse{}, err
	}

	addr := netip.MustParseAddr(req.Address).String()

	if _, err := c.run(ctx, "ping"); err != nil {
		return agentapi.Fail2banUnbanResponse{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	resp := agentapi.Fail2banUnbanResponse{Jails: []string{}}

	for _, name := range agentapi.Fail2banJails {
		out, err := c.run(ctx, "set", name, "unbanip", addr)
		if err != nil {
			if strings.Contains(err.Error(), "does not exist") {
				continue
			}

			return resp, err
		}

		// fail2ban prints how many of the given addresses it unbanned.
		n, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil {
			return resp, fmt.Errorf("f2bctl: unexpected unban answer of %s: %q", name, strings.TrimSpace(out))
		}

		if n > 0 {
			resp.Jails = append(resp.Jails, name)
		}
	}

	return resp, nil
}

// ParseStatus reads the output of "fail2ban-client status <jail>".
func ParseStatus(name, out string) (agentapi.Fail2banJail, error) {
	j := agentapi.Fail2banJail{Name: name, Running: true, Bans: []agentapi.Fail2banBan{}}
	fields := map[string]*int{
		"Currently failed": &j.CurrentlyFailed,
		"Total failed":     &j.TotalFailed,
		"Currently banned": &j.CurrentlyBanned,
		"Total banned":     &j.TotalBanned,
	}
	seen := 0

	for line := range strings.SplitSeq(out, "\n") {
		key, val, ok := strings.Cut(strings.TrimLeft(line, "|`- "), ":")
		if !ok {
			continue
		}

		p, ok := fields[key]
		if !ok {
			continue
		}

		n, err := strconv.Atoi(strings.TrimSpace(val))
		if err != nil || n < 0 {
			return agentapi.Fail2banJail{}, fmt.Errorf("f2bctl: %s of %s: %q", key, name, strings.TrimSpace(val))
		}

		*p = n
		seen++
	}

	if seen != len(fields) {
		return agentapi.Fail2banJail{}, fmt.Errorf("f2bctl: unexpected status output of %s", name)
	}

	return j, nil
}

// ParseBans reads the output of "fail2ban-client get <jail> banip
// --with-time": one line per address, "ADDR \tSTART + SECONDS = END", the
// times in fail2ban's local time. Lines with anything but an address are
// an error, so an odd answer never reaches the panel as an address.
func ParseBans(out string, loc *time.Location) ([]agentapi.Fail2banBan, error) {
	bans := []agentapi.Fail2banBan{}

	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		addrPart, rest, ok := strings.Cut(line, "\t")
		if !ok {
			return nil, fmt.Errorf("f2bctl: unexpected ban line %q", line)
		}

		addr, err := netip.ParseAddr(strings.TrimSpace(addrPart))
		if err != nil {
			return nil, fmt.Errorf("f2bctl: ban line without address: %q", line)
		}

		startPart, rest, ok1 := strings.Cut(rest, " + ")
		secsPart, _, ok2 := strings.Cut(rest, " = ")

		if !ok1 || !ok2 {
			return nil, fmt.Errorf("f2bctl: unexpected ban line %q", line)
		}

		start, err := time.ParseInLocation(time.DateTime, strings.TrimSpace(startPart), loc)
		if err != nil {
			return nil, fmt.Errorf("f2bctl: ban time in %q: %w", line, err)
		}

		secs, err := strconv.ParseInt(strings.TrimSpace(secsPart), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("f2bctl: ban duration in %q: %w", line, err)
		}

		b := agentapi.Fail2banBan{Address: addr.String(), Since: start.Unix()}
		// A negative ban time is a ban without end.
		if secs >= 0 {
			b.Until = start.Unix() + secs
		}

		bans = append(bans, b)
	}

	slices.SortStableFunc(bans, func(a, b agentapi.Fail2banBan) int { return cmp.Compare(a.Since, b.Since) })

	return bans, nil
}

func run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()

	var out bytes.Buffer

	cmd := exec.CommandContext(ctx, Client, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C"}
	cmd.Dir = "/"
	cmd.Stdout = &limitedWriter{buf: &out, max: outputLimit}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("f2bctl: fail2ban-client %s exited %d: %s", args[0], ee.ExitCode(), strings.TrimSpace(out.String()))
		}

		return "", fmt.Errorf("f2bctl: %w", err)
	}

	return out.String(), nil
}

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
