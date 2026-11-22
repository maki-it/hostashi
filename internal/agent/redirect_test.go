// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/web"
)

// A redirect site has no account; the live web state is what makes its
// name known for certificates and logs (ADR-0054).
func TestRedirectSiteIsWebOnly(t *testing.T) {
	base := filepath.Join(t.TempDir(), "web")
	h := &siteOps{web: &web.Reconciler{Base: base}}

	if h.webOnly("old") {
		t.Fatal("known without a live state")
	}

	fakeLiveGeneration(t, base, `{"sites":[{"user":"old","domains":["old.example.com"],"redirect":{"target":"https://new.example.com","status":301}},`+
		`{"user":"blog","domains":["blog.example.com"],"root":"/x"}]}`)

	if !h.webOnly("old") || h.webOnly("blog") || h.webOnly("other") {
		t.Fatal("webOnly")
	}
}

func TestWebApplyRedirectNeedsNoAccount(t *testing.T) {
	c, _ := startSiteAgentWith(t, &web.Reconciler{Base: filepath.Join(t.TempDir(), "web"), Driver: &web.Nginx{}})
	ctx := context.Background()

	// Not "unknown site": the request gets past the account check and
	// fails only in the test driver, which cannot render.
	_, err := agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{
		{User: "old", Domains: []string{"old.example.com"}, Redirect: &agentapi.WebRedirect{Target: "https://new.example.com", Status: 301}},
	}})

	var ae *agentrpc.Error
	if err == nil || errors.As(err, &ae) && (ae.Code == agentrpc.CodeNotFound || ae.Code == agentrpc.CodeInvalidArgument) {
		t.Fatalf("redirect: %v", err)
	}

	_, err = agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{
		{User: "old", Domains: []string{"old.example.com"}, Redirect: &agentapi.WebRedirect{Target: "https://old.example.com", Status: 301}},
	}})
	wantCode(t, err, agentrpc.CodeInvalidArgument)
}
