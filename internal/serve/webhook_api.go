// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
)

// webhookPath is where a Git host calls a site's webhook.
const webhookPath = "/api/v1/hooks/deploy/"

// Limits of the webhook (ADR-0062).
const (
	maxWebhookBody   = 1 << 20
	webhookPerMinute = 10
)

// webhookJSON is a site's webhook, shown once with its secret.
type webhookJSON struct {
	// ID is the random part of the address.
	ID string `json:"id"`
	// Path is the address below the panel's origin; the Git host calls it
	// with POST.
	Path string `json:"path"`
	// Secret proves a call: the webhook secret for GitHub, Gitea and
	// Forgejo (HMAC-SHA256 of the body), the secret token for GitLab, or
	// "Authorization: Bearer <secret>". It is not shown again.
	Secret string `json:"secret"`
}

// webhookResultJSON is the answer to a webhook call.
type webhookResultJSON struct {
	// Result is "started" (a deploy runs), "queued" (one runs after the
	// active run), "ignored" (another branch, a tag or another event) or
	// "pong" (GitHub's test call).
	Result string `json:"result"`
}

// webhookProof checks the proof of a call against the secret: GitHub's
// X-Hub-Signature-256, Gitea's and Forgejo's signature headers (HMAC-SHA256
// of the body), GitLab's X-Gitlab-Token or a bearer token. The first proof
// present must be valid; a call without one fails.
func webhookProof(h http.Header, body, secret []byte) bool {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	sum := mac.Sum(nil)

	sig := func(v string) bool {
		got, err := hex.DecodeString(v)

		return err == nil && hmac.Equal(got, sum)
	}

	same := func(v string) bool { return subtle.ConstantTimeCompare([]byte(v), secret) == 1 }

	switch {
	case h.Get("X-Hub-Signature-256") != "":
		v, ok := strings.CutPrefix(h.Get("X-Hub-Signature-256"), "sha256=")

		return ok && sig(v)
	case h.Get("X-Gitea-Signature") != "":
		return sig(h.Get("X-Gitea-Signature"))
	case h.Get("X-Forgejo-Signature") != "":
		return sig(h.Get("X-Forgejo-Signature"))
	case h.Get("X-Gitlab-Token") != "":
		return same(h.Get("X-Gitlab-Token"))
	case h.Get("Authorization") != "":
		v, ok := strings.CutPrefix(h.Get("Authorization"), "Bearer ")

		return ok && same(v)
	}

	return false
}

// webhookWanted reports whether a call with a valid proof asks for a deploy
// of branch: a push to it. Pings and other events of the known hosts, tags
// and other branches are not. A generic call without a body deploys.
func webhookWanted(h http.Header, body []byte, branch string) (want bool, result string) {
	switch {
	case h.Get("X-GitHub-Event") == "ping":
		return false, "pong"
	case h.Get("X-GitHub-Event") != "" && h.Get("X-GitHub-Event") != "push",
		h.Get("X-Gitea-Event") != "" && h.Get("X-Gitea-Event") != "push",
		h.Get("X-Forgejo-Event") != "" && h.Get("X-Forgejo-Event") != "push",
		h.Get("X-Gitlab-Event") != "" && h.Get("X-Gitlab-Event") != "Push Hook":
		return false, "ignored"
	}

	if len(bytes.TrimSpace(body)) == 0 {
		return true, ""
	}

	// Only the pushed ref is read; the rest of the body is ignored.
	var in struct {
		Ref *string `json:"ref"`
	}

	if err := json.Unmarshal(body, &in); err != nil || in.Ref == nil {
		return false, "ignored"
	}

	if *in.Ref != "refs/heads/"+branch {
		return false, "ignored"
	}

	return true, ""
}

// registerWebhook adds the webhook of a site (ADR-0062): turning it on or
// renewing its secret and turning it off need sites.git:write; the call
// from the Git host needs no login, only the proof.
func registerWebhook(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("POST /api/v1/sites/{name}/git/webhook", openapi.Operation{
		ID: "setSiteGitWebhook", Tag: "git", Summary: "Turn the deploy webhook of a site on, or give it a new secret; the secret is shown only here",
		Out: webhookJSON{},
	}, a.allow("sites.git:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		hook, err := svc.SetGitWebhook(r.Context(), p.User.ID, r.PathValue("name"))
		if err != nil {
			gitFail(log, w, err, "set webhook")

			return
		}

		writeJSON(w, http.StatusOK, webhookJSON{ID: hook.ID, Path: webhookPath + hook.ID, Secret: hook.Secret})
	}))

	mux.Op("DELETE /api/v1/sites/{name}/git/webhook", openapi.Operation{
		ID: "deleteSiteGitWebhook", Tag: "git", Summary: "Turn the deploy webhook of a site off",
	}, a.allow("sites.git:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if err := svc.DeleteGitWebhook(r.Context(), p.User.ID, r.PathValue("name")); err != nil {
			gitFail(log, w, err, "delete webhook")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	limit := auth.NewLimiter(webhookPerMinute, time.Minute, nil)

	mux.Op("POST "+webhookPath+"{id}", openapi.Operation{
		ID: "callDeployWebhook", Tag: "git",
		Summary: "Deploy a site's branch on a push (called by the Git host, with a signature or token instead of a login)",
		Out:     webhookResultJSON{}, Status: http.StatusAccepted, AlsoStatus: []int{http.StatusOK},
	}, func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		if limit.Blocked(id) {
			writeError(w, http.StatusTooManyRequests, "rate_limited", "too many calls of this webhook, try again later")

			return
		}

		limit.Fail(id)

		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "too_large", "the body is larger than 1 MiB")

			return
		}

		t, err := svc.GitWebhook(r.Context(), id)
		if err != nil && !errors.Is(err, hosting.ErrWebhookNotFound) {
			log.ErrorContext(r.Context(), "webhook", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "internal error")

			return
		}

		// An unknown id and a wrong proof answer alike.
		if err != nil || !webhookProof(r.Header, body, t.Secret) {
			if a.svc != nil {
				a.svc.WebhookFailed(r.Context(), clientIP(r))
			}

			writeError(w, http.StatusUnauthorized, "unauthorized", "unknown webhook or wrong signature")

			return
		}

		want, result := webhookWanted(r.Header, body, t.Branch)
		if !want {
			status := http.StatusAccepted
			if result == "pong" {
				status = http.StatusOK
			}

			writeJSON(w, status, webhookResultJSON{Result: result})

			return
		}

		started, err := svc.WebhookDeploy(r.Context(), t.Site)
		if err != nil {
			deployFail(log, w, err, "webhook deploy")

			return
		}

		result = "queued"
		if started {
			result = "started"
		}

		writeJSON(w, http.StatusAccepted, webhookResultJSON{Result: result})
	})
}
