// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/sshkeys"
)

type fakeSSH struct {
	applied map[string][]agentapi.SSHKey
	fail    error
}

func (f *fakeSSH) SSHApply(_ context.Context, req agentapi.SSHApplyRequest) (agentapi.SSHApplied, error) {
	if f.fail != nil {
		return agentapi.SSHApplied{}, f.fail
	}

	if err := req.Validate(); err != nil {
		return agentapi.SSHApplied{}, err
	}

	f.applied[req.Site] = req.Keys

	return agentapi.SSHApplied{Shell: sshkeys.NeedsShell(req.KeyList())}, nil
}

func newEdKey(t *testing.T) string {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}

	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")
}

func TestSSHKeys(t *testing.T) {
	s, _, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	pub := newEdKey(t)
	in := SSHKeyInput{Label: "kim laptop", Public: pub + " kim@laptop"}

	if _, err := s.CreateSSHKey(ctx, 1, "shop", in); !errors.Is(err, ErrNoSSH) {
		t.Fatalf("without agent: %v", err)
	}

	ag := &fakeSSH{applied: map[string][]agentapi.SSHKey{}}
	s.EnableSSH(ag)

	k, err := s.CreateSSHKey(ctx, 1, "shop", in)
	if err != nil {
		t.Fatal(err)
	}

	// SFTP only by default; the comment is dropped, the label kept.
	if k.Access != "sftp" || k.Public != pub || !strings.HasPrefix(k.Fingerprint, "SHA256:") {
		t.Fatalf("key %+v", k)
	}

	if got := ag.applied["shop"]; len(got) != 1 || got[0].Public != pub || got[0].Access != sshkeys.SFTP || got[0].Label != "kim laptop" {
		t.Fatalf("applied %+v", got)
	}

	if _, err := s.CreateSSHKey(ctx, 1, "shop", in); !errors.Is(err, ErrSSHKeyExists) {
		t.Fatalf("duplicate: %v", err)
	}

	for name, bad := range map[string]SSHKeyInput{
		"options": {Label: "x", Public: `command="sh" ` + pub},
		"garbage": {Label: "x", Public: "not a key"},
		"label":   {Label: "a\nb", Public: newEdKey(t)},
		"access":  {Label: "x", Access: "root", Public: newEdKey(t)},
	} {
		if _, err := s.CreateSSHKey(ctx, 1, "shop", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if _, err := s.CreateSSHKey(ctx, 1, "nope", SSHKeyInput{Label: "x", Public: newEdKey(t)}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown site: %v", err)
	}

	// A refused apply leaves the database as it was.
	ag.fail = errDown

	if _, err := s.CreateSSHKey(ctx, 1, "shop", SSHKeyInput{Label: "x", Public: newEdKey(t)}); !errors.Is(err, errDown) {
		t.Fatalf("failed create: %v", err)
	}

	shell := SSHKeyChange{Label: "kim", Access: sshkeys.Shell}

	if _, err := s.UpdateSSHKey(ctx, 1, "shop", k.ID, shell); !errors.Is(err, errDown) {
		t.Fatalf("failed update: %v", err)
	}

	if err := s.DeleteSSHKey(ctx, 1, "shop", k.ID); !errors.Is(err, errDown) {
		t.Fatalf("failed delete: %v", err)
	}

	keys, err := s.SSHKeys(ctx, "shop")
	if err != nil || len(keys) != 1 || keys[0].Access != "sftp" || keys[0].Label != "kim laptop" {
		t.Fatalf("after failures %+v %v", keys, err)
	}

	ag.fail = nil

	got, err := s.UpdateSSHKey(ctx, 1, "shop", k.ID, shell)
	if err != nil || got.Access != "shell" || got.Label != "kim" || ag.applied["shop"][0].Access != sshkeys.Shell {
		t.Fatalf("update %+v %v", got, err)
	}

	if _, err := s.UpdateSSHKey(ctx, 1, "shop", k.ID, SSHKeyChange{Label: "kim", Access: "all"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad access: %v", err)
	}

	if _, err := s.UpdateSSHKey(ctx, 1, "shop", k.ID+99, shell); !errors.Is(err, ErrSSHKeyNotFound) {
		t.Fatalf("unknown key: %v", err)
	}

	if _, err := s.SSHKey(ctx, "shop", k.ID+99); !errors.Is(err, ErrSSHKeyNotFound) {
		t.Fatalf("get unknown key: %v", err)
	}

	if err := s.DeleteSSHKey(ctx, 1, "shop", k.ID); err != nil || len(ag.applied["shop"]) != 0 {
		t.Fatalf("delete %v %+v", err, ag.applied)
	}

	// The limit per site.
	for range sshkeys.MaxKeys {
		if _, err := s.CreateSSHKey(ctx, 1, "shop", SSHKeyInput{Label: "k", Public: newEdKey(t)}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := s.CreateSSHKey(ctx, 1, "shop", SSHKeyInput{Label: "k", Public: newEdKey(t)}); !errors.Is(err, ErrSSHKeyLimit) {
		t.Fatalf("limit: %v", err)
	}

	// Sync sends every site's list again.
	ag.applied = map[string][]agentapi.SSHKey{}
	if err := s.SyncSSH(ctx); err != nil || len(ag.applied["shop"]) != sshkeys.MaxKeys {
		t.Fatalf("sync %v %d", err, len(ag.applied["shop"]))
	}

	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action LIKE ?`, "ssh.key.%", &n); err != nil || n != 3+sshkeys.MaxKeys {
		t.Fatalf("audit entries %d %v", n, err)
	}
}
