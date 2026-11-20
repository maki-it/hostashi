// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/firewall"
)

func startFirewallAgent(t *testing.T, m *firewall.Manager) *agentrpc.Client {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	s := NewServer(Config{Version: "t", Firewall: m}, uint32(os.Getuid()))

	dir, err := os.MkdirTemp("", "fw")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "a.sock")
	if err := s.Listen(ctx, sock, -1); err != nil {
		t.Fatal(err)
	}

	go func() { _ = s.Serve(ctx) }()

	return agentrpc.NewClient(sock)
}

func TestFirewallOps(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	base := filepath.Join(root, "firewall.nft")

	if err := os.WriteFile(base, []byte("table inet hostashi {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	reloads := 0
	m := &firewall.Manager{Dir: filepath.Join(root, "fw"), Base: base, Reload: func(context.Context) error { reloads++; return nil }}
	c := startFirewallAgent(t, m)

	state := agentapi.FirewallState{Open: []agentapi.FirewallRule{{Proto: "tcp", From: 3000}}, SSHAllow: []string{"192.0.2.0/24"}}

	st, err := agentapi.FirewallApply.Call(ctx, c, agentapi.FirewallApplyRequest{State: state})
	if err != nil || st.Pending == nil || reloads != 1 {
		t.Fatalf("apply: %+v %v (reloads %d)", st, err, reloads)
	}

	if _, err := agentapi.FirewallApply.Call(ctx, c, agentapi.FirewallApplyRequest{State: state}); code(err) != agentrpc.CodePrecondition {
		t.Errorf("second apply: %v", err)
	}

	bad := agentapi.FirewallState{Open: []agentapi.FirewallRule{{Proto: "tcp", From: 443}}}
	if _, err := agentapi.FirewallApply.Call(ctx, c, agentapi.FirewallApplyRequest{State: bad}); code(err) != agentrpc.CodeInvalidArgument {
		t.Errorf("port 443: %v", err)
	}

	if _, err := agentapi.FirewallConfirm.Call(ctx, c, agentapi.FirewallConfirmRequest{ID: "00000000000000000000000000000000"}); code(err) != agentrpc.CodeNotFound {
		t.Errorf("wrong id: %v", err)
	}

	if st, err = agentapi.FirewallConfirm.Call(ctx, c, agentapi.FirewallConfirmRequest{ID: st.Pending.ID}); err != nil || st.Pending != nil {
		t.Fatalf("confirm: %+v %v", st, err)
	}

	st, err = agentapi.FirewallStatusOp.Call(ctx, c, agentapi.FirewallStatusRequest{})
	if err != nil || !st.Enabled || len(st.State.Open) != 1 || st.State.SSHAllow[0] != "192.0.2.0/24" {
		t.Errorf("status: %+v %v", st, err)
	}
}

func TestFirewallOpsWithoutManager(t *testing.T) {
	c := startFirewallAgent(t, nil)

	if _, err := agentapi.FirewallStatusOp.Call(context.Background(), c, agentapi.FirewallStatusRequest{}); code(err) != agentrpc.CodePrecondition {
		t.Errorf("status without firewall: %v", err)
	}
}
