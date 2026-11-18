// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/php"
	"github.com/maki-it/hostashi/internal/web"
)

func TestWebApplyWithoutNginx(t *testing.T) {
	c, _ := startSiteAgent(t)

	_, err := agentapi.WebApply.Call(context.Background(), c, agentapi.WebApplyRequest{})
	wantCode(t, err, agentrpc.CodePrecondition)
}

// fakeLiveGeneration makes the reconciler report desired state d as live
// without running nginx.
func fakeLiveGeneration(t *testing.T, base string, desired string) {
	t.Helper()

	gen := filepath.Join(base, "generations", "1")
	if err := os.MkdirAll(gen, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(gen, "desired.json"), []byte(desired), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join("generations", "1"), filepath.Join(base, "current")); err != nil {
		t.Fatal(err)
	}
}

func TestSiteWithVirtualHostCannotBeDeleted(t *testing.T) {
	base := filepath.Join(t.TempDir(), "web")
	c, pool := startSiteAgentWith(t, &web.Reconciler{Base: base, Driver: &web.Nginx{}})
	ctx := context.Background()

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	fakeLiveGeneration(t, base, `{"sites":[{"user":"blog","domains":["blog.example.com"],"root":"/x"}]}`)

	_, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "blog"})
	wantCode(t, err, agentrpc.CodePrecondition)

	if _, err := os.Stat(filepath.Join(pool, "blog")); err != nil {
		t.Fatalf("home removed: %v", err)
	}
}

func TestWebApplyChecksSitesAndInput(t *testing.T) {
	c, _ := startSiteAgentWith(t, &web.Reconciler{Base: filepath.Join(t.TempDir(), "web"), Driver: &web.Nginx{}})
	ctx := context.Background()

	_, err := agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{
		{User: "nosuchsite", Domains: []string{"a.example.com"}},
	}})
	wantCode(t, err, agentrpc.CodeNotFound)

	// The server validates on its own, for a client that skips validation.
	// A root path is not a request field.
	raw := agentrpc.Op[rawRequest, rawResponse]{Name: "web.apply"}

	for _, body := range []string{
		`{"sites":[{"user":"blog","domains":["a.example.com;"]}]}`,
		`{"sites":[{"user":"blog","domains":["a.example.com"],"root":"/etc"}]}`,
		`{"sites":[{"user":"blog","domains":[]}]}`,
		`{"sites":[{"user":"blog","domains":["a.example.com"]},{"user":"shop","domains":["a.example.com"]}]}`,
	} {
		if _, err := raw.Call(ctx, c, rawRequest(body)); err == nil {
			t.Errorf("%s accepted", body)
		}
	}
}

func TestPHPSiteNeedsPHP(t *testing.T) {
	c, _ := startSiteAgentWith(t, &web.Reconciler{Base: filepath.Join(t.TempDir(), "web"), Driver: &web.Nginx{}})
	ctx := context.Background()

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	_, err := agentapi.WebApply.Call(ctx, c, agentapi.WebApplyRequest{Sites: []agentapi.WebSite{
		{User: "blog", Domains: []string{"blog.example.com"}, PHP: "8.4"},
	}})
	wantCode(t, err, agentrpc.CodePrecondition)

	res, err := agentapi.PHPVersionsList.Call(ctx, c, agentapi.PHPVersionsRequest{})
	if err != nil || res.Installed == nil || len(res.Installed) != 0 {
		t.Fatalf("versions %+v %v", res, err)
	}
}

func TestSiteWithPHPPoolCannotBeDeleted(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "php")
	p := &php.Manager{Dir: dir, SocketDir: "/run/hostashi-php", WebGroup: "www-data", Installed: func() []string { return []string{"8.4"} }}
	c, pool := startSiteAgentPHP(t, nil, p)
	ctx := context.Background()

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(`{"blog":{"user":"blog","version":"8.4","home":"/x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "blog"})
	wantCode(t, err, agentrpc.CodePrecondition)

	if _, err := os.Stat(filepath.Join(pool, "blog")); err != nil {
		t.Fatalf("home removed: %v", err)
	}

	res, err := agentapi.PHPVersionsList.Call(ctx, c, agentapi.PHPVersionsRequest{})
	if err != nil || len(res.Installed) != 1 || res.Installed[0] != "8.4" {
		t.Fatalf("versions %+v %v", res, err)
	}
}
