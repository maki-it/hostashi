// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

func testHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	k, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}

	return k
}

func TestGitHostKey(t *testing.T) {
	s, _, st := newService(t)
	ctx := t.Context()

	if _, err := s.ScanGitHostKey(ctx, "blog"); !errors.Is(err, ErrNoGit) {
		t.Fatalf("without git: %v", err)
	}

	box, err := secrets.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}

	s.EnableGit(box)

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ScanGitHostKey(ctx, "blog"); !errors.Is(err, ErrGitNotConfigured) {
		t.Fatalf("not configured: %v", err)
	}

	if _, err := s.SetGit(ctx, 1, "blog", gitInput("https://github.com/acme/blog.git", agentapi.GitAuthNone)); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ScanGitHostKey(ctx, "blog"); !errors.Is(err, ErrNotSSH) {
		t.Fatalf("https: %v", err)
	}

	if _, err := s.SetGit(ctx, 1, "blog", gitInput("ssh://git@git.example.com:2222/acme/blog.git", agentapi.GitAuthSSH)); err != nil {
		t.Fatal(err)
	}

	key := testHostKey(t)

	var asked []string

	s.hostKeyScan = func(_ context.Context, addr, user string) (ssh.PublicKey, error) {
		asked = append(asked, user+"@"+addr)
		if addr == "10.0.0.8:2222" {
			return nil, errors.New("connection refused")
		}

		return key, nil
	}

	// A name that also leads back to this host is refused before any
	// connection.
	s.SetResolver(&fakeResolver{addrs: addrs("10.0.0.8", "127.0.0.1")})

	if _, err := s.ScanGitHostKey(ctx, "blog"); !errors.Is(err, ErrInvalid) || len(asked) != 0 {
		t.Fatalf("loopback: %v, asked %v", err, asked)
	}

	// The first address (sorted) does not answer, the second one does.
	s.SetResolver(&fakeResolver{addrs: addrs("10.0.0.9", "10.0.0.8")})

	got, err := s.ScanGitHostKey(ctx, "blog")
	if err != nil || !bytes.Equal(got.Marshal(), key.Marshal()) {
		t.Fatalf("scan %v %v", got, err)
	}

	if len(asked) != 2 || asked[1] != "git@10.0.0.9:2222" {
		t.Fatalf("asked %v", asked)
	}

	// Nobody answers.
	s.hostKeyScan = func(context.Context, string, string) (ssh.PublicKey, error) { return nil, errors.New("timeout") }

	if _, err := s.ScanGitHostKey(ctx, "blog"); !errors.Is(err, ErrHostKeyUnreachable) {
		t.Fatalf("unreachable: %v", err)
	}

	// Confirm: a usable key only.
	if _, err := s.SetGitHostKey(ctx, 1, "blog", "ssh-ed25519 AAAA nope"); !errors.Is(err, ErrBadHostKey) {
		t.Fatalf("bad key: %v", err)
	}

	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))

	v, err := s.SetGitHostKey(ctx, 1, "blog", line+" comment")
	if err != nil || v.HostKey != line {
		t.Fatalf("confirm %+v %v", v, err)
	}

	es, _, err := st.AuditEntries(ctx, store.AuditFilter{Action: "site.git.hostkey"}, 0, 5)
	if err != nil || len(es) != 1 || !bytes.Contains(es[0].Diff, []byte(ssh.FingerprintSHA256(key))) {
		t.Fatalf("audit %+v %v", es, err)
	}

	// The confirmed key lets a deploy start, and stays when only the path
	// changes; a new host drops it.
	in := gitInput("ssh://git@git.example.com:2222/acme/other.git", agentapi.GitAuthSSH)
	if v, err := s.SetGit(ctx, 1, "blog", in); err != nil || v.HostKey != line {
		t.Fatalf("same host %+v %v", v, err)
	}

	in.URL = "git@git2.example.com:acme/blog.git"
	if v, err := s.SetGit(ctx, 1, "blog", in); err != nil || v.HostKey != "" {
		t.Fatalf("new host %+v %v", v, err)
	}
}
