// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/openapi"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/store"
)

// PoolAdmin renames storage pools and picks the default one (ADR-0016
// addendum 2). Neither carries a path, so the panel may ask for both.
type PoolAdmin interface {
	RenamePool(ctx context.Context, name, newName string) (agentapi.PoolInfo, error)
	SetDefaultPool(ctx context.Context, name string) (agentapi.PoolInfo, error)
}

// RPCPoolAdmin asks the agent.
type RPCPoolAdmin struct{ C *agentrpc.Client }

// RenamePool implements PoolAdmin.
func (a RPCPoolAdmin) RenamePool(ctx context.Context, name, newName string) (agentapi.PoolInfo, error) {
	return agentapi.PoolRename.Call(ctx, a.C, agentapi.PoolRenameRequest{Name: name, NewName: newName})
}

// SetDefaultPool implements PoolAdmin.
func (a RPCPoolAdmin) SetDefaultPool(ctx context.Context, name string) (agentapi.PoolInfo, error) {
	return agentapi.PoolDefault.Call(ctx, a.C, agentapi.PoolDefaultRequest{Name: name})
}

// PoolSites keeps the pool name of the panel's sites in step with the
// agent's registry after a rename.
type PoolSites interface {
	RenameSitePool(ctx context.Context, name, newName string, now time.Time) (int64, error)
}

// DefaultPoolOf returns the name of the default pool from a list; empty
// when the agent marks none (an agent older than the panel).
func DefaultPoolOf(pools []agentapi.PoolInfo) string {
	for _, p := range pools {
		if p.Default {
			return p.Name
		}
	}

	return ""
}

// Pools lists the storage pools of the agent (ADR-0016).
type Pools func(ctx context.Context) ([]agentapi.PoolInfo, error)

// RPCPools asks the agent.
func RPCPools(c *agentrpc.Client) Pools {
	return func(ctx context.Context) ([]agentapi.PoolInfo, error) {
		r, err := agentapi.PoolList.Call(ctx, c, agentapi.PoolListRequest{})

		return r.Pools, err
	}
}

type poolListJSON struct {
	Pools []agentapi.PoolInfo `json:"pools"`
}

// poolRenameJSON carries the new name of a pool.
type poolRenameJSON struct {
	Name string `json:"name"`
}

// registerPools adds GET /api/v1/pools for the pool choice in "New site"
// and the pools page. Pools are added and removed only by root on the
// server (hostashi pool add/remove), because that takes a path. Who may
// create sites may read them.

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

// registerPoolAdmin adds renaming a pool and picking the default one, for
// admins (ADR-0016 addendum 2). The agent's registry changes first; the
// panel's sites follow. If that second step fails, the same request again
// finishes it.
func registerPoolAdmin(mux *routeMux, a authn, list Pools, adm PoolAdmin, sites PoolSites, st Settings, log *slog.Logger) {
	audit := func(r *http.Request, uid int64, action, target string, detail map[string]string, diff store.Diff) {
		if err := st.AuditChange(r.Context(), &uid, action, target, detail, diff); err != nil {
			log.ErrorContext(r.Context(), "audit log", "action", action, "err", err)
		}
	}

	mux.Op("POST /api/v1/pools/{name}/rename", openapi.Operation{
		ID: "renamePool", Tag: "system", Summary: "Give a storage pool a new name; its path and sites stay",
		In: poolRenameJSON{}, Out: agentapi.PoolInfo{},
	}, a.allow("pools:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in poolRenameJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		name := r.PathValue("name")

		req := agentapi.PoolRenameRequest{Name: name, NewName: in.Name}
		if err := req.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		diff := store.DiffOf(map[string]string{"name": name}, map[string]string{"name": in.Name})

		info, err := adm.RenamePool(ctx, name, in.Name)
		if err != nil {
			// A repeat after the panel's step failed: the agent already
			// knows the pool by its new name.
			var ae *agentrpc.Error
			if !errors.As(err, &ae) || ae.Code != agentrpc.CodeNotFound || !renamed(ctx, list, name, in.Name, &info) {
				audit(r, p.User.ID, "pool.rename", name, map[string]string{"result": "error"}, diff)
				writeServiceAgentError(w, r, log, err)

				return
			}
		}

		n, err := sites.RenameSitePool(r.Context(), name, in.Name, time.Now())
		if err != nil {
			log.ErrorContext(r.Context(), "rename the pool of the panel's sites", "pool", name, "err", err)
			audit(r, p.User.ID, "pool.rename", name, map[string]string{"result": "error"}, diff)
			writeError(w, http.StatusInternalServerError, "internal", "the pool was renamed on the server, but the panel's sites were not updated; send the same rename again")

			return
		}

		audit(r, p.User.ID, "pool.rename", name, map[string]string{"result": "ok", "sites": strconv.FormatInt(n, 10)}, diff)
		writeJSON(w, http.StatusOK, info)
	}))

	mux.Op("POST /api/v1/pools/{name}/default", openapi.Operation{
		ID: "setDefaultPool", Tag: "system", Summary: "Make a storage pool the default for new sites",
		Out: agentapi.PoolInfo{},
	}, a.allow("pools:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		name := r.PathValue("name")
		if err := agentapi.ValidatePoolName(name); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		before := map[string]string{}
		if pools, err := list(ctx); err == nil {
			before["default"] = DefaultPoolOf(pools)
		}

		diff := store.DiffOf(before, map[string]string{"default": name})

		info, err := adm.SetDefaultPool(ctx, name)
		if err != nil {
			audit(r, p.User.ID, "pool.default", name, map[string]string{"result": "error"}, diff)
			writeServiceAgentError(w, r, log, err)

			return
		}

		audit(r, p.User.ID, "pool.default", name, map[string]string{"result": "ok"}, diff)
		writeJSON(w, http.StatusOK, info)
	}))
}

// renamed reports whether the agent lists newName and no longer name, and
// then sets info to the pool.
func renamed(ctx context.Context, list Pools, name, newName string, info *agentapi.PoolInfo) bool {
	pools, err := list(ctx)
	if err != nil {
		return false
	}

	found := false

	for _, p := range pools {
		switch p.Name {
		case name:
			return false
		case newName:
			*info, found = p, true
		}
	}

	return found
}
