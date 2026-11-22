// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/maki-it/hostashi/internal/openapi"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
)

// Pools lists the storage pools of the agent (ADR-0016).
type Pools func(ctx context.Context) ([]agentapi.PoolInfo, error)

// RPCPools asks the agent.
func RPCPools(c *agentrpc.Client) Pools {
	return func(ctx context.Context) ([]agentapi.PoolInfo, error) {
		r, err := agentapi.PoolList.Call(ctx, c, agentapi.PoolListRequest{})

		return r.Pools, err
	}
}

// registerPools adds GET /api/v1/pools for the pool choice in "New site".
// Pools are added and removed only by root on the server (hostashi pool);
// the panel only reads them. Who may create sites may read them.
type poolListJSON struct {
	Pools []agentapi.PoolInfo `json:"pools"`
}

func registerPools(mux *routeMux, a authn, list Pools, log *slog.Logger) {
	mux.Op("GET /api/v1/pools", openapi.Operation{
		ID: "listPools", Tag: "system", Summary: "The storage pools for site homes", Out: poolListJSON{},
	}, a.allow("pools:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		pools, err := list(ctx)
		if err != nil {
			log.WarnContext(r.Context(), "list storage pools", "err", err)

			if errors.Is(err, context.DeadlineExceeded) {
				writeError(w, http.StatusGatewayTimeout, "agent_timeout", "the agent did not answer in time")
			} else {
				writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "the storage pools could not be read, try again")
			}

			return
		}

		if pools == nil {
			pools = []agentapi.PoolInfo{}
		}

		writeJSON(w, http.StatusOK, poolListJSON{Pools: pools})
	}))
}
