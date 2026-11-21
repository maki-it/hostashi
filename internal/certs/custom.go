// SPDX-License-Identifier: AGPL-3.0-or-later

package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Own certificates (ADR-0039): uploaded with their key, or issued elsewhere
// for a key from CustomCSR. Files of a name besides the live pair:
// csr.key and csr.pem (the pending custom key and its request).

// Errors of own certificates. Each one has a fixed text for the admin; none
// contains key material.
var (
	ErrKeyType     = errors.New("certs: unsupported key (use RSA with 2048 to 8192 bits, or ECDSA P-256 or P-384)")
	ErrKeyMismatch = errors.New("certs: the key does not belong to the certificate")
	ErrNoCustomKey = errors.New("certs: no key given and no pending CSR")
	ErrEncrypted   = errors.New("certs: the key is encrypted, upload it without a passphrase")
	ErrValidity    = errors.New("certs: the certificate must be valid now and for at least one more day")
	ErrUsage       = errors.New("certs: the certificate is not for TLS servers")
)

// MinValidity is how long an own certificate must still be valid.
const MinValidity = 24 * time.Hour

// maxNames bounds the host names kept from an own certificate.
const maxNames = 100

// UntrustedError means the chain does not lead to a trusted root.
type UntrustedError struct{ Reason string }

func (e *UntrustedError) Error() string { return "certs: chain is not publicly trusted: " + e.Reason }

// MissingNamesError lists the domains the certificate does not cover.
type MissingNamesError struct{ Names []string }

func (e *MissingNamesError) Error() string {
	return "certs: the certificate does not cover " + strings.Join(e.Names, ", ")
}

// Upload is an own certificate for Store.Upload.
type Upload struct {
	Chain []byte // PEM certificates, the leaf anywhere in it
	Key   []byte // PEM private key; nil: the key of CustomCSR
	// Domains must all be covered.
	Domains []string
	// AllowUntrusted accepts a chain without a trusted root.
	AllowUntrusted bool
}

func (s *Store) roots() *x509.CertPool {
	if s.Roots != nil {
		return s.Roots
	}

	pool, err := x509.SystemCertPool()
	if err != nil {
		return x509.NewCertPool()
	}

	return pool
}

// CustomCSR creates a new pending custom key for name and returns its
// certificate signing request. It is kept apart from the ACME key, so a
// renewal cannot replace it, and kept until a certificate for it is
// uploaded or the CSR is deleted.
func (s *Store) CustomCSR(name string, domains []string) ([]byte, error) {
	dir, err := s.siteDir(name)
	if err != nil {
		return nil, err
	}

	keyPEM, csrPEM, err := newKeyAndCSR(domains)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}

	if err := writeAtomic(filepath.Join(dir, "csr.key"), keyPEM); err != nil {
		return nil, err
	}

	if err := writeAtomic(filepath.Join(dir, "csr.pem"), csrPEM); err != nil {
		return nil, err
	}

	return csrPEM, nil
}

// PendingCSR returns the pending custom CSR of name, or nil.
func (s *Store) PendingCSR(name string) ([]byte, error) {
	dir, err := s.siteDir(name)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := os.Stat(filepath.Join(dir, "csr.key")); err != nil {
		return nil, nil
	}

	b, err := os.ReadFile(filepath.Join(dir, "csr.pem"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}

	return b, nil
}

// Upload checks an own certificate and makes it the live pair of name:
// the key matches the leaf, the leaf is valid now and for MinValidity,
// is for TLS servers, covers every domain, and the chain leads to a
// trusted root unless AllowUntrusted. Nothing changes when a check fails.
func (s *Store) Upload(name string, u Upload) (Info, error) {
	dir, err := s.siteDir(name)
	if err != nil {
		return Info{}, err
	}

	if len(u.Chain) == 0 || len(u.Chain) > agentapi.MaxCertChain || len(u.Key) > agentapi.MaxCertKey {
		return Info{}, fmt.Errorf("%w: size", ErrInvalid)
	}

	if err := checkDomains(u.Domains); err != nil {
		return Info{}, err
	}

	all, _, err := parseChain(u.Chain)
	if err != nil {
		return Info{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	fromCSR := len(u.Key) == 0

	keyPEM := u.Key
	if fromCSR {
		keyPEM, err = os.ReadFile(filepath.Join(dir, "csr.key"))
		if errors.Is(err, os.ErrNotExist) {
			return Info{}, ErrNoCustomKey
		}

		if err != nil {
			return Info{}, fmt.Errorf("certs: %w", err)
		}
	}

	key, err := parseAnyKey(keyPEM)
	if err != nil {
		return Info{}, err
	}

	if err := checkKey(key); err != nil {
		return Info{}, err
	}

	chain, err := orderChain(all, key)
	if err != nil {
		return Info{}, err
	}

	info, err := s.check(chain, u.Domains, u.AllowUntrusted)
	if err != nil {
		return Info{}, err
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Info{}, fmt.Errorf("certs: %w", err)
	}

	var clean []byte
	for _, c := range chain {
		clean = append(clean, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return Info{}, fmt.Errorf("certs: %w", err)
	}

	// As in Install: nginx reads the pair only on the next reload.
	if err := writeAtomic(s.KeyPath(name), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})); err != nil {
		return Info{}, err
	}

	if err := writeAtomic(s.CertPath(name), clean); err != nil {
		return Info{}, err
	}

	if fromCSR {
		s.removeCSR(dir)
	}

	return info, nil
}

// check runs the certificate checks of Upload on a chain with the leaf
// first.
func (s *Store) check(chain []*x509.Certificate, domains []string, allowUntrusted bool) (Info, error) {
	leaf := chain[0]

	now := s.now()
	if now.Before(leaf.NotBefore) || !now.Add(MinValidity).Before(leaf.NotAfter) {
		return Info{}, ErrValidity
	}

	if leaf.IsCA || (len(leaf.ExtKeyUsage) > 0 &&
		!slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth) && !slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageAny)) {
		return Info{}, ErrUsage
	}

	names := hostNames(leaf)
	if missing := agentapi.MissingNames(names, domains); len(missing) > 0 {
		return Info{}, &MissingNamesError{Names: missing}
	}

	info := describe(chain, names)

	reason := s.verify(chain, now)
	info.Trusted = reason == ""

	if !info.Trusted && !allowUntrusted {
		return Info{}, &UntrustedError{Reason: reason}
	}

	return info, nil
}

// verify returns why chain does not lead to a trusted root at now, or "".
func (s *Store) verify(chain []*x509.Certificate, now time.Time) string {
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}

	_, err := chain[0].Verify(x509.VerifyOptions{
		Roots: s.roots(), Intermediates: inter, CurrentTime: now,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	if err == nil {
		return ""
	}

	var unknown x509.UnknownAuthorityError
	if errors.As(err, &unknown) {
		return "no trusted root for the issuer (missing intermediate certificate, internal CA or self-signed)"
	}

	return "the chain does not verify"
}

// describe fills Info for a chain with the leaf first.
func describe(chain []*x509.Certificate, names []string) Info {
	leaf := chain[0]
	sum := sha256.Sum256(leaf.Raw)

	issuer := leaf.Issuer.CommonName
	if issuer == "" && len(leaf.Issuer.Organization) > 0 {
		issuer = leaf.Issuer.Organization[0]
	}

	return Info{
		Domains: names, NotAfter: leaf.NotAfter, NotBefore: leaf.NotBefore,
		Issuer: issuer, Fingerprint: hex.EncodeToString(sum[:]),
	}
}

// hostNames returns the valid DNS names of leaf (wildcards included),
// lower case, at most maxNames. Other names cannot match a site's domain.
func hostNames(leaf *x509.Certificate) []string {
	out := []string{}

	for _, n := range leaf.DNSNames {
		n = strings.ToLower(n)

		if agentapi.ValidateDomain(strings.TrimPrefix(n, "*.")) != nil || slices.Contains(out, n) {
			continue
		}

		out = append(out, n)
		if len(out) == maxNames {
			break
		}
	}

	return out
}

// orderChain puts the certificate of key first and keeps the others in
// their order.
func orderChain(all []*x509.Certificate, key crypto.Signer) ([]*x509.Certificate, error) {
	pub, ok := key.Public().(interface{ Equal(x crypto.PublicKey) bool })
	if !ok {
		return nil, ErrKeyType
	}

	for i, c := range all {
		if pub.Equal(c.PublicKey) {
			out := append([]*x509.Certificate{c}, all[:i]...)

			return append(out, all[i+1:]...), nil
		}
	}

	return nil, ErrKeyMismatch
}

// parseAnyKey reads one private key in PKCS #8, PKCS #1 (RSA) or SEC 1
// (EC) form. EC PARAMETERS blocks are skipped; anything else is refused.
func parseAnyKey(b []byte) (crypto.Signer, error) {
	var found *pem.Block

	rest := b

	for {
		var blk *pem.Block

		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}

		switch blk.Type {
		case "EC PARAMETERS":
			continue
		case "ENCRYPTED PRIVATE KEY":
			return nil, ErrEncrypted
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			if found != nil {
				return nil, fmt.Errorf("%w: more than one key", ErrInvalid)
			}

			if _, ok := blk.Headers["DEK-Info"]; ok {
				return nil, ErrEncrypted
			}

			found = blk
		default:
			return nil, fmt.Errorf("%w: unexpected PEM block %q in the key", ErrInvalid, blk.Type)
		}
	}

	if found == nil {
		return nil, fmt.Errorf("%w: no PEM private key", ErrInvalid)
	}

	var (
		k   any
		err error
	)

	switch found.Type {
	case "RSA PRIVATE KEY":
		k, err = x509.ParsePKCS1PrivateKey(found.Bytes)
	case "EC PRIVATE KEY":
		k, err = x509.ParseECPrivateKey(found.Bytes)
	default:
		k, err = x509.ParsePKCS8PrivateKey(found.Bytes)
	}

	if err != nil {
		return nil, fmt.Errorf("%w: unreadable private key", ErrInvalid)
	}

	signer, ok := k.(crypto.Signer)
	if !ok {
		return nil, ErrKeyType
	}

	return signer, nil
}

// checkKey accepts RSA 2048 to 8192 bits and ECDSA P-256 and P-384.
// Ed25519 is refused: browsers do not offer it for TLS certificates.
func checkKey(k crypto.Signer) error {
	switch key := k.(type) {
	case *rsa.PrivateKey:
		if n := key.N.BitLen(); n < 2048 || n > 8192 {
			return ErrKeyType
		}
	case *ecdsa.PrivateKey:
		if key.Curve != elliptic.P256() && key.Curve != elliptic.P384() {
			return ErrKeyType
		}
	case ed25519.PrivateKey:
		return ErrKeyType
	default:
		return ErrKeyType
	}

	return nil
}

// Delete removes the live pair of name and a pending custom CSR. The
// caller makes sure the web server no longer uses it.
func (s *Store) Delete(name string) error {
	dir, err := s.siteDir(name)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	found := false

	for _, f := range []string{s.CertPath(name), s.KeyPath(name), filepath.Join(dir, "csr.key"), filepath.Join(dir, "csr.pem")} {
		err := os.Remove(f)
		if err == nil {
			found = true

			continue
		}

		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("certs: %w", err)
		}
	}

	if !found {
		return ErrNotFound
	}

	return nil
}

// Details returns the live certificate of name with issuer, fingerprint
// and trust, or ErrNotFound.
func (s *Store) Details(name string) (Info, error) {
	if _, err := s.siteDir(name); err != nil {
		return Info{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	chain, err := s.liveChain(name)
	if err != nil {
		return Info{}, err
	}

	info := describe(chain, hostNames(chain[0]))
	info.Trusted = s.verify(chain, s.now()) == ""

	return info, nil
}

// removeCSR drops the pending custom key after its certificate went live.
// A leftover file only keeps an old CSR on offer.
func (s *Store) removeCSR(dir string) {
	_ = os.Remove(filepath.Join(dir, "csr.key"))
	_ = os.Remove(filepath.Join(dir, "csr.pem"))
}
