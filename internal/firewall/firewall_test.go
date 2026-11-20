// SPDX-License-Identifier: AGPL-3.0-or-later

package firewall

import (
	"context"
	"errors"
	"flag"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

var update = flag.Bool("update", false, "rewrite testdata/example.nft")

// Example is the state of testdata/example.nft, which
// packaging/test/firewall-test.sh loads into a real nftables table. The
// test container's host is in 172.16.0.0/12, so from there SSH is closed
// (allowlist without it), the panel is open (allowlist with it), port 3000
// is open to everyone, 5000 only to an address that is not the host, and
// 6000-6001 to the host.
var Example = agentapi.FirewallState{
	Open: []agentapi.FirewallRule{
		{Proto: "tcp", From: 3000, Note: "app"},
		{Proto: "tcp", From: 5000, Sources: []string{"203.0.113.9", "2001:db8::/32"}},
		{Proto: "tcp", From: 6000, To: 6001, Sources: []string{"172.16.0.0/12"}},
		{Proto: "udp", From: 5353},
	},
	SSHAllow:   []string{"198.51.100.0/24", "2001:db8:1::1"},
	PanelAllow: []string{"172.16.0.0/12", "192.168.0.0/16", "10.0.0.0/8"},
}

func TestRenderExample(t *testing.T) {
	got := Render(normalize(Example))
	golden := filepath.Join("testdata", "example.nft")

	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if got != string(want) {
		t.Errorf("Render differs from %s (go test -run TestRenderExample -update):\n%s", golden, got)
	}
}

func TestRenderEmpty(t *testing.T) {
	got := Render(empty())
	if strings.Contains(got, "set ") || strings.Count(got, "\t\t") != 0 || !strings.Contains(got, "chain panel {\n\t}") {
		t.Errorf("empty state:\n%s", got)
	}
}

func TestRenderOnlyIPv6Allowlist(t *testing.T) {
	got := Render(normalize(agentapi.FirewallState{SSHAllow: []string{"2001:db8::1"}}))

	// Every IPv4 address is outside the list, so the IPv4 set is empty and
	// IPv4 SSH is dropped.
	if !strings.Contains(got, "set ssh_allow4 {\n\t\ttype ipv4_addr\n\t\tflags interval\n\t}") ||
		!strings.Contains(got, "tcp dport @ssh_ports ip saddr != @ssh_allow4 drop") {
		t.Errorf("IPv6-only allowlist:\n%s", got)
	}
}

func TestValidate(t *testing.T) {
	ok := []agentapi.FirewallState{
		{},
		Example,
		{Open: []agentapi.FirewallRule{{Proto: "udp", From: 1, To: 79}, {Proto: "tcp", From: 444, To: 65535}}},
	}

	for _, s := range ok {
		if err := s.Validate(); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}

	rule := func(r agentapi.FirewallRule) agentapi.FirewallState {
		return agentapi.FirewallState{Open: []agentapi.FirewallRule{r}}
	}

	many := make([]agentapi.FirewallRule, agentapi.MaxFirewallRules+1)
	for i := range many {
		many[i] = agentapi.FirewallRule{Proto: "tcp", From: 1000 + i}
	}

	addrs := make([]string, agentapi.MaxFirewallAddresses+1)
	for i := range addrs {
		addrs[i] = netip.AddrFrom4([4]byte{10, 0, byte(i >> 8), byte(i)}).String()
	}

	for name, s := range map[string]agentapi.FirewallState{
		"icmp":           rule(agentapi.FirewallRule{Proto: "icmp", From: 1}),
		"port 0":         rule(agentapi.FirewallRule{Proto: "tcp", From: 0}),
		"port 65536":     rule(agentapi.FirewallRule{Proto: "tcp", From: 65536}),
		"backwards":      rule(agentapi.FirewallRule{Proto: "tcp", From: 3000, To: 2000}),
		"80":             rule(agentapi.FirewallRule{Proto: "tcp", From: 80}),
		"range over 443": rule(agentapi.FirewallRule{Proto: "udp", From: 400, To: 500}),
		"bad source":     rule(agentapi.FirewallRule{Proto: "tcp", From: 3000, Sources: []string{"example.com"}}),
		"injected":       rule(agentapi.FirewallRule{Proto: "tcp", From: 3000, Sources: []string{"1.2.3.4 } accept; #"}}),
		"zone":           rule(agentapi.FirewallRule{Proto: "tcp", From: 3000, Sources: []string{"fe80::1%eth0"}}),
		"mapped":         rule(agentapi.FirewallRule{Proto: "tcp", From: 3000, Sources: []string{"::ffff:192.0.2.1"}}),
		"note":           rule(agentapi.FirewallRule{Proto: "tcp", From: 3000, Note: "a\nb"}),
		"all v4":         {SSHAllow: []string{"0.0.0.0/0"}},
		"twice":          {PanelAllow: []string{"192.0.2.1", "192.0.2.1/32"}},
		"too many rules": {Open: many},
		"too many addrs": {SSHAllow: addrs},
	} {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestAllowed(t *testing.T) {
	list := []string{"192.0.2.0/24", "2001:db8::1"}

	for addr, want := range map[string]bool{
		"192.0.2.77":       true,
		"192.0.3.1":        false,
		"2001:db8::1":      true,
		"2001:db8::2":      false,
		"127.0.0.1":        true,
		"::1":              true,
		"::ffff:192.0.2.5": true,
	} {
		if got := agentapi.FirewallAllowed(list, netip.MustParseAddr(addr)); got != want {
			t.Errorf("%s: %v", addr, got)
		}
	}

	if !agentapi.FirewallAllowed(nil, netip.MustParseAddr("203.0.113.1")) {
		t.Error("empty list must allow everyone")
	}
}

// fakeClock and fakeReload let the tests drive time and the unit.
type fakeReload struct {
	mu    sync.Mutex
	calls int
	fail  bool
	files []string
	dir   string
}

func (f *fakeReload) reload(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++

	b, _ := os.ReadFile(filepath.Join(f.dir, PanelFile))
	f.files = append(f.files, string(b))

	if f.fail {
		return errors.New("nft: syntax error")
	}

	return nil
}

func (f *fakeReload) last() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.files) == 0 {
		return ""
	}

	return f.files[len(f.files)-1]
}

func newManager(t *testing.T) (*Manager, *fakeReload, *time.Time) {
	t.Helper()

	root := t.TempDir()
	base := filepath.Join(root, "firewall.nft")

	if err := os.WriteFile(base, []byte("table inet hostashi {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "fw")
	now := time.Unix(1_800_000_000, 0)
	fr := &fakeReload{dir: dir}

	return &Manager{Dir: dir, Base: base, Reload: fr.reload, Now: func() time.Time { return now }}, fr, &now
}

func TestApplyConfirm(t *testing.T) {
	ctx := context.Background()
	m, fr, _ := newManager(t)

	st, err := m.Apply(ctx, Example)
	if err != nil {
		t.Fatal(err)
	}

	if st.Pending == nil || st.Pending.Deadline != 1_800_000_000+agentapi.FirewallConfirmWithin || !st.Enabled {
		t.Fatalf("pending = %+v", st)
	}

	if fr.last() != Render(normalize(Example)) {
		t.Errorf("loaded:\n%s", fr.last())
	}

	if _, err := m.Apply(ctx, agentapi.FirewallState{}); !errors.Is(err, ErrPending) {
		t.Errorf("second change while pending: %v", err)
	}

	if _, err := m.Confirm(ctx, strings.Repeat("0", 32)); !errors.Is(err, ErrNoChange) {
		t.Errorf("wrong id: %v", err)
	}

	st, err = m.Confirm(ctx, st.Pending.ID)
	if err != nil || st.Pending != nil {
		t.Fatalf("confirm: %+v %v", st, err)
	}

	// The confirmed state survives a restart and nothing rolls back.
	m2 := &Manager{Dir: m.Dir, Base: m.Base, Reload: fr.reload, Now: m.Now}
	if err := m2.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	st, _ = m2.Status()
	if len(st.State.Open) != 4 || st.State.Open[0].From != 3000 || st.State.SSHAllow[0] != "198.51.100.0/24" || st.RolledBack != 0 {
		t.Errorf("after restart: %+v", st)
	}

	if fr.calls != 1 {
		t.Errorf("reloads = %d, want 1 (the file matched at the restart)", fr.calls)
	}
}

func TestRollbackByTimer(t *testing.T) {
	ctx := context.Background()
	m, fr, _ := newManager(t)

	st, err := m.Apply(ctx, Example)
	if err != nil {
		t.Fatal(err)
	}

	// The timer runs the rollback; fire it now instead of after 121 s.
	m.mu.Lock()
	m.timer.Stop()
	m.mu.Unlock()
	m.expire(ctx, st.Pending.ID)

	st, _ = m.Status()
	if st.Pending != nil || len(st.State.Open) != 0 || st.RolledBack == 0 {
		t.Errorf("after rollback: %+v", st)
	}

	if fr.last() != Render(empty()) {
		t.Errorf("rolled back to:\n%s", fr.last())
	}

	if _, err := m.Confirm(ctx, strings.Repeat("a", 32)); !errors.Is(err, ErrNoChange) {
		t.Errorf("confirm after rollback: %v", err)
	}
}

func TestConfirmTooLate(t *testing.T) {
	ctx := context.Background()
	m, _, now := newManager(t)

	st, err := m.Apply(ctx, Example)
	if err != nil {
		t.Fatal(err)
	}

	*now = now.Add((agentapi.FirewallConfirmWithin + 1) * time.Second)

	if _, err := m.Confirm(ctx, st.Pending.ID); !errors.Is(err, ErrNoChange) {
		t.Errorf("late confirm: %v", err)
	}

	if st, _ := m.Status(); st.Pending != nil || len(st.State.Open) != 0 {
		t.Errorf("late confirm kept the change: %+v", st)
	}
}

func TestRecoverRollsBackAfterRestart(t *testing.T) {
	ctx := context.Background()
	m, fr, now := newManager(t)

	if _, err := m.Apply(ctx, Example); err != nil {
		t.Fatal(err)
	}

	m.timer.Stop() // the agent died

	*now = now.Add(10 * time.Minute)
	m2 := &Manager{Dir: m.Dir, Base: m.Base, Reload: fr.reload, Now: m.Now}

	if err := m2.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if st, _ := m2.Status(); st.Pending != nil || len(st.State.Open) != 0 || fr.last() != Render(empty()) {
		t.Errorf("after restart past the deadline: %+v", st)
	}
}

func TestRecoverKeepsPendingTimer(t *testing.T) {
	ctx := context.Background()
	m, fr, _ := newManager(t)

	st, err := m.Apply(ctx, Example)
	if err != nil {
		t.Fatal(err)
	}

	m.timer.Stop()

	m2 := &Manager{Dir: m.Dir, Base: m.Base, Reload: fr.reload, Now: m.Now}
	if err := m2.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	if m2.timer == nil {
		t.Fatal("no timer for the pending change")
	}

	if _, err := m2.Confirm(ctx, st.Pending.ID); err != nil {
		t.Errorf("confirm after restart: %v", err)
	}
}

func TestApplyThatDoesNotLoad(t *testing.T) {
	ctx := context.Background()
	m, fr, _ := newManager(t)
	fr.fail = true

	if _, err := m.Apply(ctx, Example); err == nil {
		t.Fatal("accepted")
	}

	fr.fail = false

	st, _ := m.Status()
	if st.Pending != nil || len(st.State.Open) != 0 || fr.last() != Render(empty()) {
		t.Errorf("after a failed load: %+v\n%s", st, fr.last())
	}
}

func TestDisabled(t *testing.T) {
	m, _, _ := newManager(t)
	if err := os.Remove(m.Base); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Apply(context.Background(), Example); !errors.Is(err, ErrDisabled) {
		t.Errorf("apply without base file: %v", err)
	}

	if st, err := m.Status(); err != nil || st.Enabled {
		t.Errorf("status: %+v %v", st, err)
	}
}
