// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
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
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/certs"
	"github.com/maki-it/hostashi/internal/web"
)

// signCSR issues a one-hour certificate for csrPEM from a throwaway CA.
func signCSR(t *testing.T, csrPEM string) string {
	t.Helper()

	blk, _ := pem.Decode([]byte(csrPEM))
	if blk == nil {
		t.Fatalf("no CSR in %q", csrPEM)
	}

	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}

	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "ca"}, IsCA: true, BasicConstraintsValid: true,
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageCertSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: csr.Subject, DNSNames: csr.DNSNames,
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour),
	}, ca, csr.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestTLSOperations(t *testing.T) {
	store := &certs.Store{Dir: filepath.Join(t.TempDir(), "tls")}
	rec := &web.Reconciler{Base: filepath.Join(t.TempDir(), "web"), Driver: &web.Nginx{ListenTLS: []string{"443"}}}
	c, _ := startSiteAgentCfg(t, Config{Web: rec, Certs: store})
	ctx := context.Background()

	_, err := agentapi.TLSCSR.Call(ctx, c, agentapi.TLSCSRRequest{User: "blog", Domains: []string{"blog.example.com"}})
	wantCode(t, err, agentrpc.CodeNotFound)

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	// No certificate yet: a TLS site is refused before nginx is touched.
	_, err = agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{
		{User: "blog", Domains: []string{"blog.example.com"}, TLS: true},
	}})
	wantCode(t, err, agentrpc.CodePrecondition)

	res, err := agentapi.TLSCSR.Call(ctx, c, agentapi.TLSCSRRequest{User: "blog", Domains: []string{"blog.example.com", "www.blog.example.com"}})
	if err != nil {
		t.Fatal(err)
	}

	_, err = agentapi.TLSInstall.Call(ctx, c, agentapi.TLSInstallRequest{User: "blog", Chain: "-----BEGIN CERTIFICATE-----\nAAAA\n-----END CERTIFICATE-----\n"})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	info, err := agentapi.TLSInstall.Call(ctx, c, agentapi.TLSInstallRequest{User: "blog", Chain: signCSR(t, res.CSR)})
	if err != nil || len(info.Domains) != 2 || info.NotAfter.Before(time.Now()) {
		t.Fatalf("install %+v %v", info, err)
	}

	if ok, err := store.Covers("blog", []string{"blog.example.com", "www.blog.example.com"}); !ok || err != nil {
		t.Fatalf("covers %v %v", ok, err)
	}

	// A domain the certificate does not name is refused.
	_, err = agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{
		{User: "blog", Domains: []string{"blog.example.com", "shop.example.com"}, TLS: true},
	}})
	wantCode(t, err, agentrpc.CodePrecondition)

	// Deleting the site removes its keys.
	if _, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Live("blog"); err == nil {
		t.Fatal("certificate stayed after delete")
	}
}

func TestPanelCertificate(t *testing.T) {
	store := &certs.Store{Dir: filepath.Join(t.TempDir(), "tls")}
	rec := &web.Reconciler{Base: filepath.Join(t.TempDir(), "web"), Driver: &web.Nginx{ListenTLS: []string{"443"}, PanelListen: []string{"8443"}}}
	c, _ := startSiteAgentCfg(t, Config{Web: rec, Certs: store})
	ctx := context.Background()

	// No certificate yet: the panel with TLS is refused before nginx is touched.
	_, err := agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{}, Panel: &agentapi.WebPanel{Domain: "panel.example.com", TLS: true}})
	wantCode(t, err, agentrpc.CodePrecondition)

	// The panel's name needs no site account.
	res, err := agentapi.TLSCSR.Call(ctx, c, agentapi.TLSCSRRequest{User: agentapi.PanelCert, Domains: []string{"panel.example.com"}})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := agentapi.TLSInstall.Call(ctx, c, agentapi.TLSInstallRequest{User: agentapi.PanelCert, Chain: signCSR(t, res.CSR)}); err != nil {
		t.Fatal(err)
	}

	if ok, err := store.Covers(agentapi.PanelCert, []string{"panel.example.com"}); !ok || err != nil {
		t.Fatalf("covers %v %v", ok, err)
	}

	// Other underscore names stay invalid.
	_, err = agentapi.TLSCSR.Call(ctx, c, agentapi.TLSCSRRequest{User: "_other", Domains: []string{"panel.example.com"}})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	// A site cannot take the panel's domain.
	_, err = agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{
		Sites: []agentapi.WebSite{{User: "blog", Domains: []string{"panel.example.com"}}},
		Panel: &agentapi.WebPanel{Domain: "panel.example.com"},
	})
	wantCode(t, err, agentrpc.CodeInvalidArgument)
}

// selfSigned returns a self-signed leaf for names and its PKCS #8 key.
func selfSigned(t *testing.T, names ...string) (chain, key string) {
	t.Helper()

	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(30 * 24 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}

	keyDER, _ := x509.MarshalPKCS8PrivateKey(k)

	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
}

func TestOwnCertificateOperations(t *testing.T) {
	store := &certs.Store{Dir: filepath.Join(t.TempDir(), "tls"), Roots: x509.NewCertPool()}
	base := filepath.Join(t.TempDir(), "web")
	rec := &web.Reconciler{Base: base, Driver: &web.Nginx{ListenTLS: []string{"443"}}}
	c, _ := startSiteAgentCfg(t, Config{Web: rec, Certs: store})
	ctx := context.Background()
	domains := []string{"blog.example.com", "www.blog.example.com"}

	chain, key := selfSigned(t, "*.blog.example.com", "blog.example.com")

	_, err := agentapi.TLSUpload.Call(ctx, c, agentapi.TLSUploadRequest{User: "blog", Chain: chain, Key: key, Domains: domains})
	wantCode(t, err, agentrpc.CodeNotFound)

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	// Self-signed: refused unless allowed.
	_, err = agentapi.TLSUpload.Call(ctx, c, agentapi.TLSUploadRequest{User: "blog", Chain: chain, Key: key, Domains: domains})
	wantCode(t, err, agentrpc.CodePrecondition)

	// A name it does not cover is named in the error.
	_, err = agentapi.TLSUpload.Call(ctx, c, agentapi.TLSUploadRequest{
		User: "blog", Chain: chain, Key: key, Domains: []string{"blog.example.com", "shop.example.com"}, AllowUntrusted: true,
	})

	var rpcErr *agentrpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != agentrpc.CodePrecondition || !strings.Contains(rpcErr.Message, "shop.example.com") {
		t.Fatalf("missing name: %v", err)
	}

	// A wrong key: invalid argument, and the message holds no key material.
	_, otherKey := selfSigned(t, "blog.example.com")

	_, err = agentapi.TLSUpload.Call(ctx, c, agentapi.TLSUploadRequest{User: "blog", Chain: chain, Key: otherKey, Domains: domains, AllowUntrusted: true})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	if strings.Contains(err.Error(), "PRIVATE") {
		t.Fatalf("key in error: %v", err)
	}

	info, err := agentapi.TLSUpload.Call(ctx, c, agentapi.TLSUploadRequest{User: "blog", Chain: chain, Key: key, Domains: domains, AllowUntrusted: true})
	if err != nil || info.Trusted || info.Fingerprint == "" || info.Issuer != "*.blog.example.com" {
		t.Fatalf("upload %+v %v", info, err)
	}

	// The wildcard certificate is good enough for web.apply.
	if ok, err := store.Covers("blog", domains); !ok || err != nil {
		t.Fatalf("covers %v %v", ok, err)
	}

	// A custom CSR is kept and shown until its certificate comes.
	csr, err := agentapi.TLSCSR.Call(ctx, c, agentapi.TLSCSRRequest{User: "blog", Domains: domains, Custom: true})
	if err != nil {
		t.Fatal(err)
	}

	got, err := agentapi.TLSInfo.Call(ctx, c, agentapi.TLSNameRequest{User: "blog"})
	if err != nil || got.Cert == nil || got.Cert.Fingerprint != info.Fingerprint || got.CSR != csr.CSR {
		t.Fatalf("info %+v %v", got, err)
	}

	// While nginx serves the pair over HTTPS, it cannot be deleted.
	fakeLiveGeneration(t, base, `{"sites":[{"user":"blog","domains":["blog.example.com"],"root":"/x","cert":"/c","key":"/k"}]}`)

	_, err = agentapi.TLSDelete.Call(ctx, c, agentapi.TLSNameRequest{User: "blog"})
	wantCode(t, err, agentrpc.CodePrecondition)

	if err := os.Remove(filepath.Join(base, "current")); err != nil {
		t.Fatal(err)
	}

	if _, err := agentapi.TLSDelete.Call(ctx, c, agentapi.TLSNameRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	got, err = agentapi.TLSInfo.Call(ctx, c, agentapi.TLSNameRequest{User: "blog"})
	if err != nil || got.Cert != nil || got.CSR != "" {
		t.Fatalf("info after delete %+v %v", got, err)
	}

	_, err = agentapi.TLSDelete.Call(ctx, c, agentapi.TLSNameRequest{User: "blog"})
	wantCode(t, err, agentrpc.CodeNotFound)
}

func TestServesTLS(t *testing.T) {
	d := web.Desired{
		Sites:  []web.Site{{User: "blog", Cert: "/c"}, {User: "shop"}},
		Parked: []web.Parked{{Name: "_park-1", Cert: "/c"}, {Name: "_park-2"}},
		Panel:  &web.Panel{Domain: "panel.example.com"},
	}

	for name, want := range map[string]bool{"blog": true, "shop": false, "_park-1": true, "_park-2": false, agentapi.PanelCert: false, "other": false} {
		if got := servesTLS(d, name); got != want {
			t.Errorf("%s: %v", name, got)
		}
	}

	d.Panel.Cert = "/c"
	if !servesTLS(d, agentapi.PanelCert) {
		t.Fatal("panel")
	}
}
