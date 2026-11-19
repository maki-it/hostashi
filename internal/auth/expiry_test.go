// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestTokenWarnings(t *testing.T) {
	e := tokenEnv(t)

	soon := e.newToken(t, TokenRequest{Name: "prom", Scopes: []string{MetricsScope}, Days: 30})
	later := e.newToken(t, TokenRequest{Name: "prom-long", Scopes: []string{MetricsScope}, Days: 31})
	deploy := e.newToken(t, TokenRequest{Name: "deploy", Days: 5})
	forever := e.newToken(t, TokenRequest{Name: "prom-forever", Scopes: []string{MetricsScope}, Unlimited: true})

	if !slices.Equal(e.svc.TokenWarnings(soon.Token), []string{WarnExpiresSoon}) {
		t.Fatal("30 days not flagged")
	}

	for _, nt := range []NewToken{later, deploy, forever} {
		if w := e.svc.TokenWarnings(nt.Token); w != nil {
			t.Errorf("%s: %v", nt.Token.Name, w)
		}
	}

	// A day later the 31-day token is inside the window, and after its end
	// the warning stops (the token no longer works at all).
	e.clk.t = e.clk.t.Add(24 * time.Hour)

	if e.svc.TokenWarnings(later.Token) == nil {
		t.Fatal("31-day token not flagged after a day")
	}

	e.clk.t = e.clk.t.Add(31 * 24 * time.Hour)

	if w := e.svc.TokenWarnings(later.Token); w != nil {
		t.Fatalf("expired token flagged: %v", w)
	}

	revoked := soon.Token
	revoked.RevokedAt = &e.clk.t

	if w := e.svc.TokenWarnings(revoked); w != nil {
		t.Fatalf("revoked token flagged: %v", w)
	}
}

func TestMetricsTokenExpiry(t *testing.T) {
	e := tokenEnv(t)
	ctx := context.Background()

	if _, ok, err := e.svc.MetricsTokenExpiry(ctx); err != nil || ok {
		t.Fatal(ok, err)
	}

	e.newToken(t, TokenRequest{Name: "deploy", Days: 2})
	e.newToken(t, TokenRequest{Name: "forever", Scopes: []string{MetricsScope}, Unlimited: true})

	if _, ok, err := e.svc.MetricsTokenExpiry(ctx); err != nil || ok {
		t.Fatal("only unlimited or other tokens, but an end was reported", err)
	}

	a := e.newToken(t, TokenRequest{Name: "prom-a", Scopes: []string{"sites:read", MetricsScope}, Days: 90})
	b := e.newToken(t, TokenRequest{Name: "prom-b", Scopes: []string{MetricsScope}, Days: 20})

	end, ok, err := e.svc.MetricsTokenExpiry(ctx)
	if err != nil || !ok || end.Unix() != b.Token.ExpiresAt.Unix() {
		t.Fatal(end, ok, err)
	}

	if err := e.svc.RevokeToken(ctx, 1, b.Token.ID, ""); err != nil {
		t.Fatal(err)
	}

	end, ok, err = e.svc.MetricsTokenExpiry(ctx)
	if err != nil || !ok || end.Unix() != a.Token.ExpiresAt.Unix() {
		t.Fatal("revoked token still counted", end, ok, err)
	}

	e.clk.t = e.clk.t.Add(91 * 24 * time.Hour)

	if _, ok, err := e.svc.MetricsTokenExpiry(ctx); err != nil || ok {
		t.Fatal("expired token still counted", err)
	}
}
