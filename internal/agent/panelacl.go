// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"slices"
	"sync"

	"github.com/maki-it/hostashi/internal/web"
)

// panelACL carries the firewall's panel allowlist into nginx when the panel
// shares port 443 with the sites (ADR-0042): the packet filter cannot tell
// panel from site traffic there. On its own port the firewall applies the
// list and nginx gets none.
//
// mu serializes every web.Reconciler apply, of web.apply and of a firewall
// change, so neither undoes the other. Lock order: firewall.Manager.mu or
// siteOps.mu, then mu; mu is never held while taking another lock.
type panelACL struct {
	web *web.Reconciler

	mu    sync.Mutex
	allow []string
}

// shared reports whether the panel is served on the sites' port.
func (a *panelACL) shared() bool {
	return a != nil && a.web != nil && a.web.Driver != nil && a.web.Driver.PanelShared()
}

// fill sets the allowlist on a panel of a new desired state. Called with
// mu held.
func (a *panelACL) fill(p *web.Panel) {
	if p != nil && a.shared() {
		p.Allow = slices.Clone(a.allow)
	}
}

// set is the firewall's panel hook: it keeps allow for later web.apply
// calls and puts it into the live generation now.
func (a *panelACL) set(ctx context.Context, allow []string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.allow = slices.Clone(allow)
	if !a.shared() {
		return nil
	}

	d, ok, err := a.web.Current()
	if err != nil {
		return err
	}

	if !ok || d.Panel == nil || slices.Equal(d.Panel.Allow, a.allow) {
		return nil
	}

	d.Panel.Allow = slices.Clone(a.allow)

	_, err = a.web.Apply(ctx, d)

	return err
}
