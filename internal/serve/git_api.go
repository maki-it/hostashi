// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
)

// gitJSON is the Git deploy setting of a site (ADR-0062), without secrets.
type gitJSON struct {
	// Configured is false for a site without a repository; the other
	// fields then hold the defaults for a new one.
	Configured bool `json:"configured"`
	// URL is https://host/path, ssh://[user@]host[:port]/path or
	// user@host:path.
	URL string `json:"url"`
	// Branch is deployed, "main" by default.
	Branch string `json:"branch"`
	// DeployDir is the directory below the home that becomes the link to
	// the live release, "htdocs" by default.
	DeployDir string `json:"deployDir"`
	// Shared are paths below a release that stay across deploys, such as
	// ".env" or "wp-content/uploads"; at most 32.
	Shared []string `json:"shared"`
	// Commands run one after the other as the site user in the new release
	// before it goes live, like cron commands; at most 20.
	Commands []string `json:"commands"`
	// Keep is how many releases stay for a rollback, 2-20.
	Keep int `json:"keep"`
	// BuildTimeoutMinutes is the time all commands may take, 1-60.
	BuildTimeoutMinutes int `json:"buildTimeoutMinutes"`
	// Submodules: fetch the repository's submodules too.
	Submodules bool `json:"submodules"`
	// Auth is "none" (public https), "token" (https with a token) or
	// "ssh" (the site's deploy key).
	Auth string `json:"auth"`
	// TokenUser is sent with the token; empty uses a fixed one.
	TokenUser string `json:"tokenUser"`
	// HasToken: a token is stored. It is never shown again.
	HasToken bool `json:"hasToken"`
	// PublicKey is the deploy key to add to the repository as read only.
	PublicKey string `json:"publicKey"`
	// HostKey is the confirmed host key of an SSH repository; empty until
	// it is confirmed.
	HostKey string `json:"hostKey"`
	// HostKeyFingerprint is the SHA-256 fingerprint of the confirmed host
	// key, to compare with the one the Git host publishes.
	HostKeyFingerprint string `json:"hostKeyFingerprint"`
	// Webhook: the webhook is on.
	Webhook bool `json:"webhook"`
	// WebhookPath is the webhook's address below the panel's origin, empty
	// while it is off; its secret is shown only when it is set.
	WebhookPath string `json:"webhookPath"`
	// UpdatedAt is RFC 3339; empty while not configured.
	UpdatedAt string `json:"updatedAt"`
}

// gitInputJSON is a new Git setting. An empty token with auth "token"
// keeps the stored one.
type gitInputJSON struct {
	URL                 string   `json:"url"`
	Branch              string   `json:"branch"`
	DeployDir           string   `json:"deployDir"`
	Shared              []string `json:"shared"`
	Commands            []string `json:"commands"`
	Keep                int      `json:"keep"`
	BuildTimeoutMinutes int      `json:"buildTimeoutMinutes"`
	Submodules          bool     `json:"submodules"`
	Auth                string   `json:"auth"`
	TokenUser           string   `json:"tokenUser"`
	// Token is write only: at most 1024 printable ASCII bytes.
	Token string `json:"token"`
}

func toGitJSON(v hosting.GitView) gitJSON {
	out := gitJSON{
		Configured: v.Configured, URL: v.URL, Branch: v.Branch, DeployDir: v.DeployDir,
		Shared: v.Shared, Commands: v.Commands, Keep: v.Keep,
		BuildTimeoutMinutes: int(v.BuildTimeout / time.Minute), Submodules: v.Submodules,
		Auth: v.Auth, TokenUser: v.TokenUser, HasToken: v.HasToken,
		PublicKey: v.PublicKey, HostKey: v.HostKey, HostKeyFingerprint: v.HostKeyFingerprint, Webhook: v.Webhook,
	}

	if v.WebhookID != "" {
		out.WebhookPath = webhookPath + v.WebhookID
	}

	if out.Shared == nil {
		out.Shared = []string{}
	}

	if out.Commands == nil {
		out.Commands = []string{}
	}

	if !v.UpdatedAt.IsZero() {
		out.UpdatedAt = v.UpdatedAt.UTC().Format(time.RFC3339)
	}

	return out
}

// gitFail answers the errors of the Git routes.
func gitFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrNoGit):
		writeError(w, http.StatusConflict, "git_unavailable", "Git deploy is not available on this panel")
	case errors.Is(err, hosting.ErrGitNotConfigured):
		writeError(w, http.StatusNotFound, "git_not_configured", "the site has no Git repository")
	case errors.Is(err, hosting.ErrNotSSH):
		writeError(w, http.StatusConflict, "not_ssh", "the site does not deploy over SSH")
	case errors.Is(err, hosting.ErrHostKeyUnreachable):
		writeError(w, http.StatusBadGateway, "unreachable", "the repository's server did not answer over SSH")
	case errors.Is(err, hosting.ErrBadHostKey):
		writeError(w, http.StatusBadRequest, "invalid_host_key", "not a usable host key (Ed25519, ECDSA or RSA with at least 3072 bits)")
	default:
		siteFail(log, w, err, what)
	}
}

// registerGit adds the routes of the Git setting of a site (ADR-0062).
// Reading needs only the site; changing it needs sites.git:write, as the
// build commands run as the site user.
func registerGit(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/sites/{name}/git", openapi.Operation{
		ID: "getSiteGit", Tag: "git", Summary: "The Git deploy setting of a site, without its token and private key", Out: gitJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		v, err := svc.Git(r.Context(), r.PathValue("name"))
		if err != nil {
			gitFail(log, w, err, "git setting")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, toGitJSON(v))
	}))

	mux.Op("PUT /api/v1/sites/{name}/git", openapi.Operation{
		ID: "setSiteGit", Tag: "git", Summary: "Set the repository, branch, shared paths and build commands of a site; SSH gets a deploy key",
		In: gitInputJSON{}, Out: gitJSON{},
	}, a.allow("sites.git:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in gitInputJSON
		if !decodeJSONLimit(w, r, &in, 256<<10) {
			return
		}

		v, err := svc.SetGit(r.Context(), p.User.ID, r.PathValue("name"), hosting.GitInput{
			URL: in.URL, Branch: in.Branch, DeployDir: in.DeployDir, Shared: in.Shared, Commands: in.Commands,
			Keep: in.Keep, BuildTimeout: time.Duration(in.BuildTimeoutMinutes) * time.Minute, Submodules: in.Submodules,
			Auth: in.Auth, TokenUser: in.TokenUser, Token: in.Token,
		})
		if err != nil {
			gitFail(log, w, err, "set git")

			return
		}

		writeJSON(w, http.StatusOK, toGitJSON(v))
	}))

	mux.Op("DELETE /api/v1/sites/{name}/git", openapi.Operation{
		ID: "deleteSiteGit", Tag: "git", Summary: "Remove the Git setting of a site with its token, deploy key and webhook; the files stay",
	}, a.allow("sites.git:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if err := svc.DeleteGit(r.Context(), p.User.ID, r.PathValue("name")); err != nil {
			gitFail(log, w, err, "delete git")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	mux.Op("POST /api/v1/sites/{name}/git/key", openapi.Operation{
		ID: "newSiteGitKey", Tag: "git", Summary: "Replace the deploy key of a site that deploys over SSH", Out: gitJSON{},
	}, a.allow("sites.git:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		v, err := svc.NewGitKey(r.Context(), p.User.ID, r.PathValue("name"))
		if err != nil {
			gitFail(log, w, err, "new git key")

			return
		}

		writeJSON(w, http.StatusOK, toGitJSON(v))
	}))

	// The scan only reads what the server presents; nothing is trusted
	// until PUT .../git/hostkey with the key the administrator confirmed.
	mux.Op("POST /api/v1/sites/{name}/git/hostkey/scan", openapi.Operation{
		ID: "scanSiteGitHostKey", Tag: "git", Summary: "Read the host key the SSH repository's server presents", Out: hostKeyJSON{},
	}, a.allow("sites.git:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		key, err := svc.ScanGitHostKey(r.Context(), r.PathValue("name"))
		if err != nil {
			gitFail(log, w, err, "scan git host key")

			return
		}

		writeJSON(w, http.StatusOK, hostKeyJSON{
			Key:         strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
			Fingerprint: ssh.FingerprintSHA256(key),
		})
	}))

	mux.Op("PUT /api/v1/sites/{name}/git/hostkey", openapi.Operation{
		ID: "setSiteGitHostKey", Tag: "git", Summary: "Trust a confirmed host key of the SSH repository's server", In: hostKeyRequest{}, Out: gitJSON{},
	}, a.allow("sites.git:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in hostKeyRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		v, err := svc.SetGitHostKey(r.Context(), p.User.ID, r.PathValue("name"), in.Key)
		if err != nil {
			gitFail(log, w, err, "set git host key")

			return
		}

		writeJSON(w, http.StatusOK, toGitJSON(v))
	}))
}
