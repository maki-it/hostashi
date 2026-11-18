// SPDX-License-Identifier: AGPL-3.0-or-later

package certs

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testCA signs certificate requests like an ACME CA would.
type testCA struct {
	key  *ecdsa.PrivateKey
	cert *x509.Certificate
}

func newCA(t *testing.T) *testCA {
	t.Helper()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}

	c, _ := x509.ParseCertificate(der)

	return &testCA{key: key, cert: c}
}

// sign returns leaf and CA as a PEM chain for csrPEM, valid from nb to na.
func (ca *testCA) sign(t *testing.T, csrPEM []byte, nb, na time.Time) []byte {
	t.Helper()

	blk, _ := pem.Decode(csrPEM)
	if blk == nil || blk.Type != "CERTIFICATE REQUEST" {
		t.Fatalf("not a CSR: %q", csrPEM)
	}

	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		t.Fatalf("csr: %v", err)
	}

	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: csr.Subject, DNSNames: csr.DNSNames,
		NotBefore: nb, NotAfter: na, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, ca.cert, csr.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}

	return append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})...)
}

func TestCSRInstallCovers(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	ca := newCA(t)
	now := time.Now()

	if _, err := s.Live("blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("live before: %v", err)
	}

	csr, err := s.CSR("blog", []string{"blog.example.com", "www.blog.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	info, err := s.Install("blog", ca.sign(t, csr, now.Add(-time.Minute), now.Add(90*24*time.Hour)))
	if err != nil {
		t.Fatal(err)
	}

	if len(info.Domains) != 2 || info.Domains[0] != "blog.example.com" {
		t.Fatalf("info %+v", info)
	}

	for _, p := range []string{s.KeyPath("blog"), s.CertPath("blog")} {
		st, err := os.Stat(p)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", p, st, err)
		}
	}

	if _, err := os.Stat(filepath.Join(s.Dir, "blog", "pending.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pending key stayed")
	}

	for domains, want := range map[string]bool{"blog.example.com": true, "www.blog.example.com": true, "new.example.com": false} {
		if ok, err := s.Covers("blog", []string{domains}); err != nil || ok != want {
			t.Errorf("covers %s: %v %v", domains, ok, err)
		}
	}

	// The same chain again: there is no pending key any more.
	if _, err := s.Install("blog", ca.sign(t, csr, now.Add(-time.Minute), now.Add(time.Hour))); !errors.Is(err, ErrNoPendingKey) {
		t.Fatalf("replay: %v", err)
	}

	// Expired: no longer covers.
	s.Now = func() time.Time { return now.Add(100 * 24 * time.Hour) }
	if ok, _ := s.Covers("blog", []string{"blog.example.com"}); ok {
		t.Fatal("expired certificate covers")
	}

	if err := s.Remove("blog"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Live("blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after remove: %v", err)
	}
}

func TestInstallRejects(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	ca := newCA(t)
	now := time.Now()

	csr, err := s.CSR("blog", []string{"blog.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	other := &Store{Dir: t.TempDir()}

	otherCSR, err := other.CSR("blog", []string{"blog.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	keyDER, _ := x509.MarshalPKCS8PrivateKey(key)

	good := ca.sign(t, csr, now.Add(-time.Minute), now.Add(time.Hour))

	for name, chain := range map[string][]byte{
		"other key":  ca.sign(t, otherCSR, now.Add(-time.Minute), now.Add(time.Hour)),
		"expired":    ca.sign(t, csr, now.Add(-2*time.Hour), now.Add(-time.Hour)),
		"not yet":    ca.sign(t, csr, now.Add(time.Hour), now.Add(2*time.Hour)),
		"empty":      nil,
		"garbage":    []byte("hello"),
		"with a key": append(append([]byte{}, good...), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})...),
	} {
		if _, err := s.Install("blog", chain); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	if _, err := s.Live("blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("something was installed: %v", err)
	}

	for _, d := range [][]string{nil, {"a.example.com;"}, {"_hostashi_control"}} {
		if _, err := s.CSR("blog", d); !errors.Is(err, ErrInvalid) {
			t.Errorf("csr %v: %v", d, err)
		}
	}

	if _, err := s.CSR("../etc", []string{"a.example.com"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad user: %v", err)
	}

	// After all that, the good chain still installs.
	if _, err := s.Install("blog", good); err != nil {
		t.Fatal(err)
	}
}
