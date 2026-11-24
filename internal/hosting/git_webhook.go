// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"

	"github.com/maki-it/hostashi/internal/store"
)

// ErrWebhookNotFound: no site has a webhook with this id.
var ErrWebhookNotFound = errors.New("hosting: no webhook with this id")

// Webhook is a site's webhook as shown once after it is turned on or its
// secret renewed.
type Webhook struct {
	ID string
	// Secret proves a call: the HMAC key for GitHub, Gitea and Forgejo,
	// the token for GitLab and the bearer token otherwise.
	Secret string
}

// WebhookTarget is what a webhook call needs: the site, its branch and the
// unsealed secret.
type WebhookTarget struct {
	Site   string
	Branch string
	Secret []byte
}

// SetGitWebhook turns the webhook of a site on, or gives an existing one a
// new secret; the id stays, so the address in the Git host stays too. The
// secret is shown only in this answer.
func (s *Service) SetGitWebhook(ctx context.Context, actor int64, name string) (Webhook, error) {
	if s.gitBox == nil {
		return Webhook{}, ErrNoGit
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.gitSite(ctx, name)
	if err != nil {
		return Webhook{}, err
	}

	g, err := s.st.SiteGit(ctx, site.ID)
	if errors.Is(err, store.ErrNotFound) {
		return Webhook{}, ErrGitNotConfigured
	}

	if err != nil {
		return Webhook{}, err
	}

	renewed := g.WebhookID != ""

	if !renewed {
		id := make([]byte, 16)
		if _, err := rand.Read(id); err != nil {
			return Webhook{}, err
		}

		g.WebhookID = hex.EncodeToString(id)
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return Webhook{}, err
	}

	secret := base64.RawURLEncoding.EncodeToString(raw)

	if g.WebhookSecret, err = s.gitBox.Seal([]byte(secret), gitContext(site.ID, "hook")); err != nil {
		return Webhook{}, err
	}

	g.UpdatedBy = actor

	if err := s.st.SetSiteGit(ctx, g, s.now()); err != nil {
		return Webhook{}, err
	}

	action := "on"
	if renewed {
		action = "new secret"
	}

	s.audit(ctx, actor, "site.git.webhook", site.Name, map[string]string{"webhook": action})

	return Webhook{ID: g.WebhookID, Secret: secret}, nil
}

// DeleteGitWebhook turns the webhook of a site off; calls to its address
// then answer as for an unknown id.
func (s *Service) DeleteGitWebhook(ctx context.Context, actor int64, name string) error {
	if s.gitBox == nil {
		return ErrNoGit
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.gitSite(ctx, name)
	if err != nil {
		return err
	}

	g, err := s.st.SiteGit(ctx, site.ID)
	if errors.Is(err, store.ErrNotFound) {
		return ErrGitNotConfigured
	}

	if err != nil {
		return err
	}

	if g.WebhookID == "" {
		return nil
	}

	g.WebhookID, g.WebhookSecret, g.UpdatedBy = "", nil, actor

	if err := s.st.SetSiteGit(ctx, g, s.now()); err != nil {
		return err
	}

	s.audit(ctx, actor, "site.git.webhook", site.Name, map[string]string{"webhook": "off"})

	return nil
}

// GitWebhook returns the target of a webhook id, or ErrWebhookNotFound.
func (s *Service) GitWebhook(ctx context.Context, id string) (WebhookTarget, error) {
	if s.gitBox == nil {
		return WebhookTarget{}, ErrWebhookNotFound
	}

	g, name, err := s.st.SiteGitByWebhook(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return WebhookTarget{}, ErrWebhookNotFound
	}

	if err != nil {
		return WebhookTarget{}, err
	}

	secret, err := s.gitBox.Open(g.WebhookSecret, gitContext(g.SiteID, "hook"))
	if err != nil {
		return WebhookTarget{}, err
	}

	return WebhookTarget{Site: name, Branch: g.Branch, Secret: secret}, nil
}

// WebhookDeploy starts a deploy for a webhook call. While a run of the
// site is active it starts none but notes one more, which RunDeploys
// starts when that run ends, so a push during a build is not lost; more
// calls in that time still give one run. started is false then.
func (s *Service) WebhookDeploy(ctx context.Context, name string) (started bool, err error) {
	_, err = s.Deploy(ctx, 0, name, store.DeploySourceWebhook)
	if errors.Is(err, ErrDeployBusy) {
		s.deployAgainMu.Lock()
		if s.deployAgain == nil {
			s.deployAgain = map[string]bool{}
		}

		s.deployAgain[name] = true
		s.deployAgainMu.Unlock()

		return false, nil
	}

	return err == nil, err
}

// startDeploysAgain starts the runs that webhooks asked for while a run
// was active, for the sites without an active run now.
func (s *Service) startDeploysAgain(ctx context.Context, active map[int64]bool, ids map[string]int64) {
	s.deployAgainMu.Lock()

	var due []string

	for name := range s.deployAgain {
		if id, ok := ids[name]; !ok || !active[id] {
			due = append(due, name)
			delete(s.deployAgain, name)
		}
	}
	s.deployAgainMu.Unlock()

	for _, name := range due {
		if _, err := s.WebhookDeploy(ctx, name); err != nil {
			s.log.WarnContext(ctx, "deploy after webhook", "site", name, "err", err)
		}
	}
}
