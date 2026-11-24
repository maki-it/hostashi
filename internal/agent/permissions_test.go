// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

func TestSitePermissionsReset(t *testing.T) {
	c, pool := startSiteAgent(t)
	ctx := context.Background()

	_, err := agentapi.SitePermissionsReset.Call(ctx, c, agentapi.SitePermissionsResetRequest{Site: "../x"})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	_, err = agentapi.SitePermissionsReset.Call(ctx, c, agentapi.SitePermissionsResetRequest{Site: "blog"})
	wantCode(t, err, agentrpc.CodeNotFound)

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	index := filepath.Join(pool, "blog", "htdocs", "index.php")
	if err := os.WriteFile(index, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(filepath.Join(pool, "blog", "htdocs"), 0o777); err != nil {
		t.Fatal(err)
	}

	res, err := agentapi.SitePermissionsReset.Call(ctx, c, agentapi.SitePermissionsResetRequest{Site: "blog"})
	if err != nil || res.Checked != 3 || res.Changed < 2 {
		t.Fatalf("reset: %+v %v", res, err)
	}

	st, err := os.Stat(index)
	if err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("index.php: %v %v", st.Mode(), err)
	}
}
