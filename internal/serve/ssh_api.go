// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/openapi"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/sshkeys"
	"github.com/maki-it/hostashi/internal/store"
)

// sshKeyJSON is one public key of a site. Times are Unix seconds.
type sshKeyJSON struct {
	ID          int64  `json:"id"`
	Label       string `json:"label"`
	Access      string `json:"access"` // "sftp" or "shell"
	Public      string `json:"public"` // type and base64, no comment
	Fingerprint string `json:"fingerprint"`
	CreatedAt   int64  `json:"createdAt"`
	UpdatedAt   int64  `json:"updatedAt"`
}

func toSSHKeyJSON(k store.SSHKey) sshKeyJSON {
	return sshKeyJSON{
		ID: k.ID, Label: k.Label, Access: k.Access, Public: k.Public, Fingerprint: k.Fingerprint,
		CreatedAt: k.CreatedAt.Unix(), UpdatedAt: k.UpdatedAt.Unix(),
	}
}

type sshKeyCreateJSON struct {
	Label  string `json:"label"`
	Access string `json:"access"` // empty: "sftp"
	Public string `json:"public"`
}

type sshKeyChangeJSON struct {
	Label  string `json:"label"`
	Access string `json:"access"`
}

func sshFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrNoSSH):
		writeError(w, http.StatusServiceUnavailable, "ssh_unavailable", "SSH access is not available on this panel")
	case errors.Is(err, hosting.ErrSSHKeyNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such SSH key")
	case errors.Is(err, hosting.ErrSSHKeyLimit):
		writeError(w, http.StatusConflict, "too_many_ssh_keys", err.Error())
	case errors.Is(err, hosting.ErrSSHKeyExists):
		writeError(w, http.StatusConflict, "ssh_key_exists", "the site already has this key")
	default:
		siteFail(log, w, err, what)
	}
}

func sshKeyID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "not_found", "no such SSH key")

		return 0, false
	}

	return id, true
}

// registerSSH adds the SSH key routes of a site (ADR-0032). Every change
// sends the site's complete list to the agent; a refused change is undone.
type sshHostKeysJSON struct {
	Keys []agentapi.SSHHostKey `json:"keys"`
}

type sshKeyListJSON struct {
	Keys []sshKeyJSON `json:"keys"`
}

func registerSSH(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	// The server's host keys, which a user compares at the first login.
	mux.Op("GET /api/v1/ssh/host-keys", openapi.Operation{
		ID: "listSSHHostKeys", Tag: "ssh", Summary: "The server's SSH host keys, to compare at the first login", Out: sshHostKeysJSON{},
	}, a.allow("ssh.hostkeys:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		keys, err := svc.SSHHostKeys(ctx)
		if err != nil {
			sshFail(log, w, err, "SSH host keys")

			return
		}

		writeJSON(w, http.StatusOK, sshHostKeysJSON{Keys: keys})
	}))

	mux.Op("GET /api/v1/sites/{name}/ssh-keys", openapi.Operation{
		ID: "listSSHKeys", Tag: "ssh", Summary: "The SSH keys of a site's login", Out: sshKeyListJSON{},
	}, a.allow("ssh:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		keys, err := svc.SSHKeys(r.Context(), r.PathValue("name"))
		if err != nil {
			sshFail(log, w, err, "list SSH keys")

			return
		}

		out := make([]sshKeyJSON, 0, len(keys))
		for _, k := range keys {
			out = append(out, toSSHKeyJSON(k))
		}

		writeJSON(w, http.StatusOK, sshKeyListJSON{Keys: out})
	}))

	mux.Op("GET /api/v1/sites/{name}/ssh-keys/{id}", openapi.Operation{
		ID: "getSSHKey", Tag: "ssh", Summary: "One SSH key of a site", Out: sshKeyJSON{},
	}, a.allow("ssh:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		id, ok := sshKeyID(w, r)
		if !ok {
			return
		}

		k, err := svc.SSHKey(r.Context(), r.PathValue("name"), id)
		if err != nil {
			sshFail(log, w, err, "get SSH key")

			return
		}

		writeJSON(w, http.StatusOK, toSSHKeyJSON(k))
	}))

	mux.Op("POST /api/v1/sites/{name}/ssh-keys", openapi.Operation{
		ID: "addSSHKey", Tag: "ssh", Summary: "Add an SSH key to a site (SFTP only by default)", In: sshKeyCreateJSON{}, Out: sshKeyJSON{}, Status: http.StatusCreated,
	}, a.allow("ssh:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in sshKeyCreateJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		k, err := svc.CreateSSHKey(ctx, p.User.ID, r.PathValue("name"), hosting.SSHKeyInput{
			Label: in.Label, Access: sshkeys.Access(in.Access), Public: in.Public,
		})
		if err != nil {
			sshFail(log, w, err, "add SSH key")

			return
		}

		writeJSON(w, http.StatusCreated, toSSHKeyJSON(k))
	}))

	// PUT changes label and access; a different key is a new entry.
	mux.Op("PUT /api/v1/sites/{name}/ssh-keys/{id}", openapi.Operation{
		ID: "updateSSHKey", Tag: "ssh", Summary: "Rename an SSH key or switch it between SFTP and shell", In: sshKeyChangeJSON{}, Out: sshKeyJSON{},
	}, a.allow("ssh:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		id, ok := sshKeyID(w, r)
		if !ok {
			return
		}

		var in sshKeyChangeJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		k, err := svc.UpdateSSHKey(ctx, p.User.ID, r.PathValue("name"), id, hosting.SSHKeyChange{
			Label: in.Label, Access: sshkeys.Access(in.Access),
		})
		if err != nil {
			sshFail(log, w, err, "change SSH key")

			return
		}

		writeJSON(w, http.StatusOK, toSSHKeyJSON(k))
	}))

	mux.Op("DELETE /api/v1/sites/{name}/ssh-keys/{id}", openapi.Operation{
		ID: "deleteSSHKey", Tag: "ssh", Summary: "Remove an SSH key from a site",
	}, a.allow("ssh:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		id, ok := sshKeyID(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.DeleteSSHKey(ctx, p.User.ID, r.PathValue("name"), id); err != nil {
			sshFail(log, w, err, "delete SSH key")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
}
