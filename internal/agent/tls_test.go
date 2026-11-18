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
	"math/big"
	"path/filepath"
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
