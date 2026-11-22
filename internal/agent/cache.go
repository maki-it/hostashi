// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/objcache"
)

var errNoCache = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the object cache is not available on this server"}

// registerCache serves cache.* (ADR-0045). cache.apply holds h.mu like
// web.apply, so a site cannot be deleted while its cache is set up.
func (h *siteOps) registerCache(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.CacheApply, func(ctx context.Context, _ agentrpc.Caller, req agentapi.CacheApplyRequest) (agentapi.CacheStatus, error) {
		if h.objc == nil {
			return agentapi.CacheStatus{}, errNoCache
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		if _, _, err := h.site(req.Site); err != nil && req.MemoryMB != 0 {
			return agentapi.CacheStatus{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site " + req.Site}
		}

		st, err := h.objc.Apply(ctx, req.Site, req.MemoryMB)

		return st, mapCacheError(err)
	})

	agentrpc.Handle(s, agentapi.CacheFlush, func(ctx context.Context, _ agentrpc.Caller, req agentapi.CacheSiteRequest) (agentapi.CacheStatus, error) {
		if h.objc == nil {
			return agentapi.CacheStatus{}, errNoCache
		}

		st, err := h.objc.Flush(ctx, req.Site)

		return st, mapCacheError(err)
	})

	agentrpc.Handle(s, agentapi.CacheStatusOp, func(ctx context.Context, _ agentrpc.Caller, req agentapi.CacheSiteRequest) (agentapi.CacheStatus, error) {
		if h.objc == nil {
			return agentapi.CacheStatus{}, errNoCache
		}

		st, err := h.objc.Status(ctx, req.Site)

		return st, mapCacheError(err)
	})
}

// cacheDisabled stops the caches of disabled sites and starts them again
// for enabled ones, after a web.apply went live.
func (h *siteOps) cacheDisabled(ctx context.Context, req agentapi.WebApplyRequest) {
	if h.objc == nil {
		return
	}

	for _, s := range req.Sites {
		if _, ok := h.m.Home(s.User); !ok {
			continue // a redirect site without an account (ADR-0054)
		}

		if err := h.objc.SetPaused(ctx, s.User, s.Disabled); err != nil {
			h.log.ErrorContext(ctx, "object cache of site", "site", s.User, "disabled", s.Disabled, "err", err)
		}
	}
}

func mapCacheError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, objcache.ErrNoCache):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "the site has no object cache"}
	case errors.Is(err, objcache.ErrPaused):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the site is disabled"}
	case errors.Is(err, objcache.ErrStartFailed):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the object cache did not start, the previous state was restored"}
	default:
		return err
	}
}
