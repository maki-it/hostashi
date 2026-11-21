// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

func TestPoolListThroughServer(t *testing.T) {
	c, pool := startSiteAgent(t)
	ctx := context.Background()

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	r, err := agentapi.PoolList.Call(ctx, c, agentapi.PoolListRequest{})
	if err != nil {
		t.Fatal(err)
	}

	if len(r.Pools) != 1 || r.Pools[0] != (agentapi.PoolInfo{Name: "default", Path: pool, Sites: 1}) {
		t.Fatalf("pools %+v", r.Pools)
	}
}

// TestPoolChangesAreRootOnly: the panel's user (any caller but root) can
// neither add nor remove a pool, whatever the request.
func TestPoolChangesAreRootOnly(t *testing.T) {
	// The admin is someone else than the test's caller.
	c, pool := startSiteAgentCfg(t, Config{adminUID: uint32(os.Getuid()) + 1})
	ctx := context.Background()

	_, err := agentapi.PoolAdd.Call(ctx, c, agentapi.PoolAddRequest{Name: "vol1", Path: "/srv/vol1"})
	wantCode(t, err, agentrpc.CodePrecondition)

	if !strings.Contains(err.Error(), "root on the server") {
		t.Errorf("error %v", err)
	}

	_, err = agentapi.PoolRemove.Call(ctx, c, agentapi.PoolRemoveRequest{Name: "vol1"})
	wantCode(t, err, agentrpc.CodePrecondition)

	if _, err := os.Lstat(filepath.Join(pool, "..", "srv")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("something was created: %v", err)
	}
}

func TestPoolAddRefusalsReachRoot(t *testing.T) {
	c, _ := startSiteAgentCfg(t, Config{adminUID: uint32(os.Getuid())})
	ctx := context.Background()

	for name, req := range map[string]agentapi.PoolAddRequest{
		"system tree": {Name: "vol1", Path: "/etc/vol1"},
		"missing":     {Name: "vol1", Path: "/nonexistent-hostashi-pool"},
	} {
		_, err := agentapi.PoolAdd.Call(ctx, c, req)
		wantCode(t, err, agentrpc.CodePrecondition)

		if !strings.Contains(err.Error(), "cannot become a storage pool") {
			t.Errorf("%s: %v", name, err)
		}
	}

	_, err := agentapi.PoolAdd.Call(ctx, c, agentapi.PoolAddRequest{Name: "default", Path: "/srv/x"})
	wantCode(t, err, agentrpc.CodeConflict)

	_, err = agentapi.PoolRemove.Call(ctx, c, agentapi.PoolRemoveRequest{Name: "nope"})
	wantCode(t, err, agentrpc.CodeNotFound)

	_, err = agentapi.PoolRemove.Call(ctx, c, agentapi.PoolRemoveRequest{Name: "default"})
	wantCode(t, err, agentrpc.CodePrecondition)

	// Bad requests never reach the agent's checks.
	for _, req := range []agentapi.PoolAddRequest{
		{Name: "Vol", Path: "/srv/v"}, {Name: "vol", Path: "srv/v"}, {Name: "vol", Path: "/srv/v w"}, {Name: "vol", Path: "/"},
	} {
		_, err := agentapi.PoolAdd.Call(ctx, c, req)
		wantCode(t, err, agentrpc.CodeInvalidArgument)
	}
}
