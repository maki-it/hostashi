// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
)

// maxCertBody bounds an upload: chain and key with JSON escaping.
const maxCertBody = 2 * (agentapi.MaxCertChain + agentapi.MaxCertKey)

// certJSON describes a live certificate as the agent reads it.
type certJSON struct {
	Domains     []string   `json:"domains"`
	Issuer      string     `json:"issuer"`
	NotBefore   *time.Time `json:"notBefore"`
	NotAfter    *time.Time `json:"notAfter"`
	Fingerprint string     `json:"fingerprint"` // SHA-256 of the leaf, hex
	Trusted     bool       `json:"trusted"`
}

// certInfoJSON is the live certificate (null: none) and a pending CSR.
type certInfoJSON struct {
	Certificate *certJSON `json:"certificate"`
	CSR         string    `json:"csr,omitempty"`
}

func toCertInfoJSON(in agentapi.TLSInfoResponse) certInfoJSON {
	out := certInfoJSON{CSR: in.CSR}

	if c := in.Cert; c != nil {
		d := c.Domains
		if d == nil {
			d = []string{}
		}

		out.Certificate = &certJSON{
			Domains: d, Issuer: c.Issuer, NotBefore: timeOrNil(c.NotBefore), NotAfter: timeOrNil(c.NotAfter),
			Fingerprint: c.Fingerprint, Trusted: c.Trusted,
		}
	}

	return out
}

// registerCerts adds the certificate routes of a site (ADR-0039).
func registerCerts(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.HandleFunc("GET /api/v1/sites/{name}/tls/certificate", a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		info, err := svc.CertDetails(ctx, r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "certificate details")

			return
		}

		writeJSON(w, http.StatusOK, toCertInfoJSON(info))
	}))

	// Uploads an own certificate. Without "key", the key of the last CSR
	// is used. The key is passed to the agent and not kept by the panel.
	mux.HandleFunc("PUT /api/v1/sites/{name}/tls/certificate", a.allow("sites.tls:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Certificate    string `json:"certificate"`
			Chain          string `json:"chain"`
			Key            string `json:"key"`
			AllowUntrusted bool   `json:"allowUntrusted"`
		}
		if !decodeJSONLimit(w, r, &in, maxCertBody) {
			return
		}

		chain := in.Certificate
		if in.Chain != "" {
			chain += "\n" + in.Chain
		}

		if in.Certificate == "" || len(chain) > agentapi.MaxCertChain || len(in.Key) > agentapi.MaxCertKey {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument",
				`"certificate" is required; certificate and chain together at most 32 KiB, key at most 16 KiB`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.UploadCert(ctx, p.User.ID, r.PathValue("name"), hosting.CertUpload{Chain: chain, Key: in.Key, AllowUntrusted: in.AllowUntrusted})
		if err != nil {
			siteFail(log, w, err, "upload certificate")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// Only with HTTPS off: a site never serves HTTPS without a certificate.
	mux.HandleFunc("DELETE /api/v1/sites/{name}/tls/certificate", a.allow("sites.tls:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.DeleteCert(ctx, p.User.ID, r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "delete certificate")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// Creates a key on the server and returns a CSR for the site's domains.
	mux.HandleFunc("POST /api/v1/sites/{name}/tls/csr", a.allow("sites.tls:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		csr, err := svc.CertCSR(ctx, p.User.ID, r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "create csr")

			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"csr": csr})
	}))

	mux.HandleFunc("PUT /api/v1/sites/{name}/tls/source", a.allow("sites.tls:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Source string `json:"source"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		s, err := svc.SetTLSSource(r.Context(), p.User.ID, r.PathValue("name"), in.Source)
		if err != nil {
			siteFail(log, w, err, "set certificate source")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	// Asks for a new Let's Encrypt certificate now; it comes in the
	// background like the first one.
	mux.HandleFunc("POST /api/v1/sites/{name}/tls/renew", a.allow("sites.tls:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		s, err := svc.RenewNow(r.Context(), p.User.ID, r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "renew certificate")

			return
		}

		writeJSON(w, http.StatusAccepted, toSiteJSON(svc, s))
	}))
}
