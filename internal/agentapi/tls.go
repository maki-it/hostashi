// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// PanelCert names the certificate of the panel's own address (ADR-0023).
// The underscore makes it impossible as a site user.
const PanelCert = "_panel"

// ParkPrefix starts the names of parked domains: "_park-<n>" names the
// config file and the certificate of one (ADR-0027). The underscore makes
// it impossible as a site user.
const ParkPrefix = "_park-"

var parkNameRe = regexp.MustCompile(`^_park-[1-9][0-9]{0,9}$`)

// ParkName is the name of parked domain n.
func ParkName(n int64) string { return ParkPrefix + strconv.FormatInt(n, 10) }

// ValidateParkName accepts "_park-<n>".
func ValidateParkName(name string) error {
	if !parkNameRe.MatchString(name) {
		return fmt.Errorf("parked name %q: must match %s", name, parkNameRe)
	}

	return nil
}

// ValidateCertName accepts a site user, PanelCert or a ParkName.
func ValidateCertName(name string) error {
	if name == PanelCert || parkNameRe.MatchString(name) {
		return nil
	}

	return ValidateSiteUser(name)
}

// MaxCertChain bounds the PEM chain of tls.install (a Let's Encrypt chain
// is about 4 KiB; one frame is at most agentrpc.MaxFrame).
const MaxCertChain = 32 << 10

// TLSCSRRequest asks for a new private key for a site and a certificate
// signing request for its domains. The key stays with the agent.
type TLSCSRRequest struct {
	User    string   `json:"user"`    // a site user or PanelCert
	Domains []string `json:"domains"` // the first one becomes the common name
}

// Validate checks the request.
func (r TLSCSRRequest) Validate() error {
	if err := ValidateCertName(r.User); err != nil {
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
	if err := ValidateCertName(r.User); err != nil {
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
