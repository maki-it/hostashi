// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maki-it/hostashi/internal/web"
)

func TestPanelACL(t *testing.T) {
	ctx := context.Background()

	var nilACL *panelACL
	if nilACL.shared() {
		t.Fatal("nil panelACL is shared")
	}

	// On its own port the firewall applies the list: nginx gets none.
	own := &panelACL{web: &web.Reconciler{Base: t.TempDir(), Driver: &web.Nginx{ListenTLS: []string{"443"}, PanelListen: []string{"8443"}}}}
	if err := own.set(ctx, []string{"192.0.2.1"}); err != nil {
		t.Fatal(err)
	}

	p := &web.Panel{Domain: "cp.example.com"}
	if own.fill(p); p.Allow != nil {
		t.Fatalf("allowlist on the panel's own port: %v", p.Allow)
	}

	base := filepath.Join(t.TempDir(), "web")
	shared := &panelACL{web: &web.Reconciler{Base: base, Driver: &web.Nginx{ListenTLS: []string{"443"}, PanelListen: []string{"443"}}}}

	// No live generation yet: the list is kept for the next web.apply.
	if err := shared.set(ctx, []string{"192.0.2.1"}); err != nil {
		t.Fatal(err)
	}

	if shared.fill(p); !slices.Equal(p.Allow, []string{"192.0.2.1"}) {
		t.Fatalf("fill: %v", p.Allow)
	}

	// The live generation has the list already: nothing to apply.
	fakeLiveGeneration(t, base, `{"panel":{"domain":"cp.example.com","allow":["192.0.2.1"]}}`)

	if err := shared.set(ctx, []string{"192.0.2.1"}); err != nil {
		t.Fatalf("same list: %v", err)
	}

	// A new list is applied; this driver has no nginx, so the apply fails
	// and the firewall change would be undone.
	if err := shared.set(ctx, []string{"198.51.100.0/24"}); err == nil {
		t.Fatal("a new list was not applied")
	}
}
