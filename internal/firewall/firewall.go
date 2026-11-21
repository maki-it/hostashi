// SPDX-License-Identifier: AGPL-3.0-or-later

// Package firewall keeps the panel's part of Hostashi's nftables table
// (ADR-0034). The installer's base file (/etc/hostashi/firewall.nft)
// declares the table, the sets ssh_ports and panel_ports, an empty chain
// panel that its input chain jumps to, and includes PanelFile. This package
// renders PanelFile from typed values only and reloads the firewall unit,
// which loads base and panel file in one nft transaction: a file that does
// not load changes nothing.
//
// Every change is pending: the previous state is kept and restored unless
// Confirm is called within agentapi.FirewallConfirmWithin. A pending
// change survives a restart of the agent (Recover).
package firewall

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/statefile"
)

// Paths on a server.
const (
	DefaultDir = "/var/lib/hostashi-agent/firewall"
	BaseFile   = "/etc/hostashi/firewall.nft"
	PanelFile  = "panel.nft"
	StateFile  = "state.json"
)

// Errors of the package.
var (
	ErrDisabled = errors.New("firewall: Hostashi's firewall is off (installed with --no-firewall)")
	ErrPending  = errors.New("firewall: another change is waiting for its confirmation")
	ErrNoChange = errors.New("firewall: no change is pending under this id")
)

// Reloader makes the firewall unit load the files again.
type Reloader func(ctx context.Context) error

// Manager keeps the state and the panel file.
type Manager struct {
	Dir    string // DefaultDir on a server
	Base   string // BaseFile on a server
	Reload Reloader
	Now    func() time.Time
	Log    *slog.Logger

	mu         sync.Mutex
	timer      *time.Timer
	rolledBack int64
	// panel applies the panel allowlist outside the packet filter (nginx
	// on port 443, ADR-0042); nil for none. See SetPanelHook.
	panel PanelHook
}

// PanelHook applies the panel allowlist of a new live state. An error
// counts like rules that do not load: the change is undone.
type PanelHook func(ctx context.Context, allow []string) error

// SetPanelHook installs h and calls it once with the live allowlist, so
// that it starts in step with the firewall.
func (m *Manager) SetPanelHook(ctx context.Context, h PanelHook) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.panel = h
	if h == nil {
		return nil
	}

	// Without the firewall there is no allowlist.
	if !m.Enabled() {
		return h(ctx, nil)
	}

	st, err := m.load()
	if err != nil {
		return err
	}

	return h(ctx, st.Live.PanelAllow)
}

// state is StateFile: the live state, and while a change is pending the
// state to go back to.
type state struct {
	Live     agentapi.FirewallState    `json:"live"`
	Previous *agentapi.FirewallState   `json:"previous,omitempty"`
	Pending  *agentapi.FirewallPending `json:"pending,omitempty"`
}

func (m *Manager) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}

	return m.Now()
}

func (m *Manager) log() *slog.Logger {
	if m.Log == nil {
		return slog.New(slog.DiscardHandler)
	}

	return m.Log
}

func owner() uint32 { return uint32(os.Geteuid()) } //nolint:gosec // uids fit in uint32

// Enabled reports whether the installer's base file is there.
func (m *Manager) Enabled() bool {
	_, err := os.Stat(m.Base)

	return err == nil
}

func (m *Manager) load() (state, error) {
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return state{}, fmt.Errorf("firewall: %w", err)
	}

	if err := statefile.CheckDir(m.Dir, owner()); err != nil {
		return state{}, err
	}

	raw, err := statefile.Read(m.Dir, StateFile, owner(), 1<<20)
	if errors.Is(err, os.ErrNotExist) {
		return state{Live: empty()}, nil
	}

	if err != nil {
		return state{}, err
	}

	var st state
	if err := json.Unmarshal(raw, &st); err != nil {
		return state{}, fmt.Errorf("firewall: %s: %w", StateFile, err)
	}

	// The file is ours, but it is rendered into rules: check it again.
	if err := st.Live.Validate(); err != nil {
		return state{}, fmt.Errorf("firewall: %s: %w", StateFile, err)
	}

	if st.Previous != nil {
		if err := st.Previous.Validate(); err != nil {
			return state{}, fmt.Errorf("firewall: %s: %w", StateFile, err)
		}
	}

	return st, nil
}

func empty() agentapi.FirewallState {
	return agentapi.FirewallState{Open: []agentapi.FirewallRule{}, SSHAllow: []string{}, PanelAllow: []string{}}
}

func (m *Manager) save(st state) error {
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	return statefile.Write(m.Dir, StateFile, append(b, '\n'))
}

// write writes the panel file for s, reloads the firewall and hands the
// panel allowlist to the panel hook. Called with mu held.
func (m *Manager) write(ctx context.Context, s agentapi.FirewallState) error {
	if err := statefile.Write(m.Dir, PanelFile, []byte(Render(s))); err != nil {
		return err
	}

	if err := m.Reload(ctx); err != nil {
		return err
	}

	if m.panel != nil {
		return m.panel(ctx, s.PanelAllow)
	}

	return nil
}

func (m *Manager) status(st state) agentapi.FirewallStatus {
	return agentapi.FirewallStatus{Enabled: m.Enabled(), State: st.Live, Pending: st.Pending, RolledBack: m.rolledBack}
}

// Status reports the live state.
func (m *Manager) Status() (agentapi.FirewallStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return agentapi.FirewallStatus{}, err
	}

	return m.status(st), nil
}

func newID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("firewall: %w", err)
	}

	return hex.EncodeToString(b), nil
}

// Apply makes s the live state, pending until Confirm. When the firewall
// does not load it, the previous rules stay and Apply fails.
func (m *Manager) Apply(ctx context.Context, s agentapi.FirewallState) (agentapi.FirewallStatus, error) {
	if err := s.Validate(); err != nil {
		return agentapi.FirewallStatus{}, err
	}

	if !m.Enabled() {
		return agentapi.FirewallStatus{}, ErrDisabled
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return agentapi.FirewallStatus{}, err
	}

	if st.Pending != nil {
		return agentapi.FirewallStatus{}, ErrPending
	}

	id, err := newID()
	if err != nil {
		return agentapi.FirewallStatus{}, err
	}

	s = normalize(s)
	prev := st.Live
	next := state{
		Live: s, Previous: &prev,
		Pending: &agentapi.FirewallPending{ID: id, Deadline: m.now().Add(agentapi.FirewallConfirmWithin * time.Second).Unix()},
	}

	// The state first: if the agent dies after the reload, Recover finds
	// the pending change and rolls it back in time.
	if err := m.save(next); err != nil {
		return agentapi.FirewallStatus{}, err
	}

	if err := m.write(ctx, s); err != nil {
		m.log().ErrorContext(ctx, "firewall change does not load, keeping the previous rules", "err", err)

		if rerr := m.restore(ctx, prev); rerr != nil {
			return agentapi.FirewallStatus{}, errors.Join(err, rerr)
		}

		return agentapi.FirewallStatus{}, fmt.Errorf("firewall: the rules do not load, nothing changed: %w", err)
	}

	m.schedule(ctx, next.Pending)
	m.log().InfoContext(ctx, "firewall change pending", "id", id, "deadline", next.Pending.Deadline)

	return m.status(next), nil
}

// restore makes s live again without a pending change. Called with mu held.
func (m *Manager) restore(ctx context.Context, s agentapi.FirewallState) error {
	if err := m.save(state{Live: s}); err != nil {
		return err
	}

	return m.write(ctx, s)
}

// Confirm keeps the pending change with id.
func (m *Manager) Confirm(ctx context.Context, id string) (agentapi.FirewallStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return agentapi.FirewallStatus{}, err
	}

	if st.Pending == nil || st.Pending.ID != id {
		return agentapi.FirewallStatus{}, ErrNoChange
	}

	// Too late: the timer is about to fire; roll back now.
	if m.now().Unix() > st.Pending.Deadline {
		m.rollback(ctx, st)

		return agentapi.FirewallStatus{}, ErrNoChange
	}

	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}

	st = state{Live: st.Live}
	if err := m.save(st); err != nil {
		return agentapi.FirewallStatus{}, err
	}

	m.log().InfoContext(ctx, "firewall change confirmed", "id", id)

	return m.status(st), nil
}

// rollback restores the previous state of a pending change. Called with mu
// held.
func (m *Manager) rollback(ctx context.Context, st state) {
	if st.Pending == nil {
		return
	}

	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}

	prev := empty()
	if st.Previous != nil {
		prev = *st.Previous
	}

	if err := m.restore(ctx, prev); err != nil {
		m.log().ErrorContext(ctx, "firewall rollback failed", "id", st.Pending.ID, "err", err)

		return
	}

	m.rolledBack = m.now().Unix()
	m.log().WarnContext(ctx, "firewall change not confirmed in time, rolled back", "id", st.Pending.ID)
}

// schedule arms the rollback of p. The timer outlives the request, so it
// keeps ctx's values but not its cancellation. Called with mu held.
func (m *Manager) schedule(ctx context.Context, p *agentapi.FirewallPending) {
	if m.timer != nil {
		m.timer.Stop()
	}

	wait := time.Unix(p.Deadline, 0).Sub(m.now()) + time.Second
	id := p.ID

	ctx = context.WithoutCancel(ctx)
	m.timer = time.AfterFunc(max(wait, 0), func() { m.expire(ctx, id) })
}

// expire rolls back the change id if it is still pending.
func (m *Manager) expire(ctx context.Context, id string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		m.log().ErrorContext(ctx, "firewall rollback: cannot read the state", "err", err)

		return
	}

	if st.Pending == nil || st.Pending.ID != id {
		return
	}

	m.rollback(ctx, st)
}

// Recover runs at the agent's start: it rolls back a change whose deadline
// passed while the agent was down, arms the timer of one still pending, and
// writes the panel file again when it does not match the state.
func (m *Manager) Recover(ctx context.Context) error {
	if !m.Enabled() {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	st, err := m.load()
	if err != nil {
		return err
	}

	if st.Pending != nil {
		if m.now().Unix() > st.Pending.Deadline {
			m.rollback(ctx, st)

			return nil
		}

		m.schedule(ctx, st.Pending)
	}

	want := Render(st.Live)

	got, err := os.ReadFile(filepath.Join(m.Dir, PanelFile))
	if err == nil && string(got) == want {
		return nil
	}

	return m.write(ctx, st.Live)
}

// normalize sorts nothing but makes the lists non-nil and writes every
// address as a masked prefix, so the state reads back as it is rendered.
func normalize(s agentapi.FirewallState) agentapi.FirewallState {
	out := empty()

	for _, r := range s.Open {
		if r.To == r.From {
			r.To = 0
		}

		r.Sources = prefixes(r.Sources)
		out.Open = append(out.Open, r)
	}

	out.SSHAllow = prefixes(s.SSHAllow)
	out.PanelAllow = prefixes(s.PanelAllow)

	return out
}

func prefixes(list []string) []string {
	out := make([]string, 0, len(list))

	for _, s := range list {
		p, _ := agentapi.ParsePrefix(s) // validated before

		if p.IsSingleIP() {
			out = append(out, p.Addr().String())
		} else {
			out = append(out, p.String())
		}
	}

	return out
}

func split(list []string) (v4, v6 []string) {
	for _, s := range list {
		p, err := agentapi.ParsePrefix(s)
		if err != nil {
			continue // validated before
		}

		v := p.String()
		if p.IsSingleIP() {
			v = p.Addr().String()
		}

		if p.Addr().Is4() {
			v4 = append(v4, v)
		} else {
			v6 = append(v6, v)
		}
	}

	return v4, v6
}

func set(name, typ string, elems []string) string {
	s := "\tset " + name + " {\n\t\ttype " + typ + "\n\t\tflags interval\n"
	if len(elems) > 0 {
		s += "\t\telements = { " + strings.Join(elems, ", ") + " }\n"
	}

	return s + "\t}\n"
}

// Render returns the panel file for s. Only validated, typed values reach
// it: protocols from a fixed list, port numbers, and addresses that
// net/netip parsed and printed again.
func Render(s agentapi.FirewallState) string {
	var b strings.Builder

	b.WriteString("# Managed by the Hostashi agent from the panel (ADR-0034). Do not edit:\n")
	b.WriteString("# the next change from the panel replaces this file.\n\n")
	b.WriteString("table inet hostashi {\n")

	var rules []string

	for _, l := range []struct {
		name, ports, l4 string
		list            []string
	}{
		{"ssh_allow", "@ssh_ports", "tcp", s.SSHAllow},
		{"panel_allow", "@panel_ports", "meta l4proto { tcp, udp }", s.PanelAllow},
	} {
		if len(l.list) == 0 {
			continue
		}

		v4, v6 := split(l.list)
		b.WriteString(set(l.name+"4", "ipv4_addr", v4))
		b.WriteString(set(l.name+"6", "ipv6_addr", v6))

		dport := "tcp dport"
		if l.l4 != "tcp" {
			dport = l.l4 + " th dport"
		}

		rules = append(rules,
			fmt.Sprintf("%s %s ip saddr != @%s4 drop", dport, l.ports, l.name),
			fmt.Sprintf("%s %s ip6 saddr != @%s6 drop", dport, l.ports, l.name))
	}

	for _, r := range s.Open {
		ports := strconv.Itoa(r.From)
		if r.Last() != r.From {
			ports += "-" + strconv.Itoa(r.Last())
		}

		match := r.Proto + " dport " + ports
		if len(r.Sources) == 0 {
			rules = append(rules, match+" accept")

			continue
		}

		v4, v6 := split(r.Sources)
		if len(v4) > 0 {
			rules = append(rules, "ip saddr { "+strings.Join(v4, ", ")+" } "+match+" accept")
		}

		if len(v6) > 0 {
			rules = append(rules, "ip6 saddr { "+strings.Join(v6, ", ")+" } "+match+" accept")
		}
	}

	b.WriteString("\tchain panel {\n")

	for _, r := range rules {
		b.WriteString("\t\t" + r + "\n")
	}

	b.WriteString("\t}\n}\n")

	return b.String()
}
