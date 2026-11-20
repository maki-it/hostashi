// SPDX-License-Identifier: AGPL-3.0-or-later

package sshaccess

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
	"github.com/maki-it/hostashi/internal/sshkeys"
	"github.com/maki-it/hostashi/internal/sysuser"
)

type fakeShells struct {
	shells map[string]string
	err    error
}

func (f *fakeShells) SetShell(_ context.Context, name, shell string) error {
	if f.err != nil {
		return f.err
	}

	f.shells[name] = shell

	return nil
}

func newKey(t *testing.T) string {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	pk, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSuffix(string(ssh.MarshalAuthorizedKey(pk)), "\n")
}

func newManager(t *testing.T) (*Manager, *fakeShells) {
	t.Helper()

	dir := t.TempDir()
	keys := filepath.Join(dir, "keys")

	if err := os.Mkdir(keys, 0o755); err != nil {
		t.Fatal(err)
	}

	uid := uint32(os.Getuid())
	sh := &fakeShells{shells: map[string]string{}}

	return &Manager{StateFile: filepath.Join(dir, "ssh.json"), KeyDir: keys, Shells: sh, dirOwner: &uid}, sh
}

func keyFile(t *testing.T, m *Manager, site string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join(m.KeyDir, site))
	if err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(filepath.Join(m.KeyDir, site))
	if err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("key file mode %v %v", fi.Mode(), err)
	}

	return string(b)
}

func TestApplyShellAndDisable(t *testing.T) {
	m, sh := newManager(t)
	ctx := context.Background()
	a, b := newKey(t), newKey(t)

	shell, err := m.Apply(ctx, agentapi.SSHApplyRequest{Site: "blog", Keys: []agentapi.SSHKey{{Label: "kim", Access: sshkeys.SFTP, Public: a}}})
	if err != nil || shell || sh.shells["blog"] != sysuser.NoLogin {
		t.Fatalf("sftp only: shell=%v err=%v login=%q", shell, err, sh.shells["blog"])
	}

	if f := keyFile(t, m, "blog"); !strings.Contains(f, `restrict,command="internal-sftp" `+a+" kim\n") {
		t.Fatalf("key file:\n%s", f)
	}

	shell, err = m.Apply(ctx, agentapi.SSHApplyRequest{Site: "blog", Keys: []agentapi.SSHKey{
		{Label: "kim", Access: sshkeys.SFTP, Public: a}, {Label: "deploy", Access: sshkeys.Shell, Public: b},
	}})
	if err != nil || !shell || sh.shells["blog"] != sysuser.Bash {
		t.Fatalf("shell key: shell=%v err=%v login=%q", shell, err, sh.shells["blog"])
	}

	// Disabled: no keys in the file, no shell, but the list is kept.
	if err := m.SetDisabled(ctx, "blog", true); err != nil {
		t.Fatal(err)
	}

	if f := keyFile(t, m, "blog"); strings.Contains(f, "ssh-") || sh.shells["blog"] != sysuser.NoLogin {
		t.Fatalf("disabled: %q %q", f, sh.shells["blog"])
	}

	// A change while disabled is stored but not rendered.
	if _, err := m.Apply(ctx, agentapi.SSHApplyRequest{Site: "blog", Keys: []agentapi.SSHKey{{Label: "deploy", Access: sshkeys.Shell, Public: b}}}); err != nil {
		t.Fatal(err)
	}

	if f := keyFile(t, m, "blog"); strings.Contains(f, "ssh-") {
		t.Fatalf("disabled site got keys: %q", f)
	}

	if err := m.SetDisabled(ctx, "blog", false); err != nil {
		t.Fatal(err)
	}

	if f := keyFile(t, m, "blog"); strings.Contains(f, a) || !strings.Contains(f, "restrict,pty "+b+" deploy\n") || sh.shells["blog"] != sysuser.Bash {
		t.Fatalf("enabled again: %q %q", f, sh.shells["blog"])
	}

	// All keys gone: empty file, nologin.
	if shell, err := m.Apply(ctx, agentapi.SSHApplyRequest{Site: "blog"}); err != nil || shell || sh.shells["blog"] != sysuser.NoLogin {
		t.Fatalf("no keys: %v %v %q", shell, err, sh.shells["blog"])
	}

	if err := m.Remove(ctx, "blog"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(filepath.Join(m.KeyDir, "blog")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("key file after Remove: %v", err)
	}

	if err := m.Remove(ctx, "blog"); err != nil {
		t.Fatalf("second Remove: %v", err)
	}
}

func TestSetDisabledUnknownSite(t *testing.T) {
	m, sh := newManager(t)

	if err := m.SetDisabled(context.Background(), "blog", false); err != nil || len(sh.shells) != 0 {
		t.Fatalf("enable unknown: %v %v", err, sh.shells)
	}

	// Disabling a site without keys writes an empty file, so nothing old
	// can stay behind.
	if err := m.SetDisabled(context.Background(), "blog", true); err != nil {
		t.Fatal(err)
	}

	if f := keyFile(t, m, "blog"); strings.Contains(f, "ssh-") {
		t.Fatalf("%q", f)
	}
}

func TestApplyRefuses(t *testing.T) {
	m, sh := newManager(t)
	ctx := context.Background()
	k := newKey(t)

	for name, req := range map[string]agentapi.SSHApplyRequest{
		"bad site": {Site: "../etc", Keys: []agentapi.SSHKey{{Label: "a", Access: sshkeys.SFTP, Public: k}}},
		"options":  {Site: "blog", Keys: []agentapi.SSHKey{{Label: "a", Access: sshkeys.SFTP, Public: `command="sh" ` + k}}},
	} {
		if _, err := m.Apply(ctx, req); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	if len(sh.shells) != 0 {
		t.Fatalf("shell changed for a refused request: %v", sh.shells)
	}

	// A key directory others can write is refused before anything is written.
	if err := os.Chmod(m.KeyDir, 0o777); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Apply(ctx, agentapi.SSHApplyRequest{Site: "blog", Keys: []agentapi.SSHKey{{Label: "a", Access: sshkeys.SFTP, Public: k}}}); err == nil {
		t.Fatal("writable key directory accepted")
	}

	if err := os.Chmod(m.KeyDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// The key file is written before the shell; a failing usermod is reported.
	sh.err = errors.New("usermod failed")
	if _, err := m.Apply(ctx, agentapi.SSHApplyRequest{Site: "blog", Keys: []agentapi.SSHKey{{Label: "a", Access: sshkeys.Shell, Public: k}}}); err == nil {
		t.Fatal("usermod failure not reported")
	}
}

func TestHostKeys(t *testing.T) {
	dir := t.TempDir()
	ed := newKey(t)

	for name, data := range map[string]string{
		"ssh_host_ed25519_key.pub": ed + " root@host\n",
		"ssh_host_rsa_key.pub":     "not a key\n",
		"ssh_host_ed25519_key":     "private, never read\n",
		"other.pub":                ed + "\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(ed))
	if err != nil {
		t.Fatal(err)
	}

	m := &Manager{HostKeyDir: dir}

	keys, err := m.HostKeys(t.Context())
	if err != nil || len(keys) != 1 || keys[0].Type != ssh.KeyAlgoED25519 || keys[0].Fingerprint != ssh.FingerprintSHA256(pk) {
		t.Fatalf("%+v %v", keys, err)
	}

	big := filepath.Join(dir, "ssh_host_ecdsa_key.pub")
	if err := os.WriteFile(big, []byte(strings.Repeat("a", maxHostKeyFile+1)), 0o644); err != nil {
		t.Fatal(err)
	}

	if keys, err := m.HostKeys(t.Context()); err != nil || len(keys) != 1 {
		t.Fatalf("oversized file: %+v %v", keys, err)
	}
}
