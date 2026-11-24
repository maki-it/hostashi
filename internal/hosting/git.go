// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

// Git deploy settings of a site (ADR-0062). This file stores and checks
// them; running a deploy comes with the deploy unit.

// ErrNoGit: the panel has no secret key to seal tokens and deploy keys.
var ErrNoGit = errors.New("hosting: Git deploy is not available")

// ErrGitNotConfigured: the site has no Git setting.
var ErrGitNotConfigured = errors.New("hosting: the site has no Git repository")

// EnableGit gives the service the panel's secret key, which seals the
// tokens and deploy keys of the sites' repositories.
func (s *Service) EnableGit(box *secrets.Box) { s.gitBox = box }

// GitInput is a new Git setting of a site. An empty Token with Auth
// "token" keeps the stored token.
type GitInput struct {
	URL          string
	Branch       string
	DeployDir    string
	Shared       []string
	Commands     []string
	Keep         int
	BuildTimeout time.Duration
	Submodules   bool
	Auth         string
	TokenUser    string
	Token        string
}

// GitView is the Git setting of a site without its secrets.
type GitView struct {
	// Configured is false for a site without a repository; the other
	// fields then hold the defaults for a new one.
	Configured   bool
	URL          string
	Branch       string
	DeployDir    string
	Shared       []string
	Commands     []string
	Keep         int
	BuildTimeout time.Duration
	Submodules   bool
	Auth         string
	TokenUser    string
	HasToken     bool
	// PublicKey is the deploy key to add to the repository as read only.
	PublicKey string
	// HostKey is the confirmed host key of an SSH repository, empty until
	// it is confirmed.
	HostKey string
	// HostKeyFingerprint is its SHA-256 fingerprint, empty without one.
	HostKeyFingerprint string
	Webhook            bool
	UpdatedAt          time.Time
}

// gitSiteTypes can deploy from Git: the types whose files Hostashi serves
// or runs. Proxy and redirect sites have no files of their own.
var gitSiteTypes = []string{store.SiteTypeStatic, store.SiteTypePHP, store.SiteTypeNode, store.SiteTypeApp, store.SiteTypePython}

func gitContext(siteID int64, what string) string {
	return "git-" + what + ":site:" + strconv.FormatInt(siteID, 10)
}

// gitSite returns a site that can deploy from Git.
func (s *Service) gitSite(ctx context.Context, name string) (store.Site, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if !site.Account || !slices.Contains(gitSiteTypes, site.Type) {
		return store.Site{}, ErrSiteType
	}

	return site, nil
}

func gitDefaults() GitView {
	return GitView{
		Branch: agentapi.DefaultGitBranch, DeployDir: agentapi.DefaultAppWorkdir,
		Shared: []string{}, Commands: []string{},
		Keep: agentapi.DefaultGitKeep, BuildTimeout: agentapi.DefaultBuildTimeout, Auth: agentapi.GitAuthNone,
	}
}

func toGitView(g store.SiteGit) GitView {
	return GitView{
		Configured: true, URL: g.URL, Branch: g.Branch, DeployDir: g.DeployDir,
		Shared: slices.Clone(g.Shared), Commands: slices.Clone(g.Commands),
		Keep: g.Keep, BuildTimeout: g.BuildTimeout, Submodules: g.Submodules,
		Auth: g.Auth, TokenUser: g.TokenUser, HasToken: len(g.Token) > 0,
		PublicKey: g.SSHPublic, HostKey: g.HostKey, HostKeyFingerprint: hostKeyFingerprint(g.HostKey), Webhook: g.WebhookID != "", UpdatedAt: g.UpdatedAt,
	}
}

// Git returns the Git setting of a site, or the defaults for a new one.
func (s *Service) Git(ctx context.Context, name string) (GitView, error) {
	site, err := s.gitSite(ctx, name)
	if err != nil {
		return GitView{}, err
	}

	g, err := s.st.SiteGit(ctx, site.ID)
	if errors.Is(err, store.ErrNotFound) {
		return gitDefaults(), nil
	}

	if err != nil {
		return GitView{}, err
	}

	return toGitView(g), nil
}

func validateGitInput(in GitInput) (agentapi.GitRemote, error) {
	rem, err := agentapi.ParseGitURL(in.URL)
	if err != nil {
		return rem, err
	}

	checks := []error{
		agentapi.ValidateGitBranch(in.Branch),
		agentapi.ValidateGitDeployDir(in.DeployDir),
		agentapi.ValidateGitShared(in.Shared),
		agentapi.ValidateGitCommands(in.Commands),
		agentapi.ValidateGitKeep(in.Keep),
		agentapi.ValidateGitBuildTimeout(in.BuildTimeout),
	}

	switch in.Auth {
	case agentapi.GitAuthNone:
		if rem.Scheme != "https" {
			checks = append(checks, errors.New("an SSH address needs the deploy key (auth ssh)"))
		}
	case agentapi.GitAuthToken:
		if rem.Scheme != "https" {
			checks = append(checks, errors.New("a token works only with an https address"))
		}

		checks = append(checks, agentapi.ValidateGitTokenUser(in.TokenUser))
		if in.Token != "" {
			checks = append(checks, agentapi.ValidateGitToken(in.Token))
		}
	case agentapi.GitAuthSSH:
		if rem.Scheme != "ssh" {
			checks = append(checks, errors.New("the deploy key works only with an SSH address"))
		}
	default:
		checks = append(checks, fmt.Errorf("auth must be %s, %s or %s", agentapi.GitAuthNone, agentapi.GitAuthToken, agentapi.GitAuthSSH))
	}

	if in.Auth != agentapi.GitAuthToken && (in.Token != "" || in.TokenUser != "") {
		checks = append(checks, errors.New("a token is only used with auth token"))
	}

	return rem, errors.Join(checks...)
}

// SetGit stores the Git setting of a site. With auth ssh, a site without a
// deploy key gets one. A new host or port of an SSH address drops the
// confirmed host key. Nothing is fetched yet.
func (s *Service) SetGit(ctx context.Context, actor int64, name string, in GitInput) (GitView, error) {
	in.Shared = nonNilStrings(in.Shared)
	in.Commands = nonNilStrings(in.Commands)

	rem, err := validateGitInput(in)
	if err != nil {
		return GitView{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if s.gitBox == nil {
		return GitView{}, ErrNoGit
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.gitSite(ctx, name)
	if err != nil {
		return GitView{}, err
	}

	if site.State != store.SiteActive {
		return GitView{}, ErrState
	}

	old, err := s.st.SiteGit(ctx, site.ID)

	configured := err == nil
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return GitView{}, err
	}

	g := store.SiteGit{
		SiteID: site.ID, URL: in.URL, Branch: in.Branch, DeployDir: in.DeployDir,
		Shared: in.Shared, Commands: in.Commands, Keep: in.Keep, BuildTimeout: in.BuildTimeout, Submodules: in.Submodules,
		Auth: in.Auth, UpdatedBy: actor,
		// The deploy key and the webhook stay when the repository changes.
		SSHKey: old.SSHKey, SSHPublic: old.SSHPublic, WebhookID: old.WebhookID, WebhookSecret: old.WebhookSecret,
	}

	if configured && rem.Scheme == "ssh" && sameGitHost(old.URL, rem) {
		g.HostKey = old.HostKey
	}

	if in.Auth == agentapi.GitAuthToken {
		g.TokenUser = in.TokenUser

		switch {
		case in.Token != "":
			if g.Token, err = s.gitBox.Seal([]byte(in.Token), gitContext(site.ID, "token")); err != nil {
				return GitView{}, err
			}
		case configured && old.Auth == agentapi.GitAuthToken:
			g.Token = old.Token
		default:
			return GitView{}, fmt.Errorf("%w: a token is required", ErrInvalid)
		}
	}

	if in.Auth == agentapi.GitAuthSSH && len(g.SSHKey) == 0 {
		if g.SSHKey, g.SSHPublic, err = s.newDeployKey(site); err != nil {
			return GitView{}, err
		}
	}

	if err := s.st.SetSiteGit(ctx, g, s.now()); err != nil {
		return GitView{}, err
	}

	var before map[string]string
	if configured {
		before = gitAudit(toGitView(old))
	}

	after := toGitView(g)
	if in.Token != "" {
		after.HasToken = true
	}

	a := gitAudit(after)
	if in.Token != "" && before != nil && before["token"] == "set" {
		a["token"] = "replaced"
	}

	s.auditChange(ctx, actor, "site.git.update", site.Name, nil, before, a)

	saved, err := s.st.SiteGit(ctx, site.ID)
	if err != nil {
		return GitView{}, err
	}

	return toGitView(saved), nil
}

// sameGitHost reports whether an old address names the same SSH server as
// a parsed new one.
func sameGitHost(oldURL string, rem agentapi.GitRemote) bool {
	o, err := agentapi.ParseGitURL(oldURL)

	return err == nil && o.Scheme == "ssh" && strings.EqualFold(o.Host, rem.Host) && o.Port == rem.Port
}

// DeleteGit removes the Git setting of a site, with its token, deploy key
// and webhook. The files of the live release stay.
func (s *Service) DeleteGit(ctx context.Context, actor int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.gitSite(ctx, name)
	if err != nil {
		return err
	}

	old, err := s.st.SiteGit(ctx, site.ID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrGitNotConfigured
	}

	if err != nil {
		return err
	}

	if err := s.st.DeleteSiteGit(ctx, site.ID); err != nil {
		return err
	}

	s.auditChange(ctx, actor, "site.git.delete", site.Name, nil, gitAudit(toGitView(old)), nil)

	return nil
}

// NewGitKey replaces the deploy key of a site that deploys over SSH. The
// old key stops working once it is removed from the repository.
func (s *Service) NewGitKey(ctx context.Context, actor int64, name string) (GitView, error) {
	if s.gitBox == nil {
		return GitView{}, ErrNoGit
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.gitSite(ctx, name)
	if err != nil {
		return GitView{}, err
	}

	g, err := s.st.SiteGit(ctx, site.ID)
	if errors.Is(err, store.ErrNotFound) {
		return GitView{}, ErrGitNotConfigured
	}

	if err != nil {
		return GitView{}, err
	}

	if g.Auth != agentapi.GitAuthSSH {
		return GitView{}, fmt.Errorf("%w: the site does not deploy over SSH", ErrInvalid)
	}

	oldPublic := g.SSHPublic

	if g.SSHKey, g.SSHPublic, err = s.newDeployKey(site); err != nil {
		return GitView{}, err
	}

	g.UpdatedBy = actor

	if err := s.st.SetSiteGit(ctx, g, s.now()); err != nil {
		return GitView{}, err
	}

	s.auditChange(ctx, actor, "site.git.key", site.Name, nil,
		map[string]string{"publicKey": keyFingerprint(oldPublic)}, map[string]string{"publicKey": keyFingerprint(g.SSHPublic)})

	saved, err := s.st.SiteGit(ctx, site.ID)
	if err != nil {
		return GitView{}, err
	}

	return toGitView(saved), nil
}

// newDeployKey makes an Ed25519 key pair and returns the private key in
// OpenSSH form, sealed for the site, and the public key line.
func (s *Service) newDeployKey(site store.Site) ([]byte, string, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, "", fmt.Errorf("hosting: deploy key: %w", err)
	}

	comment := "hostashi-" + site.Name

	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		return nil, "", fmt.Errorf("hosting: deploy key: %w", err)
	}

	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, "", fmt.Errorf("hosting: deploy key: %w", err)
	}

	sealed, err := s.gitBox.Seal(pem.EncodeToMemory(block), gitContext(site.ID, "key"))
	if err != nil {
		return nil, "", err
	}

	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))) + " " + comment

	return sealed, line, nil
}

// keyFingerprint is the SHA-256 fingerprint of a public key line, for the
// audit log; empty for none.
func keyFingerprint(line string) string {
	if line == "" {
		return ""
	}

	k, _, _, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		return "invalid"
	}

	return ssh.FingerprintSHA256(k)
}

// gitAudit describes a setting for the audit log, without secrets.
func gitAudit(v GitView) map[string]string {
	token := ""
	if v.HasToken {
		token = "set"
	}

	return map[string]string{
		"url": v.URL, "branch": v.Branch, "deployDir": v.DeployDir,
		"shared":       strings.Join(v.Shared, " "),
		"commands":     strconv.Itoa(len(v.Commands)) + " (" + commandsDigest(v.Commands) + ")",
		"keep":         strconv.Itoa(v.Keep),
		"buildTimeout": v.BuildTimeout.String(),
		"submodules":   strconv.FormatBool(v.Submodules),
		"auth":         v.Auth, "tokenUser": v.TokenUser, "token": token,
		"publicKey": keyFingerprint(v.PublicKey), "hostKey": hostKeyFingerprint(v.HostKey),
	}
}

// commandsDigest shortens the build commands for the audit diff: a change
// of any command changes it.
func commandsDigest(cmds []string) string {
	sum := sha256.Sum256([]byte(strings.Join(cmds, "\n")))

	return hex.EncodeToString(sum[:6])
}

func hostKeyFingerprint(k string) string {
	if k == "" {
		return ""
	}

	return keyFingerprint(k)
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}

	return v
}
