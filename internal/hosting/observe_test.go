// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestObserveApply(t *testing.T) {
	svc, ag, _ := newService(t)

	type call struct {
		ok  bool
		gen int
	}

	var got []call

	svc.ObserveApply(func(ok bool, gen int) { got = append(got, call{ok, gen}) })

	if _, err := svc.Create(t.Context(), 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	ag.webErr = errDown

	if err := svc.Sync(t.Context()); err == nil {
		t.Fatal("sync with a failing agent passed")
	}

	if len(got) != 2 || !got[0].ok || got[0].gen != 1 || got[1].ok {
		t.Fatalf("%+v", got)
	}
}

func TestCertExpiries(t *testing.T) {
	svc, _, st := newService(t)

	list, err := svc.CertExpiries(t.Context())
	if err != nil || len(list) != 0 {
		t.Fatalf("%+v %v", list, err)
	}

	for _, n := range []string{"shop", "blog"} {
		if _, err := svc.Create(t.Context(), 1, CreateRequest{Name: n, Domains: []string{n + ".example.com"}}); err != nil {
			t.Fatal(err)
		}
	}

	end := time.Date(2027, 2, 1, 0, 0, 0, 0, time.UTC)
	if err := st.SetSiteCert(t.Context(), "shop", []string{"shop.example.com"}, end, time.Now()); err != nil {
		t.Fatal(err)
	}

	if err := svc.EnablePanel("panel.example.com"); err != nil {
		t.Fatal(err)
	}

	panelEnd := end.Add(24 * time.Hour)
	if err := svc.savePanelTLS(t.Context(), PanelTLS{Domains: []string{"panel.example.com"}, NotAfter: panelEnd}); err != nil {
		t.Fatal(err)
	}

	list, err = svc.CertExpiries(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	want := []CertExpiry{{Site: "shop", Cert: "shop", NotAfter: end}, {Cert: agentapi.PanelCert, NotAfter: panelEnd}}
	if len(list) != 2 || list[0].Site != want[0].Site || list[0].Cert != want[0].Cert || !list[0].NotAfter.Equal(end) ||
		list[1].Site != "" || list[1].Cert != agentapi.PanelCert || !list[1].NotAfter.Equal(panelEnd) {
		t.Fatalf("%+v", list)
	}
}
