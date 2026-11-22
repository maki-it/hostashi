// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/openapi"
)

// Browser login for the CLI (ADR-0058 addendum 1, RFC 8628).

type deviceStartRequest struct {
	// Client names the device, e.g. "hostashi CLI on laptop"; it becomes
	// the token's name.
	Client string `json:"client"`
}

type deviceStartJSON struct {
	DeviceCode              string `json:"deviceCode"`
	UserCode                string `json:"userCode"`
	VerificationURI         string `json:"verificationUri"`
	VerificationURIComplete string `json:"verificationUriComplete"`
	ExpiresIn               int    `json:"expiresIn"` // seconds
	Interval                int    `json:"interval"`  // seconds between polls
}

type deviceRequestJSON struct {
	UserCode  string    `json:"userCode"`
	Client    string    `json:"client"`
	IP        string    `json:"ip"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type deviceApproveRequest struct {
	UserCode string `json:"userCode"`
	// Approve false denies the login; the rest is then ignored.
	Approve       bool     `json:"approve"`
	Scopes        []string `json:"scopes"`
	Sites         []string `json:"sites"` // null or missing: every site
	ExpiresInDays int      `json:"expiresInDays"`
}

type deviceTokenRequest struct {
	DeviceCode string `json:"deviceCode"`
}

// deviceLoginDays is the default lifetime of a token from a device login.
const deviceLoginDays = 90

// verificationURI is the panel's approval page: at the public URL when
// one is set, else at the address the CLI used.
func verificationURI(publicURL string, r *http.Request) string {
	base := strings.TrimRight(publicURL, "/")
	if base == "" {
		base = (&url.URL{Scheme: "https", Host: r.Host}).String()
	}

	return base + "/#/device"
}

func registerDevice(mux *routeMux, a authn, publicURL string) {
	mux.Op("POST /api/v1/auth/device", openapi.Operation{
		ID: "startDeviceLogin", Tag: "auth", Summary: "Start a browser login for the CLI: a code to approve in the panel",
		In: deviceStartRequest{}, Out: deviceStartJSON{}, Status: http.StatusCreated,
	}, func(w http.ResponseWriter, r *http.Request) {
		var in deviceStartRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		st, err := a.svc.StartDevice(r.Context(), in.Client, clientIP(r))
		if err != nil {
			a.fail(w, err, "start device login")

			return
		}

		page := verificationURI(publicURL, r)

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, deviceStartJSON{
			DeviceCode: st.DeviceCode, UserCode: st.UserCode,
			VerificationURI: page, VerificationURIComplete: page + "?code=" + url.QueryEscape(st.UserCode),
			ExpiresIn: int(st.ExpiresIn / time.Second), Interval: int(st.Interval / time.Second),
		})
	})

	// The approval page reads the login it is about to approve.
	mux.Op("GET /api/v1/auth/device/{userCode}", openapi.Operation{
		ID: "getDeviceLogin", Tag: "auth", Summary: "A pending browser login of the CLI, for the approval page", Out: deviceRequestJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok || !a.secondFactorOK(w, r, u) {
			return
		}

		d, err := a.svc.DeviceRequestOf(r.Context(), u.ID, r.PathValue("userCode"))
		if err != nil {
			a.fail(w, err, "read device login")

			return
		}

		writeJSON(w, http.StatusOK, deviceRequestJSON{UserCode: d.UserCode, Client: d.Client, IP: d.IP, CreatedAt: d.Created, ExpiresAt: d.Expires})
	})

	mux.Op("POST /api/v1/auth/device/approve", openapi.Operation{
		ID: "approveDeviceLogin", Tag: "auth", Summary: "Approve or deny a browser login of the CLI",
		In: deviceApproveRequest{},
	}, func(w http.ResponseWriter, r *http.Request) {
		u, ok := a.session(w, r)
		if !ok || !a.secondFactorOK(w, r, u) {
			return
		}

		var in deviceApproveRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		days := in.ExpiresInDays
		if days == 0 {
			days = deviceLoginDays
		}

		req := auth.TokenRequest{Scopes: in.Scopes, Sites: in.Sites, Days: days}
		if err := a.svc.ApproveDevice(r.Context(), u.ID, in.UserCode, in.Approve, req, clientIP(r)); err != nil {
			a.fail(w, err, "approve device login")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	})

	mux.Op("POST /api/v1/auth/device/token", openapi.Operation{
		ID: "pollDeviceLogin", Tag: "auth",
		Summary: "The CLI's poll: the new API token once approved, else authorization_pending, slow_down, access_denied or expired_token",
		In:      deviceTokenRequest{}, Out: tokenCreatedJSON{},
	}, func(w http.ResponseWriter, r *http.Request) {
		var in deviceTokenRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		nt, err := a.svc.PollDevice(r.Context(), in.DeviceCode)
		if err != nil {
			a.fail(w, err, "poll device login")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, a.createdToken(nt))
	})
}
