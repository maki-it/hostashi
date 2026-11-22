// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
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

// certTarget is what the certificate routes act on: a site, a parked
// domain or the panel. Each function gets the request for its path value;
// result is the JSON of the target after a change. del may be nil.
type certTarget struct {
	details func(ctx context.Context, r *http.Request) (agentapi.TLSInfoResponse, error)
	upload  func(ctx context.Context, r *http.Request, actor int64, in hosting.CertUpload) (any, error)
	csr     func(ctx context.Context, r *http.Request, actor int64) (string, error)
	source  func(ctx context.Context, r *http.Request, actor int64, source string) (any, error)
	renew   func(ctx context.Context, r *http.Request, actor int64) (any, error)
	del     func(ctx context.Context, r *http.Request, actor int64) (any, error)
	fail    func(log *slog.Logger, w http.ResponseWriter, err error, what string)
	// tag, noun and out name the routes in the API document: noun goes
	// into the operation ids (e.g. "Site" -> getSiteCertificate), out is
	// a value of the JSON the change routes answer with.
	tag, noun string
	out       any
}

type csrJSON struct {
	CSR string `json:"csr"`
}

type certUploadRequest struct {
	Certificate    string `json:"certificate"`
	Chain          string `json:"chain"`
	Key            string `json:"key"`
	AllowUntrusted bool   `json:"allowUntrusted"`
}

type certSourceRequest struct {
	Source string `json:"source"`
}

// op is the operation record of a certificate route.
func (t certTarget) op(verb, what, summary string, in, out any) openapi.Operation {
	o := openapi.Operation{ID: verb + t.noun + what, Tag: t.tag, Summary: summary, In: in, Out: out}
	if verb == "renew" {
		o.Status = http.StatusAccepted
	}

	return o
}

// registerCertRoutes adds the certificate routes below prefix (ADR-0039).
func registerCertRoutes(mux *routeMux, a authn, prefix string, readAction, writeAction authz.Action, t certTarget, log *slog.Logger) {
	mux.Op("GET "+prefix+"/certificate", t.op("get", "Certificate", "Get the live certificate and a pending CSR", nil, certInfoJSON{}), a.allow(readAction, func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		info, err := t.details(ctx, r)
		if err != nil {
			t.fail(log, w, err, "certificate details")

			return
		}

		writeJSON(w, http.StatusOK, toCertInfoJSON(info))
	}))

	// Uploads an own certificate. Without "key", the key of the last CSR
	// is used. The key is passed to the agent and not kept by the panel.
	mux.Op("PUT "+prefix+"/certificate", t.op("upload", "Certificate", "Upload an own certificate", certUploadRequest{}, t.out), a.allow(writeAction, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in certUploadRequest
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

		out, err := t.upload(ctx, r, p.User.ID, hosting.CertUpload{Chain: chain, Key: in.Key, AllowUntrusted: in.AllowUntrusted})
		if err != nil {
			t.fail(log, w, err, "upload certificate")

			return
		}

		writeJSON(w, http.StatusOK, out)
	}))

	if t.del != nil {
		// Only with HTTPS off: nothing serves HTTPS without a certificate.
		mux.Op("DELETE "+prefix+"/certificate", t.op("delete", "Certificate", "Remove the own certificate (HTTPS off only)", nil, t.out), a.allow(writeAction, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
			defer cancel()

			out, err := t.del(ctx, r, p.User.ID)
			if err != nil {
				t.fail(log, w, err, "delete certificate")

				return
			}

			writeJSON(w, http.StatusOK, out)
		}))
	}

	// Creates a key on the server and returns a CSR for the names.
	mux.Op("POST "+prefix+"/csr", t.op("create", "CSR", "Create a key and a certificate signing request", nil, csrJSON{}), a.allow(writeAction, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		csr, err := t.csr(ctx, r, p.User.ID)
		if err != nil {
			t.fail(log, w, err, "create csr")

			return
		}

		writeJSON(w, http.StatusOK, csrJSON{CSR: csr})
	}))

	mux.Op("PUT "+prefix+"/source", t.op("set", "TLSSource", "Choose Let's Encrypt or an own certificate", certSourceRequest{}, t.out), a.allow(writeAction, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in certSourceRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		out, err := t.source(r.Context(), r, p.User.ID, in.Source)
		if err != nil {
			t.fail(log, w, err, "set certificate source")

			return
		}

		writeJSON(w, http.StatusOK, out)
	}))

	// Asks for a new Let's Encrypt certificate now; it comes in the
	// background like the first one.
	mux.Op("POST "+prefix+"/renew", t.op("renew", "Certificate", "Ask for a new Let's Encrypt certificate now", nil, t.out), a.allow(writeAction, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		out, err := t.renew(r.Context(), r, p.User.ID)
		if err != nil {
			t.fail(log, w, err, "renew certificate")

			return
		}

		writeJSON(w, http.StatusAccepted, out)
	}))
}

// registerCerts adds the certificate routes of sites, parked domains and
// the panel address (ADR-0039).
func registerCerts(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	site := func(r *http.Request) string { return r.PathValue("name") }
	siteOut := func(s store.Site, err error) (any, error) {
		if err != nil {
			return nil, err
		}

		return toSiteJSON(svc, s), nil
	}

	registerCertRoutes(mux, a, "/api/v1/sites/{name}/tls", "sites:read", "sites.tls:write", certTarget{
		details: func(ctx context.Context, r *http.Request) (agentapi.TLSInfoResponse, error) {
			return svc.CertDetails(ctx, site(r))
		},
		upload: func(ctx context.Context, r *http.Request, actor int64, in hosting.CertUpload) (any, error) {
			return siteOut(svc.UploadCert(ctx, actor, site(r), in))
		},
		csr: func(ctx context.Context, r *http.Request, actor int64) (string, error) {
			return svc.CertCSR(ctx, actor, site(r))
		},
		source: func(ctx context.Context, r *http.Request, actor int64, src string) (any, error) {
			return siteOut(svc.SetTLSSource(ctx, actor, site(r), src))
		},
		renew: func(ctx context.Context, r *http.Request, actor int64) (any, error) {
			return siteOut(svc.RenewNow(ctx, actor, site(r)))
		},
		del: func(ctx context.Context, r *http.Request, actor int64) (any, error) {
			return siteOut(svc.DeleteCert(ctx, actor, site(r)))
		},
		fail: siteFail, tag: "sites", noun: "Site", out: siteJSON{},
	}, log)

	park := func(r *http.Request) string { return r.PathValue("domain") }
	parkOut := func(p store.Parked, err error) (any, error) {
		if err != nil {
			return nil, err
		}

		return toParkedJSON(svc, p), nil
	}

	registerCertRoutes(mux, a, "/api/v1/parked-domains/{domain}/tls", "parked:read", "parked:write", certTarget{
		details: func(ctx context.Context, r *http.Request) (agentapi.TLSInfoResponse, error) {
			return svc.ParkedCertDetails(ctx, park(r))
		},
		upload: func(ctx context.Context, r *http.Request, actor int64, in hosting.CertUpload) (any, error) {
			return parkOut(svc.UploadParkedCert(ctx, actor, park(r), in))
		},
		csr: func(ctx context.Context, r *http.Request, actor int64) (string, error) {
			return svc.ParkedCertCSR(ctx, actor, park(r))
		},
		source: func(ctx context.Context, r *http.Request, actor int64, src string) (any, error) {
			return parkOut(svc.SetParkedTLSSource(ctx, actor, park(r), src))
		},
		renew: func(ctx context.Context, r *http.Request, actor int64) (any, error) {
			return parkOut(svc.RenewParkedNow(ctx, actor, park(r)))
		},
		del: func(ctx context.Context, r *http.Request, actor int64) (any, error) {
			return parkOut(svc.DeleteParkedCert(ctx, actor, park(r)))
		},
		fail: pageFail, tag: "parked", noun: "Parked", out: parkedJSON{},
	}, log)

	panelOut := func(p hosting.PanelTLS, err error) (any, error) {
		if err != nil {
			return nil, err
		}

		return toPanelTLSJSON(p), nil
	}

	// The panel has no delete: going back to Let's Encrypt replaces an own
	// certificate once the new one is there.
	registerCertRoutes(mux, a, "/api/v1/panel/tls", "panel.tls:read", "panel.tls:write", certTarget{
		details: func(ctx context.Context, _ *http.Request) (agentapi.TLSInfoResponse, error) {
			return svc.PanelCertDetails(ctx)
		},
		upload: func(ctx context.Context, _ *http.Request, actor int64, in hosting.CertUpload) (any, error) {
			return panelOut(svc.UploadPanelCert(ctx, actor, in))
		},
		csr: func(ctx context.Context, _ *http.Request, actor int64) (string, error) {
			return svc.PanelCertCSR(ctx, actor)
		},
		source: func(ctx context.Context, _ *http.Request, actor int64, src string) (any, error) {
			return panelOut(svc.SetPanelTLSSource(ctx, actor, src))
		},
		renew: func(ctx context.Context, _ *http.Request, actor int64) (any, error) {
			return panelOut(svc.RenewPanelNow(ctx, actor))
		},
		fail: siteFail, tag: "panel", noun: "Panel", out: panelTLSJSON{},
	}, log)
}
