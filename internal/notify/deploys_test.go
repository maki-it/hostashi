// SPDX-License-Identifier: AGPL-3.0-or-later

package notify

import (
	"slices"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/store"
)

func TestDeployFailed(t *testing.T) {
	ev := DeployFailed("shop", store.SiteDeploy{
		ID: 7, Source: store.DeploySourceWebhook, Commit: strings.Repeat("ab", 20), Message: "Fix cart\n\nlong body",
		Error: "a step of the deploy failed\nexit status 1",
	})

	if ev.Category != Deploys || ev.Site != "shop" || ev.Key != "deploy-failed:shop:7" || ev.Fragment != "sites/shop/git" ||
		ev.Mail.Template != "deploy-failed" || ev.Mail.Subject != "Deploy of shop failed" {
		t.Fatalf("event %+v", ev)
	}

	for _, want := range []string{"Commit: abababababab (Fix cart)", "Started by: a push (webhook)", "Error: a step of the deploy failed"} {
		if !slices.Contains(ev.Mail.Lines, want) {
			t.Errorf("missing %q in %q", want, ev.Mail.Lines)
		}
	}

	// A run that failed before the checkout has no commit line.
	ev = DeployFailed("shop", store.SiteDeploy{ID: 8, Source: "other"})
	for _, l := range ev.Mail.Lines {
		if strings.HasPrefix(l, "Commit:") || strings.HasPrefix(l, "Started by:") || strings.HasPrefix(l, "Error:") {
			t.Errorf("line %q", l)
		}
	}
}
