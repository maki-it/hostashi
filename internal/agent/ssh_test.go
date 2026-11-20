// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/sshaccess"
	"github.com/maki-it/hostashi/internal/sshkeys"
)

type memShells map[string]string

func (m memShells) SetShell(_ context.Context, name, shell string) error {
	m[name] = shell

	return nil
}

func TestSSHWithoutManager(t *testing.T) {
	c, _ := startSiteAgent(t)

	_, err := agentapi.SSHApply.Call(context.Background(), c, agentapi.SSHApplyRequest{Site: "blog"})
	wantCode(t, err, agentrpc.CodePrecondition)

	_, err = agentapi.SSHHostKeys.Call(context.Background(), c, agentapi.SSHHostKeysRequest{})
	wantCode(t, err, agentrpc.CodePrecondition)
}

func TestSSHHostKeysOp(t *testing.T) {
	dir := t.TempDir()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "ssh_host_ed25519_key.pub"), ssh.MarshalAuthorizedKey(pk), 0o644); err != nil {
		t.Fatal(err)
	}

	c, _ := startSiteAgentCfg(t, Config{SSH: &sshaccess.Manager{HostKeyDir: dir}})

	got, err := agentapi.SSHHostKeys.Call(context.Background(), c, agentapi.SSHHostKeysRequest{})
	if err != nil || len(got.Keys) != 1 || got.Keys[0].Fingerprint != ssh.FingerprintSHA256(pk) {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestSSHOps(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root: the key directory must belong to root")
	}

	dir := t.TempDir()
	keys := filepath.Join(dir, "keys")

	if err := os.Mkdir(keys, 0o755); err != nil {
		t.Fatal(err)
	}

	shells := memShells{}
	mgr := &sshaccess.Manager{StateFile: filepath.Join(dir, "ssh.json"), KeyDir: keys, Shells: shells}
	c, _ := startSiteAgentCfg(t, Config{SSH: mgr})
	ctx := context.Background()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	key := agentapi.SSHKey{Label: "kim", Access: sshkeys.Shell, Public: strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")}

	_, err = agentapi.SSHApply.Call(ctx, c, agentapi.SSHApplyRequest{Site: "blog", Keys: []agentapi.SSHKey{key}})
	wantCode(t, err, agentrpc.CodeNotFound)

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	resp, err := agentapi.SSHApply.Call(ctx, c, agentapi.SSHApplyRequest{Site: "blog", Keys: []agentapi.SSHKey{key}})
	if err != nil || !resp.Shell || shells["blog"] != "/bin/bash" {
		t.Fatalf("apply: %+v %v %v", resp, err, shells)
	}

	b, err := os.ReadFile(filepath.Join(keys, "blog"))
	if err != nil || !strings.Contains(string(b), "restrict,pty "+key.Public+" kim\n") {
		t.Fatalf("key file %q %v", b, err)
	}

	if _, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(keys, "blog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key file after site delete: %v", err)
	}
}
