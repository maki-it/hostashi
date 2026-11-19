// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"time"
)

// inspectCert parses the leaf certificate of a PEM chain. It never reads the
// private key; the key file is only checked for existence and mode.
func inspectCert(pemBytes []byte, names []string, now time.Time) (TLS, []Finding) {
	var (
		t  TLS
		fs []Finding
	)

	blk, _ := pem.Decode(pemBytes)
	if blk == nil || blk.Type != "CERTIFICATE" {
		return TLS{Kind: "missing"}, []Finding{{Severity: SevReview, Code: "tls_unparsable", Msg: "certificate file holds no PEM certificate; a new certificate is needed"}}
	}

	c, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		return TLS{Kind: "missing"}, []Finding{{Severity: SevReview, Code: "tls_unparsable", Msg: "certificate does not parse: " + err.Error()}}
	}

	t.NotAfter = c.NotAfter

	t.Names = c.DNSNames
	switch {
	case containsFold(c.Issuer.Organization, "Let's Encrypt"):
		t.Kind = "letsencrypt"
	case c.Issuer.String() == c.Subject.String() && c.CheckSignature(c.SignatureAlgorithm, c.RawTBSCertificate, c.Signature) == nil:
		t.Kind = "selfsigned"
	default:
		t.Kind = "custom"
	}

	if now.After(c.NotAfter) {
		fs = append(fs, Finding{Severity: SevReview, Code: "tls_expired", Msg: "certificate expired on " + c.NotAfter.Format("2006-01-02")})
	} else if c.NotAfter.Sub(now) < 14*24*time.Hour {
		fs = append(fs, Finding{Severity: SevInfo, Code: "tls_expiring", Msg: "certificate expires within 14 days"})
	}

	for _, n := range names {
		if err := c.VerifyHostname(n); err != nil {
			fs = append(fs, Finding{Severity: SevReview, Code: "tls_name_mismatch", Msg: "certificate does not cover " + n})
		}
	}

	switch t.Kind {
	case "letsencrypt":
		fs = append(fs, Finding{Severity: SevInfo, Code: "tls_reissue", Msg: "Let's Encrypt certificate; Hostashi issues its own via ACME after cut-over, no key copy needed"})
	case "selfsigned":
		fs = append(fs, Finding{Severity: SevReview, Code: "tls_selfsigned", Msg: "self-signed certificate"})
	default:
		fs = append(fs, Finding{Severity: SevInfo, Code: "tls_custom", Msg: "custom certificate (issuer " + strings.Join(c.Issuer.Organization, ", ") + "); key must be copied on adoption"})
	}

	return t, fs
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}

	return false
}
