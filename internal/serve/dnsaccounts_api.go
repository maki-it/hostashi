// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/dnsprov"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
)

// dnsCheckTimeout bounds an access check: one record created and deleted
// for each zone of an account.
const dnsCheckTimeout = 2 * time.Minute

// dnsAccountJSON is a DNS account in the API (ADR-0076). Secret fields
// never leave the panel; settings holds the fields that are no secret.
type dnsAccountJSON struct {
	ID        int64             `json:"id"`
	Label     string            `json:"label"`
	Provider  string            `json:"provider"`
	Zones     []string          `json:"zones"`
	Settings  map[string]string `json:"settings"`
	State     string            `json:"state"` // "ok", "error" or "unchecked"
	LastError string            `json:"lastError,omitempty"`
	CheckedAt *time.Time        `json:"checkedAt,omitempty"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}

type dnsAccountListJSON struct {
	Accounts []dnsAccountJSON `json:"accounts"`
}

type dnsAccountAddRequest struct {
	Label    string   `json:"label"`
	Provider string   `json:"provider"`
	Zones    []string `json:"zones"`
	// Credentials are the provider's fields (GET /api/v1/dns-providers).
	Credentials map[string]string `json:"credentials"`
}

type dnsAccountUpdateRequest struct {
	Label string   `json:"label"`
	Zones []string `json:"zones"`
	// Credentials replace every field when set; left out, the stored
	// ones stay.
	Credentials map[string]string `json:"credentials,omitempty"`
}

type dnsProviderListJSON struct {
	Providers []dnsprov.Provider `json:"providers"`
}

func toDNSAccountJSON(v hosting.DNSAccountView) dnsAccountJSON {
	out := dnsAccountJSON{
		ID: v.ID, Label: v.Label, Provider: v.Provider, Zones: v.Zones, Settings: v.Settings,
		LastError: v.LastError, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt,
	}

	switch {
	case v.CheckedAt.IsZero():
		out.State = "unchecked"
	case v.LastError != "":
		out.State = "error"
	default:
		out.State = "ok"
	}

	if !v.CheckedAt.IsZero() {
		t := v.CheckedAt
		out.CheckedAt = &t
	}

	return out
}

func dnsAccountFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrDNSAccountNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such DNS account")
	case errors.Is(err, hosting.ErrDNSAccountExists):
		writeError(w, http.StatusConflict, "dns_account_exists", "a DNS account with this label exists")
	case errors.Is(err, hosting.ErrDNSZoneTaken):
		writeError(w, http.StatusConflict, "dns_zone_taken", err.Error())
	case errors.Is(err, hosting.ErrDNSAccountsOff):
		writeError(w, http.StatusConflict, "dns_accounts_unavailable", "DNS accounts are not available on this panel")
	default:
		siteFail(log, w, err, what)
	}
}

// dnsAccountID reads {id}; a malformed one is a missing account.
func dnsAccountID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusNotFound, "not_found", "no such DNS account")

		return 0, false
	}

	return id, true
}

// registerDNSAccounts adds the DNS account routes (ADR-0076): admins only.
func registerDNSAccounts(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/dns-providers", openapi.Operation{
		ID: "listDNSProviders", Tag: "dns-accounts", Summary: "List the DNS providers and their credential fields", Out: dnsProviderListJSON{},
	}, a.allow("dns_accounts:read", func(w http.ResponseWriter, _ *http.Request, _ auth.Principal) {
		writeJSON(w, http.StatusOK, dnsProviderListJSON{Providers: dnsprov.All()})
	}))

	mux.Op("GET /api/v1/dns-accounts", openapi.Operation{
		ID: "listDNSAccounts", Tag: "dns-accounts", Summary: "List the DNS accounts", Out: dnsAccountListJSON{},
	}, a.allow("dns_accounts:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		list, err := svc.DNSAccountList(r.Context())
		if err != nil {
			dnsAccountFail(log, w, err, "list DNS accounts")

			return
		}

		out := make([]dnsAccountJSON, 0, len(list))
		for _, v := range list {
			out = append(out, toDNSAccountJSON(v))
		}

		writeJSON(w, http.StatusOK, dnsAccountListJSON{Accounts: out})
	}))

	mux.Op("POST /api/v1/dns-accounts", openapi.Operation{
		ID: "addDNSAccount", Tag: "dns-accounts", Summary: "Add a DNS account and check its access", In: dnsAccountAddRequest{}, Out: dnsAccountJSON{}, Status: http.StatusCreated,
	}, a.allow("dns_accounts:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in dnsAccountAddRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), dnsCheckTimeout)
		defer cancel()

		v, err := svc.AddDNSAccount(ctx, p.User.ID, hosting.NewDNSAccount{
			Label: in.Label, Provider: in.Provider, Zones: in.Zones, Credentials: in.Credentials,
		})
		if err != nil {
			dnsAccountFail(log, w, err, "add DNS account")

			return
		}

		writeJSON(w, http.StatusCreated, toDNSAccountJSON(v))
	}))

	mux.Op("GET /api/v1/dns-accounts/{id}", openapi.Operation{
		ID: "getDNSAccount", Tag: "dns-accounts", Summary: "Read a DNS account", Out: dnsAccountJSON{},
	}, a.allow("dns_accounts:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		id, ok := dnsAccountID(w, r)
		if !ok {
			return
		}

		v, err := svc.DNSAccount(r.Context(), id)
		if err != nil {
			dnsAccountFail(log, w, err, "read DNS account")

			return
		}

		writeJSON(w, http.StatusOK, toDNSAccountJSON(v))
	}))

	mux.Op("PUT /api/v1/dns-accounts/{id}", openapi.Operation{
		ID: "updateDNSAccount", Tag: "dns-accounts", Summary: "Change a DNS account; new credentials are checked at once", In: dnsAccountUpdateRequest{}, Out: dnsAccountJSON{},
	}, a.allow("dns_accounts:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		id, ok := dnsAccountID(w, r)
		if !ok {
			return
		}

		var in dnsAccountUpdateRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), dnsCheckTimeout)
		defer cancel()

		v, err := svc.UpdateDNSAccount(ctx, p.User.ID, id, hosting.NewDNSAccount{
			Label: in.Label, Zones: in.Zones, Credentials: in.Credentials,
		})
		if err != nil {
			dnsAccountFail(log, w, err, "update DNS account")

			return
		}

		writeJSON(w, http.StatusOK, toDNSAccountJSON(v))
	}))

	mux.Op("DELETE /api/v1/dns-accounts/{id}", openapi.Operation{
		ID: "deleteDNSAccount", Tag: "dns-accounts", Summary: "Remove a DNS account",
	}, a.allow("dns_accounts:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		id, ok := dnsAccountID(w, r)
		if !ok {
			return
		}

		if err := svc.DeleteDNSAccount(r.Context(), p.User.ID, id); err != nil {
			dnsAccountFail(log, w, err, "delete DNS account")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	mux.Op("POST /api/v1/dns-accounts/{id}/check", openapi.Operation{
		ID: "checkDNSAccount", Tag: "dns-accounts", Summary: "Check that a DNS account may write its zones", Out: dnsAccountJSON{},
	}, a.allow("dns_accounts:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		id, ok := dnsAccountID(w, r)
		if !ok {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), dnsCheckTimeout)
		defer cancel()

		v, err := svc.CheckDNSAccount(ctx, id)
		if err != nil {
			dnsAccountFail(log, w, err, "check DNS account")

			return
		}

		writeJSON(w, http.StatusOK, toDNSAccountJSON(v))
	}))
}
