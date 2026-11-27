// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/doctor"
	"github.com/maki-it/hostashi/internal/pagecache"
	"github.com/maki-it/hostashi/internal/sites"
	"github.com/maki-it/hostashi/internal/web"
)

// openPageCache builds the cache directories for nginx's worker user, or nil
// when that user does not exist (then a page cache is refused).
func openPageCache() *pagecache.Dirs {
	u, err := lookupUser(WebUser)
	if err != nil {
		return nil
	}

	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)

	if err1 != nil || err2 != nil || uid == 0 || gid == 0 {
		return nil
	}

	return &pagecache.Dirs{UID: uid, GID: gid}
}

var errNoPageCache = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the page cache is not available on this server"}

// pageCacheFor prepares the cache directory of a site and returns what
// package web renders. The directory is on the site's pool and in the
// site's quota project, so cached pages count against its disk limit.
func (h *siteOps) pageCacheFor(user, home string, c agentapi.WebPageCache) (*web.PageCache, error) {
	if h.cache == nil {
		return nil, errNoPageCache
	}

	site, _, err := h.site(user)
	if err != nil {
		return nil, err
	}

	// The cache sits below the pool root, outside the home: without this
	// check a missing volume would put it on the root disk (spike S9).
	if err := h.m.CheckPool(site.Pool); err != nil {
		return nil, mapSiteError(err)
	}

	dir, err := h.cache.Ensure(poolOf(home), user, site.UID)
	if err != nil {
		return nil, fmt.Errorf("page cache of %s: %w", user, err)
	}

	if err := h.assignCacheQuota(dir, site); err != nil {
		return nil, err
	}

	return &web.PageCache{Dir: dir, Preset: c.Preset, Minutes: c.Minutes, SizeMB: c.SizeMB, Exclude: c.Exclude}, nil
}

// assignCacheQuota tags dir with the site's project where project quota is
// on. Without it, only max_size limits the cache (doctor says so).
func (h *siteOps) assignCacheQuota(dir string, site sites.Site) error {
	if h.quota == nil {
		return nil
	}

	ok, err := h.quota.Available(dir)
	if err != nil || !ok {
		return err
	}

	return mapQuotaError(h.quota.Assign(dir, site.UID, false))
}

// cacheChanges compares the page caches of two web states. A cache is
// purged when it starts serving again with possibly stale or wrong entries:
// after maintenance or a disabled phase, and when its rules or cache time
// changed. A site whose cache is gone from next loses its directory.
func cacheChanges(prev, next web.Desired) (purge, remove []string) {
	old := map[string]web.Site{}
	for _, s := range prev.Sites {
		old[s.User] = s
	}

	used := map[string]bool{}

	for _, s := range next.Sites {
		if s.PageCache == nil {
			continue
		}

		used[s.User] = true

		o, ok := old[s.User]
		if !ok || o.PageCache == nil {
			continue // a new cache starts empty anyway
		}

		serving := func(x web.Site) bool { return !x.Disabled && x.Maintenance == nil && x.PHPSocket != "" }
		if !serving(o) && serving(s) || !sameRules(*o.PageCache, *s.PageCache) {
			purge = append(purge, s.User)
		}
	}

	for _, s := range prev.Sites {
		if s.PageCache != nil && !used[s.User] {
			remove = append(remove, s.User)
		}
	}

	return purge, remove
}

// sameRules reports whether two caches answer the same requests in the same
// way. The size alone does not make old entries wrong.
func sameRules(a, b web.PageCache) bool {
	return a.Dir == b.Dir && a.Preset == b.Preset && a.Minutes == b.Minutes && slices.Equal(a.Exclude, b.Exclude)
}

// applyCacheChanges runs after a web.apply went live. Failures are logged;
// the next web.apply or a purge tries again.
func (h *siteOps) applyCacheChanges(ctx context.Context, prev, next web.Desired) {
	if h.cache == nil {
		return
	}

	purge, _ := cacheChanges(prev, next)

	for _, u := range purge {
		if err := h.purgeCache(u); err != nil {
			h.log.ErrorContext(ctx, "purge page cache", "site", u, "err", err)
		}
	}

	// Every directory without a cache in next goes, also one left over by
	// an earlier failure.
	used := map[string]bool{}

	for _, s := range next.Sites {
		if s.PageCache != nil {
			used[s.User] = true
		}
	}

	removed := false

	for _, p := range h.m.Pools() {
		names, err := h.cache.List(p.Path)
		if err != nil {
			h.log.ErrorContext(ctx, "list page caches", "pool", p.Name, "err", err)

			continue
		}

		for _, n := range names {
			if used[n] {
				continue
			}

			if err := h.cache.Remove(p.Path, n); err != nil {
				h.log.ErrorContext(ctx, "remove page cache", "site", n, "pool", p.Name, "err", err)
			}

			removed = true
		}
	}

	if len(purge) > 0 || removed {
		go h.sweepCaches(context.WithoutCancel(ctx))
	}
}

func (h *siteOps) purgeCache(user string) error {
	site, home, err := h.site(user)
	if err != nil {
		return err
	}

	if err := h.m.CheckPool(site.Pool); err != nil {
		return mapSiteError(err)
	}

	if err := h.cache.Purge(poolOf(home), user, site.UID); err != nil {
		return err
	}

	return h.assignCacheQuota(pagecache.Path(poolOf(home), user), site)
}

// removeCache deletes the cache directory of a site, whose home may already
// be gone: every pool is tried.
func (h *siteOps) removeCache(ctx context.Context, user string) {
	if h.cache == nil {
		return
	}

	for _, p := range h.m.Pools() {
		if err := h.cache.Remove(p.Path, user); err != nil {
			h.log.ErrorContext(ctx, "remove page cache", "site", user, "pool", p.Name, "err", err)
		}
	}
}

// sweepCaches deletes the contents of purged caches in every pool.
func (h *siteOps) sweepCaches(ctx context.Context) {
	h.sweepMu.Lock()
	defer h.sweepMu.Unlock()

	for _, p := range h.m.Pools() {
		if err := h.cache.Sweep(p.Path); err != nil {
			h.log.ErrorContext(ctx, "delete purged page cache", "pool", p.Name, "err", err)
		}
	}
}

// pageCachePurge serves pagecache.purge: only a site whose cache is on in
// the live web state, so the call cannot create directories for others.
func (h *siteOps) pageCachePurge(ctx context.Context, _ agentrpc.Caller, req agentapi.PageCachePurgeRequest) (agentapi.PageCachePurgeResponse, error) {
	if h.cache == nil || h.web == nil {
		return agentapi.PageCachePurgeResponse{}, errNoPageCache
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	cur, _, err := h.web.Current()
	if err != nil {
		return agentapi.PageCachePurgeResponse{}, err
	}

	i := slices.IndexFunc(cur.Sites, func(s web.Site) bool { return s.User == req.User })
	if i < 0 || cur.Sites[i].PageCache == nil {
		return agentapi.PageCachePurgeResponse{}, &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the page cache of this site is off"}
	}

	if err := h.purgeCache(req.User); err != nil {
		return agentapi.PageCachePurgeResponse{}, err
	}

	go h.sweepCaches(context.WithoutCancel(ctx))

	return agentapi.PageCachePurgeResponse{}, nil
}

// pageCacheChecks are the doctor checks of the page caches (ADR-0046): each
// cache in the live web state has its directory with the right owner and
// mode, cached pages count against the site's disk limit where quota is
// on, and no directory is left over from a cache that is gone.
func (h *siteOps) pageCacheChecks() []agentapi.DoctorCheck {
	if h.cache == nil || h.web == nil {
		return nil
	}

	cur, _, err := h.web.Current()
	if err != nil {
		return []agentapi.DoctorCheck{doctor.New(agentapi.DoctorWarn, "system.pagecache", "nginx.error", "", doctor.P{"error": err.Error()})}
	}

	var out []agentapi.DoctorCheck

	want := map[string]bool{}

	for _, s := range cur.Sites {
		if s.PageCache == nil {
			continue
		}

		want[s.User] = true
		c := doctor.NewSite(agentapi.DoctorOK, "site.pagecache", s.User, "pagecache.ok", "", nil)

		_, home, err := h.site(s.User)
		if err == nil {
			err = h.cache.Check(poolOf(home), s.User)
		}

		switch {
		case err != nil:
			c = doctor.NewSite(agentapi.DoctorFail, "site.pagecache", s.User, "pagecache.error", "pagecache.error.hint", doctor.P{"error": err.Error()})
		case h.quota != nil:
			if ok, qerr := h.quota.Available(home); qerr == nil && !ok {
				c = doctor.NewSite(agentapi.DoctorWarn, "site.pagecache", s.User, "pagecache.noquota", "pagecache.noquota.hint", nil)
			}
		}

		out = append(out, c)
	}

	for _, p := range h.m.Pools() {
		names, err := h.cache.List(p.Path)
		if err != nil {
			out = append(out, doctor.New(agentapi.DoctorWarn, "system.pagecache", "pagecache.pool", "", doctor.P{"pool": p.Name, "error": err.Error()}))

			continue
		}

		for _, n := range names {
			if !want[n] {
				out = append(out, doctor.New(agentapi.DoctorWarn, "system.pagecache", "pagecache.orphan", "pagecache.orphan.hint", doctor.P{"pool": p.Name, "site": n}))
			}
		}
	}

	return out
}
