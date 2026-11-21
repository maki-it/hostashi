// SPDX-License-Identifier: AGPL-3.0-or-later

package certs

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// leafOpts shapes a test leaf.
type leafOpts struct {
	names  []string
	nb, na time.Time
	usage  []x509.ExtKeyUsage
	isCA   bool
}

// issue signs pub with the CA and returns the leaf in PEM.
func (ca *testCA) issue(t *testing.T, pub crypto.PublicKey, o leafOpts) []byte {
	t.Helper()

	if o.usage == nil {
		o.usage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	}

	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: o.names[0]}, DNSNames: o.names,
		NotBefore: o.nb, NotAfter: o.na, ExtKeyUsage: o.usage, IsCA: o.isCA, BasicConstraintsValid: o.isCA,
	}, ca.cert, pub, ca.key)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func (ca *testCA) pem() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw})
}

func pkcs8(t *testing.T, k any) []byte {
	t.Helper()

	der, err := x509.MarshalPKCS8PrivateKey(k)
	if err != nil {
		t.Fatal(err)
	}

	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

// trustingStore trusts ca as a root.
func trustingStore(t *testing.T, ca *testCA) *Store {
	t.Helper()

	pool := x509.NewCertPool()
	pool.AddCert(ca.cert)

	return &Store{Dir: t.TempDir(), Roots: pool}
}

func TestUploadWithKey(t *testing.T) {
	ca := newCA(t)
	s := trustingStore(t, ca)
	now := time.Now()

	key, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	leaf := ca.issue(t, &key.PublicKey, leafOpts{names: []string{"*.example.com", "example.com"}, nb: now.Add(-time.Hour), na: now.Add(20 * 24 * time.Hour)})

	// The CA first: Upload finds the leaf by its key.
	chain := append(ca.pem(), leaf...)

	info, err := s.Upload("blog", Upload{Chain: chain, Key: pkcs8(t, key), Domains: []string{"example.com", "www.example.com"}})
	if err != nil {
		t.Fatal(err)
	}

	if !info.Trusted || info.Issuer != "test CA" || len(info.Fingerprint) != 64 || !slices.Equal(info.Domains, []string{"*.example.com", "example.com"}) {
		t.Fatalf("info %+v", info)
	}

	b, err := os.ReadFile(s.CertPath("blog"))
	if err != nil || !strings.HasPrefix(string(b), string(leaf)) {
		t.Fatalf("leaf is not first: %v", err)
	}

	for _, p := range []string{s.KeyPath("blog"), s.CertPath("blog")} {
		st, err := os.Stat(p)
		if err != nil || st.Mode().Perm() != 0o600 {
			t.Fatalf("%s: %v %v", p, st, err)
		}
	}

	for d, want := range map[string]bool{"shop.example.com": true, "example.com": true, "a.b.example.com": false, "example.org": false} {
		if ok, err := s.Covers("blog", []string{d}); err != nil || ok != want {
			t.Errorf("covers %s: %v %v", d, ok, err)
		}
	}

	d, err := s.Details("blog")
	if err != nil || d.Fingerprint != info.Fingerprint || !d.Trusted {
		t.Fatalf("details %+v %v", d, err)
	}

	if err := s.Delete("blog"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Live("blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}

	if err := s.Delete("blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestUploadKeyForms(t *testing.T) {
	ca := newCA(t)
	s := trustingStore(t, ca)
	now := time.Now()
	o := leafOpts{names: []string{"blog.example.com"}, nb: now.Add(-time.Hour), na: now.Add(48 * time.Hour)}

	rsaKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}

	ecKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ecDER, _ := x509.MarshalECPrivateKey(ecKey)

	for name, tc := range map[string]struct {
		pub crypto.PublicKey
		key []byte
	}{
		"pkcs1": {&rsaKey.PublicKey, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(rsaKey)})},
		"sec1 with parameters": {&ecKey.PublicKey, append(
			pem.EncodeToMemory(&pem.Block{Type: "EC PARAMETERS", Bytes: []byte{6, 8, 42, 134, 72, 206, 61, 3, 1, 7}}),
			pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: ecDER})...)},
	} {
		if _, err := s.Upload("blog", Upload{Chain: ca.issue(t, tc.pub, o), Key: tc.key, Domains: []string{"blog.example.com"}}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}

	// The stored key is always PKCS #8.
	b, _ := os.ReadFile(s.KeyPath("blog"))
	if blk, _ := pem.Decode(b); blk == nil || blk.Type != "PRIVATE KEY" {
		t.Fatalf("stored key %q", b)
	}
}

func TestUploadRejects(t *testing.T) {
	ca := newCA(t)
	s := trustingStore(t, ca)
	now := time.Now()
	domains := []string{"blog.example.com"}

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	p224, _ := ecdsa.GenerateKey(elliptic.P224(), rand.Reader)
	_, edKey, _ := ed25519.GenerateKey(rand.Reader)

	small, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}

	ok := leafOpts{names: domains, nb: now.Add(-time.Hour), na: now.Add(30 * 24 * time.Hour)}
	good := ca.issue(t, &key.PublicKey, ok)

	with := func(f func(*leafOpts)) []byte {
		o := ok
		f(&o)

		return ca.issue(t, &key.PublicKey, o)
	}

	encrypted := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Headers: map[string]string{"Proc-Type": "4,ENCRYPTED", "DEK-Info": "AES-128-CBC,00"}, Bytes: []byte{1}})

	untrusted := newCA(t)

	for name, tc := range map[string]struct {
		u    Upload
		want error
	}{
		"other key":          {Upload{Chain: good, Key: pkcs8(t, other)}, ErrKeyMismatch},
		"expires tomorrow":   {Upload{Chain: with(func(o *leafOpts) { o.na = now.Add(23 * time.Hour) }), Key: pkcs8(t, key)}, ErrValidity},
		"expired":            {Upload{Chain: with(func(o *leafOpts) { o.nb, o.na = now.Add(-48*time.Hour), now.Add(-time.Hour) }), Key: pkcs8(t, key)}, ErrValidity},
		"not yet valid":      {Upload{Chain: with(func(o *leafOpts) { o.nb = now.Add(time.Hour) }), Key: pkcs8(t, key)}, ErrValidity},
		"client cert":        {Upload{Chain: with(func(o *leafOpts) { o.usage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth} }), Key: pkcs8(t, key)}, ErrUsage},
		"ca as leaf":         {Upload{Chain: with(func(o *leafOpts) { o.isCA = true }), Key: pkcs8(t, key)}, ErrUsage},
		"rsa 1024":           {Upload{Chain: ca.issue(t, &small.PublicKey, ok), Key: pkcs8(t, small)}, ErrKeyType},
		"p-224":              {Upload{Chain: ca.issue(t, &p224.PublicKey, ok), Key: pkcs8(t, p224)}, ErrKeyType},
		"ed25519":            {Upload{Chain: ca.issue(t, edKey.Public(), ok), Key: pkcs8(t, edKey)}, ErrKeyType},
		"encrypted":          {Upload{Chain: good, Key: encrypted}, ErrEncrypted},
		"pkcs8 encrypted":    {Upload{Chain: good, Key: pem.EncodeToMemory(&pem.Block{Type: "ENCRYPTED PRIVATE KEY", Bytes: []byte{1}})}, ErrEncrypted},
		"two keys":           {Upload{Chain: good, Key: append(pkcs8(t, key), pkcs8(t, key)...)}, ErrInvalid},
		"certificate as key": {Upload{Chain: good, Key: good}, ErrInvalid},
		"key text":           {Upload{Chain: good, Key: []byte("not a key")}, ErrInvalid},
		"key in chain":       {Upload{Chain: append(append([]byte{}, good...), pkcs8(t, key)...), Key: pkcs8(t, key)}, ErrInvalid},
		"no chain":           {Upload{Key: pkcs8(t, key)}, ErrInvalid},
		"no key, no csr":     {Upload{Chain: good}, ErrNoCustomKey},
		"too large":          {Upload{Chain: good, Key: make([]byte, 17<<10)}, ErrInvalid},
	} {
		if tc.u.Domains == nil {
			tc.u.Domains = domains
		}

		if _, err := s.Upload("blog", tc.u); !errors.Is(err, tc.want) {
			t.Errorf("%s: %v, want %v", name, err, tc.want)
		}
	}

	var missing *MissingNamesError

	_, err = s.Upload("blog", Upload{Chain: good, Key: pkcs8(t, key), Domains: []string{"blog.example.com", "shop.example.com", "www.blog.example.com"}})
	if !errors.As(err, &missing) || !slices.Equal(missing.Names, []string{"shop.example.com", "www.blog.example.com"}) {
		t.Fatalf("missing names: %v", err)
	}

	// Not publicly trusted: refused unless allowed, then marked.
	foreign := untrusted.issue(t, &key.PublicKey, ok)

	var ut *UntrustedError
	if _, err := s.Upload("blog", Upload{Chain: foreign, Key: pkcs8(t, key), Domains: domains}); !errors.As(err, &ut) {
		t.Fatalf("untrusted: %v", err)
	}

	if _, err := s.Live("blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("something was installed: %v", err)
	}

	info, err := s.Upload("blog", Upload{Chain: foreign, Key: pkcs8(t, key), Domains: domains, AllowUntrusted: true})
	if err != nil || info.Trusted {
		t.Fatalf("allowed untrusted: %+v %v", info, err)
	}

	if _, err := s.Upload("../x", Upload{Chain: good, Key: pkcs8(t, key), Domains: domains}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad name: %v", err)
	}
}

func TestUploadFromCustomCSR(t *testing.T) {
	ca := newCA(t)
	s := trustingStore(t, ca)
	now := time.Now()
	domains := []string{"blog.example.com", "www.blog.example.com"}

	if csr, err := s.PendingCSR("blog"); err != nil || csr != nil {
		t.Fatalf("pending before: %q %v", csr, err)
	}

	csr, err := s.CustomCSR("blog", domains)
	if err != nil {
		t.Fatal(err)
	}

	// An ACME round in between does not touch the custom key.
	acmeCSR, err := s.CSR("blog", domains)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.Install("blog", ca.sign(t, acmeCSR, now.Add(-time.Minute), now.Add(90*24*time.Hour))); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.PendingCSR("blog"); string(got) != string(csr) {
		t.Fatal("pending custom CSR changed")
	}

	chain := ca.sign(t, csr, now.Add(-time.Minute), now.Add(365*24*time.Hour))

	info, err := s.Upload("blog", Upload{Chain: chain, Domains: domains})
	if err != nil || !info.Trusted {
		t.Fatalf("upload %+v %v", info, err)
	}

	if got, _ := s.PendingCSR("blog"); got != nil {
		t.Fatal("pending CSR stayed")
	}

	if _, err := os.Stat(filepath.Join(s.Dir, "blog", "csr.key")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("custom key file stayed")
	}

	// Once used, the key is gone: the same chain needs a key again.
	if _, err := s.Upload("blog", Upload{Chain: chain, Domains: domains}); !errors.Is(err, ErrNoCustomKey) {
		t.Fatalf("replay: %v", err)
	}

	// Delete also drops a pending CSR.
	if _, err := s.CustomCSR("blog", domains); err != nil {
		t.Fatal(err)
	}

	if err := s.Delete("blog"); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.PendingCSR("blog"); got != nil {
		t.Fatal("pending CSR after delete")
	}
}
