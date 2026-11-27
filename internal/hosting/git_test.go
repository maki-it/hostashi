// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

func withToken(g GitInput, tok string) GitInput {
	g.Token = tok

	return g
}

func gitInput(url, auth string) GitInput {
	return GitInput{
		URL: url, Branch: "main", DeployDir: "htdocs",
		Keep: 5, BuildTimeout: 15 * time.Minute, Auth: auth,
	}
}

func TestGitSettings(t *testing.T) {
	s, _, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	redirect := &store.SiteRedirect{Target: "https://example.com", Status: 302}
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "old", Domains: []string{"old.example.com"}, Redirect: redirect}); err != nil {
		t.Fatal(err)
	}

	in := gitInput("git@github.com:acme/blog.git", agentapi.GitAuthSSH)

	// Without the secret key nothing is stored.
	if _, err := s.SetGit(ctx, 1, "blog", in); !errors.Is(err, ErrNoGit) {
		t.Fatalf("without a key: %v", err)
	}

	box, err := secrets.New(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}

	s.EnableGit(box)

	// Defaults for a site without a repository.
	v, err := s.Git(ctx, "blog")
	if err != nil || v.Configured || v.Branch != "main" || v.DeployDir != "htdocs" || v.Keep != 5 || v.BuildTimeout != 15*time.Minute || v.Auth != agentapi.GitAuthNone {
		t.Fatalf("defaults %+v %v", v, err)
	}

	if _, err := s.Git(ctx, "old"); !errors.Is(err, ErrSiteType) {
		t.Fatalf("redirect site: %v", err)
	}

	if _, err := s.SetGit(ctx, 1, "old", in); !errors.Is(err, ErrSiteType) {
		t.Fatalf("set on redirect site: %v", err)
	}

	if _, err := s.Git(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing site: %v", err)
	}

	for name, bad := range map[string]GitInput{
		"file url":         gitInput("file:///srv/x", agentapi.GitAuthNone),
		"ssh without key":  gitInput("git@github.com:a/b", agentapi.GitAuthNone),
		"https with key":   gitInput("https://github.com/a/b", agentapi.GitAuthSSH),
		"token over ssh":   withToken(gitInput("git@github.com:a/b", agentapi.GitAuthToken), "x"),
		"token, no token":  gitInput("https://github.com/a/b", agentapi.GitAuthToken),
		"token with none":  withToken(gitInput("https://github.com/a/b", agentapi.GitAuthNone), "x"),
		"unknown auth":     gitInput("https://github.com/a/b", "password"),
		"bad branch":       func() GitInput { g := in; g.Branch = "-x"; return g }(),
		"hidden dir":       func() GitInput { g := in; g.DeployDir = ".deploy"; return g }(),
		"bad shared":       func() GitInput { g := in; g.Shared = []string{"../x"}; return g }(),
		"bad command":      func() GitInput { g := in; g.Commands = []string{"a\nb"}; return g }(),
		"keep 1":           func() GitInput { g := in; g.Keep = 1; return g }(),
		"timeout too long": func() GitInput { g := in; g.BuildTimeout = 2 * time.Hour; return g }(),
	} {
		if _, err := s.SetGit(ctx, 1, "blog", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// SSH: Hostashi makes the deploy key.
	in.Shared = []string{".env", "wp-content/uploads"}
	in.Commands = []string{"composer install --no-dev"}

	v, err = s.SetGit(ctx, 1, "blog", in)
	if err != nil {
		t.Fatal(err)
	}

	if !v.Configured || v.URL != in.URL || v.Auth != agentapi.GitAuthSSH || len(v.Shared) != 2 || len(v.Commands) != 1 {
		t.Fatalf("set %+v", v)
	}

	pub, comment, _, _, err := ssh.ParseAuthorizedKey([]byte(v.PublicKey))
	if err != nil || pub.Type() != ssh.KeyAlgoED25519 || comment != "hostashi-blog" {
		t.Fatalf("public key %q: %v", v.PublicKey, err)
	}

	site, _ := s.Get(ctx, "blog")
	row, _ := st.SiteGit(ctx, site.ID)

	if bytes.Contains(row.SSHKey, []byte("PRIVATE KEY")) {
		t.Fatal("private key stored in plain text")
	}

	plain, err := box.Open(row.SSHKey, gitContext(site.ID, "key"))
	if err != nil {
		t.Fatal(err)
	}

	priv, err := ssh.ParsePrivateKey(plain)
	if err != nil || !bytes.Equal(priv.PublicKey().Marshal(), pub.Marshal()) {
		t.Fatalf("private key does not match: %v", err)
	}

	// Bound to the site: it does not open for another one.
	if _, err := box.Open(row.SSHKey, gitContext(site.ID+1, "key")); err == nil {
		t.Fatal("key opens for another site")
	}

	// A change keeps the key; a confirmed host key stays for the same host.
	row.HostKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl"
	if err := st.SetSiteGit(ctx, row, time.Now()); err != nil {
		t.Fatal(err)
	}

	in.Branch = "production"

	v2, err := s.SetGit(ctx, 1, "blog", in)
	if err != nil {
		t.Fatal(err)
	}

	if v2.PublicKey != v.PublicKey || v2.Branch != "production" || v2.HostKey == "" {
		t.Fatalf("change: %+v", v2)
	}

	// Another host drops the confirmed host key.
	in.URL = "ssh://git@gitea.example.com:2222/acme/blog"

	v3, err := s.SetGit(ctx, 1, "blog", in)
	if err != nil {
		t.Fatal(err)
	}

	if v3.HostKey != "" || v3.PublicKey != v.PublicKey {
		t.Fatalf("new host: %+v", v3)
	}

	// A new key replaces the old one.
	v4, err := s.NewGitKey(ctx, 1, "blog")
	if err != nil || v4.PublicKey == v.PublicKey || !strings.HasPrefix(v4.PublicKey, "ssh-ed25519 ") {
		t.Fatalf("new key %+v %v", v4, err)
	}

	d := lastDiff(t, st, "site.git.key")
	if c := d["publicKey"]; c.Old == nil || c.New == nil || *c.Old == *c.New || !strings.HasPrefix(*c.New, "SHA256:") {
		t.Fatalf("key audit %+v", d)
	}

	// Token over HTTPS: sealed, never shown; an empty token keeps it.
	tok := gitInput("https://github.com/acme/blog.git", agentapi.GitAuthToken)
	tok.TokenUser, tok.Token = "x-access-token", "ghp_s3cr3t"

	v5, err := s.SetGit(ctx, 1, "blog", tok)
	if err != nil || !v5.HasToken || v5.TokenUser != "x-access-token" {
		t.Fatalf("token %+v %v", v5, err)
	}

	row, _ = st.SiteGit(ctx, site.ID)
	if bytes.Contains(row.Token, []byte("s3cr3t")) {
		t.Fatal("token stored in plain text")
	}

	if got, err := box.Open(row.Token, gitContext(site.ID, "token")); err != nil || string(got) != "ghp_s3cr3t" {
		t.Fatalf("token %q %v", got, err)
	}

	tok.Token = ""
	if _, err := s.SetGit(ctx, 1, "blog", tok); err != nil {
		t.Fatalf("keep token: %v", err)
	}

	if again, _ := st.SiteGit(ctx, site.ID); !bytes.Equal(again.Token, row.Token) {
		t.Fatal("token not kept")
	}

	// The audit log never holds the token.
	es, _, err := st.AuditEntries(ctx, store.AuditFilter{Action: "site.git"}, 0, 50)
	if err != nil || len(es) < 5 {
		t.Fatalf("audit %d %v", len(es), err)
	}

	for _, e := range es {
		if bytes.Contains(e.Diff, []byte("s3cr3t")) || bytes.Contains(e.Detail, []byte("s3cr3t")) || bytes.Contains(e.Diff, []byte("PRIVATE")) {
			t.Fatalf("secret in audit: %s", e.Diff)
		}
	}

	// A new key needs SSH.
	if _, err := s.NewGitKey(ctx, 1, "blog"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("new key over https: %v", err)
	}

	// Public HTTPS: the token goes.
	pubIn := gitInput("https://github.com/acme/blog.git", agentapi.GitAuthNone)
	if v6, err := s.SetGit(ctx, 1, "blog", pubIn); err != nil || v6.HasToken {
		t.Fatalf("public %+v %v", v6, err)
	}

	if err := s.DeleteGit(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	if v, _ := s.Git(ctx, "blog"); v.Configured {
		t.Fatal("still configured")
	}

	if err := s.DeleteGit(ctx, 1, "blog"); !errors.Is(err, ErrGitNotConfigured) {
		t.Fatalf("delete twice: %v", err)
	}

	if _, err := s.NewGitKey(ctx, 1, "blog"); !errors.Is(err, ErrGitNotConfigured) {
		t.Fatalf("new key without setting: %v", err)
	}
}
