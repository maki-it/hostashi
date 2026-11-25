// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestPanelTLSDoctor(t *testing.T) {
	s, _, _ := newService(t)
	ctx := t.Context()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	days := func(d int) time.Time { return now.Add(time.Duration(d)*24*time.Hour + time.Hour) }

	// No host name: the panel serves no HTTPS, nothing to report.
	if got, err := s.PanelTLSDoctor(ctx, now); err != nil || got != nil {
		t.Fatalf("without a panel domain: %+v %v", got, err)
	}

	if err := s.EnablePanel("panel.example.com"); err != nil {
		t.Fatal(err)
	}

	// Waiting for the first certificate, nothing failed yet.
	if got, err := s.PanelTLSDoctor(ctx, now); err != nil || got != nil {
		t.Fatalf("before the first certificate: %+v %v", got, err)
	}

	for _, tc := range []struct {
		name   string
		tls    PanelTLS
		status string
		want   string
	}{
		{"fine", PanelTLS{NotAfter: days(60)}, agentapi.DoctorOK, "valid until 2027-01-24"},
		{"soon", PanelTLS{NotAfter: days(20)}, agentapi.DoctorWarn, "(20 days)"},
		{"very soon", PanelTLS{NotAfter: days(3)}, agentapi.DoctorFail, "(3 days)"},
		{"expired", PanelTLS{NotAfter: now.Add(-time.Hour)}, agentapi.DoctorFail, "expired on 2026-11-25"},
		{"renewal failed", PanelTLS{NotAfter: days(60), Error: "acme: NXDOMAIN\nmore"}, agentapi.DoctorWarn, "last renewal failed: acme: NXDOMAIN"},
		{"never issued", PanelTLS{Error: "acme: timeout"}, agentapi.DoctorFail, "no certificate for panel.example.com: acme: timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.savePanelTLS(ctx, tc.tls); err != nil {
				t.Fatal(err)
			}

			got, err := s.PanelTLSDoctor(ctx, now)
			if err != nil || len(got) != 1 {
				t.Fatalf("checks %+v, %v", got, err)
			}

			c := got[0]
			if c.ID != "panel.tls" || c.Site != "" || c.Status != tc.status || !strings.Contains(c.Summary, tc.want) || strings.Contains(c.Summary, "more") {
				t.Fatalf("check %+v", c)
			}

			if (c.Status == agentapi.DoctorOK) != (c.Hint == "") || (c.Hint != "" && !strings.Contains(c.Hint, "System")) {
				t.Fatalf("hint %q for status %s", c.Hint, c.Status)
			}
		})
	}
}
