// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"golang.org/x/sys/unix"

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

		if h.quota != nil {
			h.enforceQuota(p)
		}

		if req.Default {
			if _, err := h.m.SetDefaultPool(p.Name); err != nil {
				return agentapi.PoolInfo{}, mapPoolError(err)
			}
		}

		return h.poolInfo(p, 0), nil
	})

	// Rename and default carry no path and change no unit, so the panel may
	// send them too (ADR-0016 addendum 2).
	agentrpc.Handle(s, agentapi.PoolRename, func(_ context.Context, _ agentrpc.Caller, req agentapi.PoolRenameRequest) (agentapi.PoolInfo, error) {
		h.mu.Lock()
		defer h.mu.Unlock()

		p, err := h.m.RenamePool(req.Name, req.NewName)
		if err != nil {
			return agentapi.PoolInfo{}, mapPoolError(err)
		}

		return h.poolInfo(p, h.poolSites(p.Name)), nil
	})

	agentrpc.Handle(s, agentapi.PoolDefault, func(_ context.Context, _ agentrpc.Caller, req agentapi.PoolDefaultRequest) (agentapi.PoolInfo, error) {
		h.mu.Lock()
		defer h.mu.Unlock()

		p, err := h.m.SetDefaultPool(req.Name)
		if err != nil {
			return agentapi.PoolInfo{}, mapPoolError(err)
		}

		return h.poolInfo(p, h.poolSites(p.Name)), nil
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

func (h *siteOps) poolSites(name string) int {
	n := 0

	for _, s := range h.m.List() {
		if s.Pool == name {
			n++
		}
	}

	return n
}

func (h *siteOps) poolInfos() []agentapi.PoolInfo {
	count := map[string]int{}
	for _, s := range h.m.List() {
		count[s.Pool]++
	}

	out := []agentapi.PoolInfo{}
	for _, p := range h.m.Pools() {
		out = append(out, h.poolInfo(p, count[p.Name]))
	}

	return out
}

// poolInfo describes a pool; state, quota and sizes only when its volume
// is there, so a missing volume never shows the root disk's free space.
func (h *siteOps) poolInfo(p sites.Pool, n int) agentapi.PoolInfo {
	info := agentapi.PoolInfo{Name: p.Name, Path: p.Path, Sites: n, Default: p.Name == h.m.DefaultPool()}
	if h.m.CheckPool(p.Name) != nil {
		return info
	}

	info.Available = true
	info.Quota = h.quotaState(p)

	var st unix.Statfs_t
	if err := unix.Statfs(p.Path, &st); err == nil {
		bsize := uint64(st.Bsize) //nolint:gosec // the block size is positive
		info.FreeBytes, info.TotalBytes = st.Bavail*bsize, st.Blocks*bsize
	}

	return info
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
