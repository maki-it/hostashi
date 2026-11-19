// SPDX-License-Identifier: AGPL-3.0-or-later

package cpsource_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cpsource"
)

func hostKey(t *testing.T) (ssh.PublicKey, string) {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	return k, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k)))
}

func newSource(t *testing.T) *cpsource.Source {
	t.Helper()

	return &cpsource.Source{Dir: filepath.Join(t.TempDir(), "cloudpanel")}
}

func TestLifecycle(t *testing.T) {
	s := newSource(t)

	info, err := s.Get()
	if err != nil || info.Source != nil || info.Ready {
		t.Fatalf("empty: %+v %v", info, err)
	}

	if _, err := s.SetHostKey("ssh-ed25519 AAAA"); err == nil {
		t.Fatal("host key without source")
	}

	info, err = s.Set(agentapi.CloudPanelSource{Host: "203.0.113.7", Port: 22})
	if err != nil || info.Ready || !strings.HasPrefix(info.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("set: %+v %v", info, err)
	}

	want := `restrict,port-forwarding,permitopen="127.0.0.1:3306",command="/usr/lib/openssh/sftp-server -R" ` + info.PublicKey
	if info.AuthorizedKeysLine != want {
		t.Fatalf("line %q", info.AuthorizedKeysLine)
	}

	st, err := os.Stat(filepath.Join(s.Dir, cpsource.KeyFile))
	if err != nil || st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("key file: %v %v", st, err)
	}

	if _, _, err := s.ClientConfig(); !errors.Is(err, cpsource.ErrNotReady) {
		t.Fatalf("client config before host key: %v", err)
	}

	k, line := hostKey(t)

	info, err = s.SetHostKey(line)
	if err != nil || !info.Ready || info.HostKey != ssh.FingerprintSHA256(k) {
		t.Fatalf("host key: %+v %v", info, err)
	}

	// The key stays when only the port changes, the host key does not.
	pub := info.PublicKey

	info, err = s.Set(agentapi.CloudPanelSource{Host: "203.0.113.7", Port: 2222})
	if err != nil || info.Ready || info.PublicKey != pub {
		t.Fatalf("new port: %+v %v", info, err)
	}

	if _, err := s.SetHostKey(line); err != nil {
		t.Fatal(err)
	}

	src, cfg, err := s.ClientConfig()
	if err != nil || src.Port != 2222 || cfg.User != "root" {
		t.Fatalf("client config: %+v %v", src, err)
	}

	if err := cfg.HostKeyCallback("x", nil, k); err != nil {
		t.Fatalf("pinned key refused: %v", err)
	}

	other, _ := hostKey(t)
	if err := cfg.HostKeyCallback("x", nil, other); err == nil {
		t.Fatal("other host key accepted")
	}

	if _, err := s.Delete(); err != nil {
		t.Fatal(err)
	}

	for _, f := range []string{cpsource.KeyFile, cpsource.KnownHostsFile, cpsource.SourceFile} {
		if _, err := os.Stat(filepath.Join(s.Dir, f)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s left after delete: %v", f, err)
		}
	}

	// A new source after delete gets a new key.
	info, err = s.Set(agentapi.CloudPanelSource{Host: "old.example.com", Port: 22})
	if err != nil || info.PublicKey == pub {
		t.Fatalf("after delete: %+v %v", info, err)
	}
}

func TestIPv6HostKey(t *testing.T) {
	s := newSource(t)
	if _, err := s.Set(agentapi.CloudPanelSource{Host: "2001:db8::7", Port: 22}); err != nil {
		t.Fatal(err)
	}

	_, line := hostKey(t)

	if info, err := s.SetHostKey(line); err != nil || !info.Ready {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestRefuses(t *testing.T) {
	s := newSource(t)

	for _, src := range []agentapi.CloudPanelSource{
		{Host: "", Port: 22},
		{Host: "Old.Example.com", Port: 22},
		{Host: "old.example.com", Port: 0},
		{Host: "old.example.com", Port: 70000},
		{Host: "0.0.0.0", Port: 22},
		{Host: "old example", Port: 22},
		{Host: "-oProxyCommand=x", Port: 22},
		{Host: "::ffff:1.2.3.4", Port: 22},
	} {
		if _, err := s.Set(src); !errors.Is(err, agentapi.ErrSourceInvalid) {
			t.Errorf("%+v: %v", src, err)
		}
	}

	if _, err := s.Set(agentapi.CloudPanelSource{Host: "old.example.com", Port: 22}); err != nil {
		t.Fatal(err)
	}

	for _, line := range []string{"", "nonsense", `command="x" ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIJ7VcR+Y2pWk7z6kYQh3V1n4sT9cLr2pYbXa8mW3qF0d`} {
		if _, err := s.SetHostKey(line); !errors.Is(err, cpsource.ErrBadHostKey) {
			t.Errorf("%q: %v", line, err)
		}
	}
}

func TestLooseDirectoryRefused(t *testing.T) {
	s := newSource(t)
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Set(agentapi.CloudPanelSource{Host: "old.example.com", Port: 22}); err == nil {
		t.Fatal("directory open to others accepted")
	}
}
