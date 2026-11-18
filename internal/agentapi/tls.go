// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// MaxCertChain bounds the PEM chain of tls.install (a Let's Encrypt chain
// is about 4 KiB; one frame is at most agentrpc.MaxFrame).
const MaxCertChain = 32 << 10

// TLSCSRRequest asks for a new private key for a site and a certificate
// signing request for its domains. The key stays with the agent.
type TLSCSRRequest struct {
	User    string   `json:"user"`
	Domains []string `json:"domains"` // the first one becomes the common name
}

// Validate checks the request.
func (r TLSCSRRequest) Validate() error {
	if err := ValidateSiteUser(r.User); err != nil {
		return err
	}

	if len(r.Domains) == 0 || len(r.Domains) > MaxDomainsPerSite {
		return fmt.Errorf("between 1 and %d domains required", MaxDomainsPerSite)
	}

	seen := map[string]bool{}

	for _, d := range r.Domains {
		if err := ValidateDomain(d); err != nil {
			return err
		}

		if seen[d] {
			return errors.New("domain " + d + " is listed twice")
		}

		seen[d] = true
	}

	return nil
}

// TLSCSRResponse carries the request in PEM form.
type TLSCSRResponse struct {
	CSR string `json:"csr"`
}

// TLSCSR creates the pending key of a site and returns its CSR. An older
// pending key is replaced; the live certificate is not touched.
var TLSCSR = agentrpc.Op[TLSCSRRequest, TLSCSRResponse]{Name: "tls.csr"}

// TLSInstallRequest hands the certificate chain (leaf first, PEM) for the
// pending key of a site to the agent.
type TLSInstallRequest struct {
	User  string `json:"user"`
	Chain string `json:"chain"`
}

// Validate checks the request; the agent checks the chain itself.
func (r TLSInstallRequest) Validate() error {
	if err := ValidateSiteUser(r.User); err != nil {
		return err
	}

	if r.Chain == "" || len(r.Chain) > MaxCertChain {
		return fmt.Errorf("chain: between 1 and %d bytes", MaxCertChain)
	}

	return nil
}

// TLSCertInfo describes the live certificate of a site.
type TLSCertInfo struct {
	Domains  []string  `json:"domains"`
	NotAfter time.Time `json:"notAfter"`
}

// TLSInstall checks the chain against the pending key and makes the pair
// live for the next web.apply.
var TLSInstall = agentrpc.Op[TLSInstallRequest, TLSCertInfo]{Name: "tls.install"}
