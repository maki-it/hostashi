// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/web"
)

func TestWebApplyRefusesUnsafeWebDir(t *testing.T) {
	c, pool := startSiteAgentWith(t, &web.Reconciler{Base: filepath.Join(t.TempDir(), "web"), Driver: &web.Nginx{}})
	ctx := context.Background()

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	htdocs := filepath.Join(pool, "blog", "htdocs")
	if err := os.Symlink("/etc", filepath.Join(htdocs, "escape")); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(htdocs, "file"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{"escape", "escape/nginx", "file"} {
		_, err := agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{
			{User: "blog", Domains: []string{"blog.example.com"}, WebDir: dir},
		}})
		wantCode(t, err, agentrpc.CodeInvalidArgument)
	}

	// A folder that does not exist yet passes the check; the test driver
	// then fails to render, which is not an invalid argument.
	_, err := agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{
		{User: "blog", Domains: []string{"blog.example.com"}, WebDir: "dist"},
	}})

	var ae *agentrpc.Error
	if errors.As(err, &ae) && ae.Code == agentrpc.CodeInvalidArgument {
		t.Fatalf("missing folder refused: %v", err)
	}
}
