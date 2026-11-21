// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/sites"
)

var errPoolRootOnly = &agentrpc.Error{
	Code: agentrpc.CodePrecondition, Message: "storage pools are added and removed by root on the server (hostashi pool)",
}

// registerPools adds the pool operations (ADR-0016, spike S9). Adding and
// removing take a path and change what every site unit hides, so only root
// on the server may call them, never the panel.
func (h *siteOps) registerPools(s *agentrpc.Server, adminUID uint32) {
	agentrpc.Handle(s, agentapi.PoolList, func(context.Context, agentrpc.Caller, agentapi.PoolListRequest) (agentapi.PoolListResponse, error) {
		return agentapi.PoolListResponse{Pools: h.poolInfos()}, nil
	})

	agentrpc.Handle(s, agentapi.PoolAdd, func(ctx context.Context, c agentrpc.Caller, req agentapi.PoolAddRequest) (agentapi.PoolInfo, error) {
		if c.UID != adminUID {
			return agentapi.PoolInfo{}, errPoolRootOnly
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		p, err := h.m.AddPool(req.Name, req.Path)
		if err != nil {
			return agentapi.PoolInfo{}, mapPoolError(err)
		}

		h.refreshUnits(ctx)

		return agentapi.PoolInfo{Name: p.Name, Path: p.Path}, nil
	})

	agentrpc.Handle(s, agentapi.PoolRemove, func(ctx context.Context, c agentrpc.Caller, req agentapi.PoolRemoveRequest) (agentapi.PoolRemoveResponse, error) {
		if c.UID != adminUID {
			return agentapi.PoolRemoveResponse{}, errPoolRootOnly
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		if err := h.m.RemovePool(req.Name); err != nil {
			return agentapi.PoolRemoveResponse{}, mapPoolError(err)
		}

		h.refreshUnits(ctx)

		return agentapi.PoolRemoveResponse{}, nil
	})
}

func (h *siteOps) poolInfos() []agentapi.PoolInfo {
	count := map[string]int{}
	for _, s := range h.m.List() {
		count[s.Pool]++
	}

	out := []agentapi.PoolInfo{}
	for _, p := range h.m.Pools() {
		out = append(out, agentapi.PoolInfo{Name: p.Name, Path: p.Path, Sites: count[p.Name]})
	}

	return out
}

// refreshUnits writes the site units again so they hide the changed set of
// pools: the PHP masters restart, cron units are rendered anew. The file
// manager's drop-in follows at its next start. Failures are logged; the
// pool change itself stands.
func (h *siteOps) refreshUnits(ctx context.Context) {
	if h.php != nil {
		if err := h.php.Refresh(ctx); err != nil {
			h.log.ErrorContext(ctx, "refresh PHP units after a pool change", "err", err)
		}
	}

	if h.cron != nil {
		if err := h.cron.Refresh(ctx, h.cronSite); err != nil {
			h.log.ErrorContext(ctx, "refresh cron units after a pool change", "err", err)
		}
	}
}

func mapPoolError(err error) error {
	var msg string

	switch {
	case errors.Is(err, sites.ErrPoolExists):
		return &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "a pool with this name already exists"}
	case errors.Is(err, sites.ErrNotFound):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown pool"}
	case errors.Is(err, sites.ErrPoolInUse):
		msg = err.Error()
	case errors.Is(err, sites.ErrPoolRefused):
		msg = err.Error()
	default:
		return err
	}

	// The reason names the admin's own path and what to fix; the caller is
	// root on the server.
	return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: msg}
}
