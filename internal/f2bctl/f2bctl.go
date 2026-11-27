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
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Client is fail2ban's command line client (Debian 13 and Ubuntu 26.04).
const Client = "/usr/bin/fail2ban-client"

const (
	commandTimeout = 15 * time.Second
	// outputLimit is about 65,000 bans of one jail with time; a longer
	// list is cut at a line end and reported as truncated.
	outputLimit = 4 << 20
	// cacheFor keeps one reading of fail2ban for the pages and filters
	// that follow it; every reading starts nine client processes.
	cacheFor = 5 * time.Second
)

// ErrTruncated is returned with the output that fit when a command printed
// more than the output limit. The output ends at a full line.
var ErrTruncated = errors.New("f2bctl: output truncated")

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
	// Now is the clock of the cache; nil is time.Now.
	Now func() time.Time

	mu     sync.Mutex
	cached *snapshot
	at     time.Time
}

// snapshot is one reading of every Hostashi jail.
type snapshot struct {
	jails     []agentapi.Fail2banJail
	bans      map[string][]agentapi.Fail2banBan
	truncated bool
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

// Status returns every jail of agentapi.Fail2banJails and the page of
// blocked addresses that req asks for. A jail fail2ban does not know is
// reported as not running; a fail2ban that does not answer is
// ErrUnavailable. req must have passed Validate.
func (c *Controller) Status(ctx context.Context, req agentapi.Fail2banStatusRequest) (agentapi.Fail2banStatus, error) {
	if err := req.Validate(); err != nil {
		return agentapi.Fail2banStatus{}, err
	}

	snap, err := c.read(ctx)
	if err != nil {
		return agentapi.Fail2banStatus{}, err
	}

	st := Page(snap.jails, snap.bans, req)
	st.Truncated = snap.truncated

	return st, nil
}

// read returns a reading of fail2ban not older than cacheFor.
func (c *Controller) read(ctx context.Context) (*snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	if c.cached != nil && now.Sub(c.at) >= 0 && now.Sub(c.at) < cacheFor {
		return c.cached, nil
	}

	if _, err := c.run(ctx, "ping"); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}

	snap := &snapshot{jails: make([]agentapi.Fail2banJail, 0, len(agentapi.Fail2banJails)), bans: map[string][]agentapi.Fail2banBan{}}

	for _, name := range agentapi.Fail2banJails {
		j, bans, truncated, err := c.jail(ctx, name)
		if errors.Is(err, errNoJail) {
			snap.jails = append(snap.jails, agentapi.Fail2banJail{Name: name})

			continue
		}

		if err != nil {
			return nil, err
		}

		snap.jails = append(snap.jails, j)
		snap.bans[name] = bans
		snap.truncated = snap.truncated || truncated
	}

	c.cached, c.at = snap, now

	return snap, nil
}

func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}

	return time.Now()
}

// forget drops the cached reading, after a change.
func (c *Controller) forget() {
	c.mu.Lock()
	c.cached = nil
	c.mu.Unlock()
}

// Page merges the bans of every jail per address, keeps the addresses that
// match req and returns the page req asks for, newest first.
func Page(jails []agentapi.Fail2banJail, bans map[string][]agentapi.Fail2banBan, req agentapi.Fail2banStatusRequest) agentapi.Fail2banStatus {
	by := map[string]*agentapi.Fail2banBlocked{}

	for _, name := range agentapi.Fail2banJails {
		for _, b := range bans[name] {
			cur := by[b.Address]
			if cur == nil {
				by[b.Address] = &agentapi.Fail2banBlocked{Address: b.Address, Jails: []string{name}, Since: b.Since, Until: b.Until}

				continue
			}

			if !slices.Contains(cur.Jails, name) {
				cur.Jails = append(cur.Jails, name)
			}

			cur.Since = min(cur.Since, b.Since)
			// The address stays blocked until its last ban ends; 0 is no end.
			if cur.Until != 0 && (b.Until == 0 || b.Until > cur.Until) {
				cur.Until = b.Until
			}
		}
	}

	match := addressMatcher(req.Address)
	all := make([]agentapi.Fail2banBlocked, 0, len(by))

	for _, b := range by {
		if req.Jail != "" && !slices.Contains(b.Jails, req.Jail) {
			continue
		}

		if match(b.Address) {
			all = append(all, *b)
		}
	}

	slices.SortFunc(all, func(a, b agentapi.Fail2banBlocked) int {
		if c := cmp.Compare(b.Since, a.Since); c != 0 {
			return c
		}

		return cmp.Compare(a.Address, b.Address)
	})

	limit := req.Limit
	if limit == 0 {
		limit = agentapi.Fail2banPageDefault
	}

	from := min(req.Offset, len(all))
	to := min(from+limit, len(all))
	out := agentapi.Fail2banStatus{Jails: jails, Blocked: slices.Clip(all[from:to]), Total: len(all)}

	if out.Jails == nil {
		out.Jails = []agentapi.Fail2banJail{}
	}

	return out
}

// addressMatcher reads an address filter: a range such as 203.0.113.0/24
// keeps the addresses in it; anything else keeps the addresses that
// contain it, or equal it when it is an address written another way.
func addressMatcher(q string) func(string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return func(string) bool { return true }
	}

	if p, err := netip.ParsePrefix(q); err == nil {
		p = p.Masked()

		return func(s string) bool {
			a, err := netip.ParseAddr(s)

			return err == nil && p.Contains(a)
		}
	}

	exact, exactErr := netip.ParseAddr(q)

	return func(s string) bool {
		if strings.Contains(s, q) {
			return true
		}

		a, err := netip.ParseAddr(s)

		return exactErr == nil && err == nil && a == exact
	}
}

func (c *Controller) jail(ctx context.Context, name string) (agentapi.Fail2banJail, []agentapi.Fail2banBan, bool, error) {
	st, err := c.run(ctx, "status", name)
	if err != nil {
		if strings.Contains(err.Error(), "does not exist") {
			return agentapi.Fail2banJail{}, nil, false, errNoJail
		}

		return agentapi.Fail2banJail{}, nil, false, err
	}

	j, err := ParseStatus(name, st)
	if err != nil {
		return agentapi.Fail2banJail{}, nil, false, err
	}

	out, err := c.run(ctx, "get", name, "banip", "--with-time")

	truncated := errors.Is(err, ErrTruncated)
	if err != nil && !truncated {
		return agentapi.Fail2banJail{}, nil, false, err
	}

	bans, err := ParseBans(out, c.loc())
	if err != nil {
		return agentapi.Fail2banJail{}, nil, false, err
	}

	return j, bans, truncated, nil
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

	// The next reading shows the change, also after an error halfway.
	defer c.forget()

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
	j := agentapi.Fail2banJail{Name: name, Running: true}
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
	lw := &limitedWriter{buf: &out, max: outputLimit}
	cmd.Stdout = lw
	cmd.Stderr = lw

	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return "", fmt.Errorf("f2bctl: fail2ban-client %s exited %d: %s", args[0], ee.ExitCode(), strings.TrimSpace(out.String()))
		}

		return "", fmt.Errorf("f2bctl: %w", err)
	}

	if lw.cut {
		// Drop the line the limit cut in two.
		s := out.String()

		return s[:strings.LastIndexByte(s, '\n')+1], ErrTruncated
	}

	return out.String(), nil
}

type limitedWriter struct {
	buf *bytes.Buffer
	max int
	cut bool
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	room := w.max - w.buf.Len()
	if room > 0 {
		_, _ = w.buf.Write(p[:min(room, len(p))])
	}

	if len(p) > room {
		w.cut = true
	}

	return len(p), nil
}
