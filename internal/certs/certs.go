// SPDX-License-Identifier: AGPL-3.0-or-later

// Package certs keeps the TLS keys and certificates of the sites on the
// agent side. A site's private key is created here and never leaves root:
// the panel gets a certificate signing request, obtains the certificate
// through ACME and hands back only the certificate chain.
//
// Layout below Dir (root, 0700): <site>/pending.key (a key waiting for its
// certificate), <site>/key.pem and <site>/cert.pem (the live pair, read by
// the nginx master).
package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Errors of the store.
var (
	ErrInvalid = errors.New("certs: invalid input")
	// ErrNoPendingKey means install was called without a CSR first, or the
	// certificate is for another key.
	ErrNoPendingKey = errors.New("certs: certificate does not match the pending key")
	// ErrNotFound means the site has no live certificate.
	ErrNotFound = errors.New("certs: no certificate")
)

// Store manages the files below Dir.
type Store struct {
	Dir string
	Now func() time.Time // nil: time.Now

	mu sync.Mutex
}

// Info describes a live certificate.
type Info struct {
	Domains  []string
	NotAfter time.Time
}

func (s *Store) now() time.Time {
	if s.Now == nil {
		return time.Now()
	}

	return s.Now()
}

func (s *Store) siteDir(user string) (string, error) {
	if err := agentapi.ValidateSiteUser(user); err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return filepath.Join(s.Dir, user), nil
}

// KeyPath and CertPath are the live files of a site, for the nginx config.
func (s *Store) KeyPath(user string) string  { return filepath.Join(s.Dir, user, "key.pem") }
func (s *Store) CertPath(user string) string { return filepath.Join(s.Dir, user, "cert.pem") }

// CSR creates a new pending key for the site and returns a certificate
// signing request for domains (the first one becomes the common name).
// An older pending key is replaced; the live pair is not touched.
func (s *Store) CSR(user string, domains []string) ([]byte, error) {
	dir, err := s.siteDir(user)
	if err != nil {
		return nil, err
	}

	if err := checkDomains(domains); err != nil {
		return nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}

	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: domains[0]},
		DNSNames: domains,
	}, key)
	if err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}

	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}

	if err := writeAtomic(filepath.Join(dir, "pending.key"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})); err != nil {
		return nil, err
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}), nil
}

func checkDomains(domains []string) error {
	if len(domains) == 0 || len(domains) > agentapi.MaxDomainsPerSite {
		return fmt.Errorf("%w: between 1 and %d domains required", ErrInvalid, agentapi.MaxDomainsPerSite)
	}

	for _, d := range domains {
		if err := agentapi.ValidateDomain(d); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalid, err)
		}
	}

	return nil
}

// Install checks chain (leaf first, then intermediates) against the pending
// key and makes key and chain the live pair. The leaf must be valid now and
// name only valid host names.
func (s *Store) Install(user string, chain []byte) (Info, error) {
	dir, err := s.siteDir(user)
	if err != nil {
		return Info{}, err
	}

	if len(chain) == 0 || len(chain) > agentapi.MaxCertChain {
		return Info{}, fmt.Errorf("%w: chain size", ErrInvalid)
	}

	certs, clean, err := parseChain(chain)
	if err != nil {
		return Info{}, err
	}

	leaf := certs[0]

	now := s.now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return Info{}, fmt.Errorf("%w: certificate is not valid now", ErrInvalid)
	}

	if err := checkDomains(leaf.DNSNames); err != nil {
		return Info{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	pendingPath := filepath.Join(dir, "pending.key")

	keyPEM, err := os.ReadFile(pendingPath)
	if errors.Is(err, os.ErrNotExist) {
		return Info{}, ErrNoPendingKey
	}

	if err != nil {
		return Info{}, fmt.Errorf("certs: %w", err)
	}

	key, err := parseKey(keyPEM)
	if err != nil {
		return Info{}, err
	}

	pub, ok := key.Public().(interface{ Equal(x crypto.PublicKey) bool })
	if !ok || !pub.Equal(leaf.PublicKey) {
		return Info{}, ErrNoPendingKey
	}

	// nginx reads the pair only on a reload, which only web.apply triggers.
	// A crash between the two renames leaves a mismatch that nginx -t
	// rejects; the next install repairs it.
	if err := writeAtomic(s.KeyPath(user), keyPEM); err != nil {
		return Info{}, err
	}

	if err := writeAtomic(s.CertPath(user), clean); err != nil {
		return Info{}, err
	}

	if err := os.Remove(pendingPath); err != nil {
		return Info{}, fmt.Errorf("certs: %w", err)
	}

	return Info{Domains: slices.Clone(leaf.DNSNames), NotAfter: leaf.NotAfter}, nil
}

// parseChain accepts only CERTIFICATE blocks and returns them re-encoded,
// so nothing else (no key, no text) ends up in the file nginx reads.
func parseChain(chain []byte) ([]*x509.Certificate, []byte, error) {
	var (
		out   []*x509.Certificate
		clean []byte
	)

	rest := chain

	for {
		var b *pem.Block

		b, rest = pem.Decode(rest)
		if b == nil {
			break
		}

		if b.Type != "CERTIFICATE" {
			return nil, nil, fmt.Errorf("%w: unexpected PEM block %q", ErrInvalid, b.Type)
		}

		c, err := x509.ParseCertificate(b.Bytes)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		out = append(out, c)
		clean = append(clean, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: b.Bytes})...)

		if len(out) > 5 {
			return nil, nil, fmt.Errorf("%w: chain too long", ErrInvalid)
		}
	}

	if len(out) == 0 {
		return nil, nil, fmt.Errorf("%w: no certificate in chain", ErrInvalid)
	}

	return out, clean, nil
}

func parseKey(b []byte) (crypto.Signer, error) {
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New("certs: unreadable key file")
	}

	k, err := x509.ParsePKCS8PrivateKey(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("certs: %w", err)
	}

	signer, ok := k.(crypto.Signer)
	if !ok {
		return nil, errors.New("certs: key cannot sign")
	}

	return signer, nil
}

// Live returns the live certificate of a site, or ErrNotFound.
func (s *Store) Live(user string) (Info, error) {
	if _, err := s.siteDir(user); err != nil {
		return Info{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	b, err := os.ReadFile(s.CertPath(user))
	if errors.Is(err, os.ErrNotExist) {
		return Info{}, ErrNotFound
	}

	if err != nil {
		return Info{}, fmt.Errorf("certs: %w", err)
	}

	if _, err := os.Stat(s.KeyPath(user)); err != nil {
		return Info{}, ErrNotFound
	}

	certs, _, err := parseChain(b)
	if err != nil {
		return Info{}, err
	}

	return Info{Domains: slices.Clone(certs[0].DNSNames), NotAfter: certs[0].NotAfter}, nil
}

// Covers reports whether the live certificate of a site is valid now and
// names every domain in domains.
func (s *Store) Covers(user string, domains []string) (bool, error) {
	info, err := s.Live(user)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}

	if err != nil {
		return false, err
	}

	if !s.now().Before(info.NotAfter) {
		return false, nil
	}

	for _, d := range domains {
		if !slices.Contains(info.Domains, d) {
			return false, nil
		}
	}

	return true, nil
}

// Remove deletes everything of a site.
func (s *Store) Remove(user string) error {
	dir, err := s.siteDir(user)
	if err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("certs: %w", err)
	}

	return nil
}

func writeAtomic(name string, data []byte) error {
	tmp := name + ".tmp"

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("certs: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()

		return fmt.Errorf("certs: %w", err)
	}

	if err := f.Sync(); err != nil {
		_ = f.Close()

		return fmt.Errorf("certs: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("certs: %w", err)
	}

	if err := os.Rename(tmp, name); err != nil {
		return fmt.Errorf("certs: %w", err)
	}

	return nil
}
