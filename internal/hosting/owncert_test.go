// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

func TestOwnCertificate(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	day := func(d int) time.Time { return time.Unix(0, 0).Add(time.Duration(d) * 24 * time.Hour) }
	now := day(1000)
	s.now = func() time.Time { return now }

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com", "www.blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	// No ACME client needed for an own certificate.
	_, err := s.UploadCert(ctx, 1, "blog", CertUpload{Chain: "own blog.example.com 1365", Key: "key"})

	var ae *agentrpc.Error
	if !errors.As(err, &ae) || !strings.Contains(ae.Message, "www.blog.example.com") {
		t.Fatalf("missing name: %v", err)
	}

	site, err := s.UploadCert(ctx, 1, "blog", CertUpload{Chain: "own *.blog.example.com,blog.example.com 1365", Key: "key"})
	if err != nil || !site.TLS || site.TLSSource != store.TLSCustom || site.TLSCertSource != store.TLSCustom || !site.TLSTrusted {
		t.Fatalf("upload %+v %v", site, err)
	}

	if got := ag.live(); got != "[blog=[blog.example.com www.blog.example.com]+tls]" {
		t.Fatalf("live %s", got)
	}

	// The audit log has the fingerprint, never key material.
	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action = 'site.cert.upload' AND detail LIKE '%ab12%' AND detail NOT LIKE ?`, "%key%", &n); err != nil || n != 1 {
		t.Fatalf("audit %d %v", n, err)
	}

	// The renewal loop leaves an own certificate alone, even near expiry.
	iss := &fakeIssuer{validDays: 1500}
	s.EnableTLS(iss)

	now = day(1360)

	s.RenewDue(ctx)

	if iss.calls != 0 {
		t.Fatal("own certificate renewed")
	}

	// Renew now is for Let's Encrypt only.
	if _, err := s.RenewNow(ctx, 1, "blog"); !errors.Is(err, ErrNotLetsEncrypt) {
		t.Fatalf("renew custom: %v", err)
	}

	// A domain the certificate does not cover is refused; a covered one is fine.
	if _, err := s.SetDomains(ctx, 1, "blog", []string{"blog.example.com", "shop.example.com"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("uncovered domain: %v", err)
	}

	if _, err := s.SetDomains(ctx, 1, "blog", []string{"blog.example.com", "shop.blog.example.com"}); err != nil {
		t.Fatal(err)
	}

	// Back to Let's Encrypt: the own certificate serves until the new one is in.
	if site, err := s.SetTLSSource(ctx, 1, "blog", store.TLSLetsEncrypt); err != nil || site.TLSCertSource != store.TLSCustom {
		t.Fatalf("source %+v %v", site, err)
	}

	if got := ag.live(); !strings.HasSuffix(got, "+tls]") {
		t.Fatalf("HTTPS dropped while switching: %s", got)
	}

	s.RenewDue(ctx)

	site, _ = s.Get(ctx, "blog")
	if iss.calls != 1 || site.TLSCertSource != store.TLSLetsEncrypt || !site.TLSNotAfter.Equal(day(1500)) {
		t.Fatalf("after switch: calls %d site %+v", iss.calls, site)
	}

	// Renew now: once per RenewGap, also for a fresh certificate.
	if _, err := s.RenewNow(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RenewNow(ctx, 1, "blog"); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("second renew: %v", err)
	}

	s.RenewDue(ctx)

	if site, _ = s.Get(ctx, "blog"); iss.calls != 2 || site.TLSRenewForced {
		t.Fatalf("forced renewal: calls %d site %+v", iss.calls, site)
	}

	s.RenewDue(ctx)

	if iss.calls != 2 {
		t.Fatal("renewed again without a request")
	}

	if _, err := s.SetTLSSource(ctx, 1, "blog", "selfmade"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad source: %v", err)
	}

	// Delete only with HTTPS off.
	if _, err := s.DeleteCert(ctx, 1, "blog"); !errors.Is(err, ErrCertInUse) {
		t.Fatalf("delete while on: %v", err)
	}

	if _, err := s.SetTLS(ctx, 1, "blog", false); err != nil {
		t.Fatal(err)
	}

	site, err = s.DeleteCert(ctx, 1, "blog")
	if err != nil || site.TLSCertSource != "" || !site.TLSNotAfter.IsZero() || len(ag.certDeletes) != 1 {
		t.Fatalf("delete %+v %v %v", site, err, ag.certDeletes)
	}
}

func TestOwnCertificateFromCSR(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()
	s.now = func() time.Time { return time.Unix(0, 0).Add(1000 * 24 * time.Hour) }

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	// Without a CSR, an upload without a key is refused by the agent.
	if _, err := s.UploadCert(ctx, 1, "blog", CertUpload{Chain: "own blog.example.com 1365"}); err == nil {
		t.Fatal("upload without key and CSR")
	}

	csr, err := s.CertCSR(ctx, 1, "blog")
	if err != nil || csr != "owncsr blog blog.example.com" {
		t.Fatalf("csr %q %v", csr, err)
	}

	if d, err := s.CertDetails(ctx, "blog"); err != nil || d.CSR == "" {
		t.Fatalf("details %+v %v", d, err)
	}

	// Not publicly trusted: only with the confirmation.
	if _, err := s.UploadCert(ctx, 1, "blog", CertUpload{Chain: "own blog.example.com 1365 untrusted"}); err == nil {
		t.Fatal("untrusted accepted")
	}

	site, err := s.UploadCert(ctx, 1, "blog", CertUpload{Chain: "own blog.example.com 1365 untrusted", AllowUntrusted: true})
	if err != nil || site.TLSTrusted || !strings.HasSuffix(ag.live(), "+tls]") {
		t.Fatalf("upload %+v %v %s", site, err, ag.live())
	}

	if _, err := s.CertDetails(ctx, "nosuch"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}
}

func TestOwnCertificateParked(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	day := func(d int) time.Time { return time.Unix(0, 0).Add(time.Duration(d) * 24 * time.Hour) }
	now := day(1000)
	s.now = func() time.Time { return now }

	if _, err := s.SetParked(ctx, 1, "soon.example.net", "", false); err != nil {
		t.Fatal(err)
	}

	if _, err := s.UploadParkedCert(ctx, 1, "soon.example.net", CertUpload{Chain: "own other.example.net 1365", Key: "key"}); err == nil {
		t.Fatal("certificate for another name accepted")
	}

	p, err := s.UploadParkedCert(ctx, 1, "soon.example.net", CertUpload{Chain: "own *.example.net 1365", Key: "key"})
	if err != nil || !p.TLS || p.TLSSource != store.TLSCustom || !ag.web.Parked[0].TLS {
		t.Fatalf("upload %+v %v %+v", p, err, ag.web.Parked)
	}

	// Without an ACME client, HTTPS can be turned on again with the own certificate.
	if _, err := s.SetParked(ctx, 1, "soon.example.net", "", true); err != nil {
		t.Fatalf("tls on with own certificate: %v", err)
	}

	iss := &fakeIssuer{validDays: 1500}
	s.EnableTLS(iss)

	now = day(1360)

	s.RenewDue(ctx)

	if iss.calls != 0 {
		t.Fatal("own certificate renewed")
	}

	if _, err := s.RenewParkedNow(ctx, 1, "soon.example.net"); !errors.Is(err, ErrNotLetsEncrypt) {
		t.Fatalf("renew custom: %v", err)
	}

	if _, err := s.SetParkedTLSSource(ctx, 1, "soon.example.net", store.TLSLetsEncrypt); err != nil {
		t.Fatal(err)
	}

	s.RenewDue(ctx)

	p, _ = s.ParkedDomain(ctx, "soon.example.net")
	if iss.calls != 1 || p.TLSCertSource != store.TLSLetsEncrypt {
		t.Fatalf("after switch: calls %d %+v", iss.calls, p)
	}

	if _, err := s.RenewParkedNow(ctx, 1, "soon.example.net"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RenewParkedNow(ctx, 1, "soon.example.net"); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("renew again: %v", err)
	}

	s.RenewDue(ctx)

	if iss.calls != 2 {
		t.Fatalf("forced renewal: %d", iss.calls)
	}

	if csr, err := s.ParkedCertCSR(ctx, 1, "soon.example.net"); err != nil || !strings.HasPrefix(csr, "owncsr _park-1 ") {
		t.Fatalf("csr %q %v", csr, err)
	}

	if _, err := s.DeleteParkedCert(ctx, 1, "soon.example.net"); !errors.Is(err, ErrCertInUse) {
		t.Fatalf("delete while on: %v", err)
	}

	if _, err := s.SetParked(ctx, 1, "soon.example.net", "", false); err != nil {
		t.Fatal(err)
	}

	if p, err := s.DeleteParkedCert(ctx, 1, "soon.example.net"); err != nil || p.TLSCertSource != "" || ag.certDeletes[0] != "_park-1" {
		t.Fatalf("delete %+v %v", p, err)
	}

	if _, err := s.ParkedCertDetails(ctx, "nosuch.example.net"); !errors.Is(err, ErrParkedNotFound) {
		t.Fatalf("unknown: %v", err)
	}
}

func TestOwnCertificatePanel(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	day := func(d int) time.Time { return time.Unix(0, 0).Add(time.Duration(d) * 24 * time.Hour) }
	now := day(1000)
	s.now = func() time.Time { return now }

	if _, err := s.UploadPanelCert(ctx, 1, CertUpload{Chain: "own panel.example.com 1365", Key: "key"}); !errors.Is(err, ErrNoPanel) {
		t.Fatalf("without panel address: %v", err)
	}

	if err := s.EnablePanel("panel.example.com"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.UploadPanelCert(ctx, 1, CertUpload{Chain: "own shop.example.com 1365", Key: "key"}); err == nil {
		t.Fatal("certificate for another name accepted")
	}

	p, err := s.UploadPanelCert(ctx, 1, CertUpload{Chain: "own *.example.com 1365 untrusted", Key: "key", AllowUntrusted: true})
	if err != nil || p.source() != store.TLSCustom || !p.Untrusted || !p.Live || ag.web.Panel == nil || !ag.web.Panel.TLS {
		t.Fatalf("upload %+v %v %+v", p, err, ag.web.Panel)
	}

	iss := &fakeIssuer{validDays: 1500}
	s.EnableTLS(iss)

	now = day(1360)

	s.RenewDue(ctx)

	if iss.calls != 0 {
		t.Fatal("own panel certificate renewed")
	}

	if _, err := s.RenewPanelNow(ctx, 1); !errors.Is(err, ErrNotLetsEncrypt) {
		t.Fatalf("renew custom: %v", err)
	}

	// Back to Let's Encrypt: the own certificate serves until the new one is in.
	if _, err := s.SetPanelTLSSource(ctx, 1, store.TLSLetsEncrypt); err != nil {
		t.Fatal(err)
	}

	if !ag.web.Panel.TLS {
		t.Fatal("panel lost HTTPS while switching")
	}

	s.RenewDue(ctx)

	p, _ = s.PanelTLS(ctx)
	if iss.calls != 1 || p.certSource() != store.TLSLetsEncrypt || p.Untrusted || !p.NotAfter.Equal(day(1500)) {
		t.Fatalf("after switch: calls %d %+v", iss.calls, p)
	}

	if _, err := s.RenewPanelNow(ctx, 1); err != nil {
		t.Fatal(err)
	}

	if _, err := s.RenewPanelNow(ctx, 1); !errors.Is(err, ErrTooSoon) {
		t.Fatalf("renew again: %v", err)
	}

	s.RenewDue(ctx)

	if p, _ = s.PanelTLS(ctx); iss.calls != 2 || p.RenewForced {
		t.Fatalf("forced renewal: %d %+v", iss.calls, p)
	}

	if csr, err := s.PanelCertCSR(ctx, 1); err != nil || !strings.HasPrefix(csr, "owncsr _panel panel.example.com") {
		t.Fatalf("csr %q %v", csr, err)
	}
}
