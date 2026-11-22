// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/openapi"

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

type passkeyListJSON struct {
	Passkeys []passkeyJSON `json:"passkeys"`
}

type passkeyRegisterRequest struct {
	CeremonyID string          `json:"ceremonyId"`
	Name       string          `json:"name"`
	Credential json.RawMessage `json:"credential"`
}

type passkeyLoginRequest struct {
	CeremonyID string          `json:"ceremonyId"`
	Credential json.RawMessage `json:"credential"`
}

func registerPasskeys(mux *routeMux, a authn) {
	mux.Op("POST /api/v1/auth/passkeys/register/begin", openapi.Operation{
		ID: "beginPasskeyRegistration", Tag: "passkeys", Summary: "Start adding a passkey (WebAuthn options for the browser)", Out: ceremonyJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
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

	mux.Op("POST /api/v1/auth/passkeys/register/finish", openapi.Operation{
		ID: "finishPasskeyRegistration", Tag: "passkeys", Summary: "Store the new passkey", In: passkeyRegisterRequest{}, Out: passkeyJSON{}, Status: http.StatusCreated,
	}, func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok {
			return
		}

		var in passkeyRegisterRequest
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

	mux.Op("GET /api/v1/auth/passkeys", openapi.Operation{
		ID: "listPasskeys", Tag: "passkeys", Summary: "The caller's passkeys", Out: passkeyListJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
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

		writeJSON(w, http.StatusOK, passkeyListJSON{Passkeys: out})
	})

	mux.Op("DELETE /api/v1/auth/passkeys/{id}", openapi.Operation{
		ID: "deletePasskey", Tag: "passkeys", Summary: "Remove one of the caller's passkeys",
	}, func(w http.ResponseWriter, r *http.Request) {
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

	mux.Op("POST /api/v1/auth/passkey/login/begin", openapi.Operation{
		ID: "beginPasskeyLogin", Tag: "passkeys", Summary: "Start a passkey login (WebAuthn options for the browser)", Out: ceremonyJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		id, opts, err := a.svc.PasskeyLoginBegin(clientIP(r))
		if err != nil {
			a.fail(w, err, "passkey login begin")

			return
		}

		writeJSON(w, http.StatusOK, ceremonyJSON{CeremonyID: id, Options: opts})
	})

	mux.Op("POST /api/v1/auth/passkey/login/finish", openapi.Operation{
		ID: "finishPasskeyLogin", Tag: "passkeys", Summary: "Log in with a passkey and start a browser session", In: passkeyLoginRequest{}, Out: meJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		var in passkeyLoginRequest
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
