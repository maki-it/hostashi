// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
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
	// Custom keeps the key and the CSR apart from the ACME flow, for a
	// certificate the admin gets elsewhere and hands in with tls.upload.
	Custom bool `json:"custom,omitempty"`
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
		if err := ValidateDomainPattern(d); err != nil {
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
	// The fields below are filled by tls.upload and tls.info.
	NotBefore time.Time `json:"notBefore,omitzero"`
	Issuer    string    `json:"issuer,omitempty"`
	// Fingerprint is the SHA-256 of the leaf in DER, lower-case hex.
	Fingerprint string `json:"fingerprint,omitempty"`
	// Trusted: the chain leads to a root of the system trust store.
	Trusted bool `json:"trusted,omitempty"`
}

// TLSInstall checks the chain against the pending key and makes the pair
// live for the next web.apply.
var TLSInstall = agentrpc.Op[TLSInstallRequest, TLSCertInfo]{Name: "tls.install"}

// MaxCertKey bounds the PEM private key of tls.upload (an RSA 8192 key in
// PKCS #8 is about 6.5 KiB). Chain and key together stay below one frame.
const MaxCertKey = 16 << 10

// TLSUploadRequest makes an own certificate the live pair of a site, a
// parked domain or the panel (ADR-0039). Key is the PEM private key; empty
// uses the key of the last custom CSR (TLSCSRRequest.Custom). The agent
// checks the pair, its validity, the chain and that it covers Domains.
type TLSUploadRequest struct {
	User    string   `json:"user"`
	Chain   string   `json:"chain"`
	Key     string   `json:"key,omitempty"`
	Domains []string `json:"domains"`
	// AllowUntrusted accepts a chain that does not lead to a root of the
	// system trust store (an internal CA, a self-signed certificate).
	AllowUntrusted bool `json:"allowUntrusted,omitempty"`
}

// Validate checks the request; the agent checks the certificate itself.
func (r TLSUploadRequest) Validate() error {
	if err := ValidateCertName(r.User); err != nil {
		return err
	}

	if r.Chain == "" || len(r.Chain) > MaxCertChain {
		return fmt.Errorf("chain: between 1 and %d bytes", MaxCertChain)
	}

	if len(r.Key) > MaxCertKey {
		return fmt.Errorf("key: at most %d bytes", MaxCertKey)
	}

	return TLSCSRRequest{User: r.User, Domains: r.Domains}.Validate()
}

// TLSUpload checks an own certificate and makes it live for the next
// web.apply. The key is written root 0600 and never returned.
var TLSUpload = agentrpc.Op[TLSUploadRequest, TLSCertInfo]{Name: "tls.upload"}

// TLSNameRequest names the certificate of a site, a parked domain or the
// panel.
type TLSNameRequest struct {
	User string `json:"user"`
}

// Validate checks the request.
func (r TLSNameRequest) Validate() error { return ValidateCertName(r.User) }

// TLSInfoResponse describes the live certificate (nil: none) and the
// pending custom CSR (empty: none).
type TLSInfoResponse struct {
	Cert *TLSCertInfo `json:"cert,omitempty"`
	CSR  string       `json:"csr,omitempty"`
}

// TLSInfo reads the live certificate and the pending custom CSR.
var TLSInfo = agentrpc.Op[TLSNameRequest, TLSInfoResponse]{Name: "tls.info"}

// TLSDeleteResponse is empty.
type TLSDeleteResponse struct{}

// TLSDelete removes the live pair and a pending custom CSR. It is refused
// while the last accepted web state serves the name over HTTPS, so nothing
// is ever served without its certificate.
var TLSDelete = agentrpc.Op[TLSNameRequest, TLSDeleteResponse]{Name: "tls.delete"}

// CertCovers reports whether a certificate naming names covers domain. A
// wildcard "*.example.com" covers exactly one more label in front, as
// browsers check it (RFC 6125 section 6.4.3): "a.example.com", not
// "example.com" and not "a.b.example.com".
func CertCovers(names []string, domain string) bool {
	domain = strings.ToLower(domain)

	for _, n := range names {
		n = strings.ToLower(n)
		if n == domain {
			return true
		}

		if rest, ok := strings.CutPrefix(n, "*."); ok {
			label, parent, found := strings.Cut(domain, ".")
			if found && label != "" && parent == rest {
				return true
			}
		}
	}

	return false
}

// MissingNames returns the domains that names does not cover, in order.
func MissingNames(names, domains []string) []string {
	out := []string{}

	for _, d := range domains {
		if !CertCovers(names, d) {
			out = append(out, d)
		}
	}

	return out
}
