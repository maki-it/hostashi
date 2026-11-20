// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/store"
)

// maxCredentialBytes allows for authenticators that send attestation data
// although none is requested.
const maxCredentialBytes = 64 << 10

type passkeyJSON struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
}

func toPasskeyJSON(p store.Passkey) passkeyJSON {
	return passkeyJSON{ID: p.ID, Name: p.Name, CreatedAt: p.CreatedAt, LastUsedAt: p.LastUsedAt}
}

type ceremonyJSON struct {
	CeremonyID string `json:"ceremonyId"`
	Options    any    `json:"options"` // pass to navigator.credentials.create or .get
}

func registerPasskeys(mux *routeMux, a authn) {
	mux.HandleFunc("POST /api/v1/auth/passkeys/register/begin", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok {
			return
		}

		id, opts, err := a.svc.PasskeyRegisterBegin(r.Context(), u.ID)
		if err != nil {
			a.fail(w, err, "passkey register begin")

			return
		}

		writeJSON(w, http.StatusOK, ceremonyJSON{CeremonyID: id, Options: opts})
	})

	mux.HandleFunc("POST /api/v1/auth/passkeys/register/finish", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok {
			return
		}

		var in struct {
			CeremonyID string          `json:"ceremonyId"`
			Name       string          `json:"name"`
			Credential json.RawMessage `json:"credential"`
		}
		if !decodeJSONLimit(w, r, &in, maxCredentialBytes) {
			return
		}

		p, err := a.svc.PasskeyRegisterFinish(r.Context(), u.ID, in.CeremonyID, in.Name, in.Credential, clientIP(r))
		if err != nil {
			a.fail(w, err, "passkey register finish")

			return
		}

		writeJSON(w, http.StatusCreated, toPasskeyJSON(p))
	})

	mux.HandleFunc("GET /api/v1/auth/passkeys", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok {
			return
		}

		ps, err := a.svc.ListPasskeys(r.Context(), u.ID)
		if err != nil {
			a.fail(w, err, "list passkeys")

			return
		}

		out := make([]passkeyJSON, 0, len(ps))
		for _, p := range ps {
			out = append(out, toPasskeyJSON(p))
		}

		writeJSON(w, http.StatusOK, map[string][]passkeyJSON{"passkeys": out})
	})

	mux.HandleFunc("DELETE /api/v1/auth/passkeys/{id}", func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok {
			return
		}

		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil || id <= 0 {
			a.fail(w, auth.ErrPasskeyNotFound, "delete passkey")

			return
		}

		if err := a.svc.DeletePasskey(r.Context(), u.ID, id, clientIP(r)); err != nil {
			a.fail(w, err, "delete passkey")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("POST /api/v1/auth/passkey/login/begin", func(w http.ResponseWriter, r *http.Request) {
		id, opts, err := a.svc.PasskeyLoginBegin(clientIP(r))
		if err != nil {
			a.fail(w, err, "passkey login begin")

			return
		}

		writeJSON(w, http.StatusOK, ceremonyJSON{CeremonyID: id, Options: opts})
	})

	mux.HandleFunc("POST /api/v1/auth/passkey/login/finish", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			CeremonyID string          `json:"ceremonyId"`
			Credential json.RawMessage `json:"credential"`
		}
		if !decodeJSONLimit(w, r, &in, maxCredentialBytes) {
			return
		}

		token, u, err := a.svc.PasskeyLoginFinish(r.Context(), in.CeremonyID, in.Credential, clientIP(r))
		if err != nil {
			a.fail(w, err, "passkey login")

			return
		}

		setSessionCookie(w, token, int(auth.SessionTTL.Seconds()))
		a.writeLoggedIn(w, r, u)
	})
}
