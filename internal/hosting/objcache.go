// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of the object cache.
var (
	ErrNoObjectCache = errors.New("hosting: the object cache is not available")
	// ErrObjectCacheOff means the site's cache is off, so it cannot be
	// flushed.
	ErrObjectCacheOff = errors.New("hosting: the object cache of the site is off")
	errCacheDisabled  = fmt.Errorf("%w: the site is disabled", ErrState)
)

// CacheAgent is the part of the agent the object cache uses (ADR-0045).
type CacheAgent interface {
	CacheApply(ctx context.Context, site string, memoryMB int) (agentapi.CacheStatus, error)
	CacheFlush(ctx context.Context, site string) (agentapi.CacheStatus, error)
	CacheStatus(ctx context.Context, site string) (agentapi.CacheStatus, error)
}

// CacheApply calls cache.apply.
func (a RPCAgent) CacheApply(ctx context.Context, site string, memoryMB int) (agentapi.CacheStatus, error) {
	return agentapi.CacheApply.Call(ctx, a.C, agentapi.CacheApplyRequest{Site: site, MemoryMB: memoryMB})
}

// CacheFlush calls cache.flush.
func (a RPCAgent) CacheFlush(ctx context.Context, site string) (agentapi.CacheStatus, error) {
	return agentapi.CacheFlush.Call(ctx, a.C, agentapi.CacheSiteRequest{Site: site})
}

// CacheStatus calls cache.status.
func (a RPCAgent) CacheStatus(ctx context.Context, site string) (agentapi.CacheStatus, error) {
	return agentapi.CacheStatusOp.Call(ctx, a.C, agentapi.CacheSiteRequest{Site: site})
}

// EnableObjectCache sets the agent for object caches. Without it they
// answer ErrNoObjectCache.
func (s *Service) EnableObjectCache(a CacheAgent) { s.cache = a }

// ObjectCacheView is the cache a site should have and, when the agent
// answered, what runs.
type ObjectCacheView struct {
	Enabled bool
	// MemoryMB is the size the cache has or gets when turned on.
	MemoryMB int
	// MemoryLimitBytes is the site's memory limit (ADR-0015) that the cache
	// counts against, 0 for none.
	MemoryLimitBytes int64
	Status           *agentapi.CacheStatus
}

func (s *Service) cacheSite(ctx context.Context, name string) (store.Site, error) {
	if s.cache == nil {
		return store.Site{}, ErrNoObjectCache
	}

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, ErrState
	}

	return site, nil
}

func (s *Service) cacheView(ctx context.Context, site store.Site) (ObjectCacheView, error) {
	c, err := s.st.ObjectCache(ctx, site.ID)
	if err != nil {
		return ObjectCacheView{}, err
	}

	v := ObjectCacheView{Enabled: c.Enabled, MemoryMB: c.MemoryMB}
	if v.MemoryMB == 0 {
		v.MemoryMB = agentapi.DefaultCacheMB
	}

	sl, ok, err := s.st.SiteLimits(ctx, site.ID)
	if err != nil {
		return ObjectCacheView{}, err
	}

	if ok {
		v.MemoryLimitBytes = sl.Limits.MemoryBytes
	}

	return v, nil
}

// ObjectCache returns the cache of a site with the agent's status. Without
// an answer of the agent the status is nil.
func (s *Service) ObjectCache(ctx context.Context, name string) (ObjectCacheView, error) {
	if s.cache == nil {
		return ObjectCacheView{}, ErrNoObjectCache
	}

	site, err := s.Get(ctx, name)
	if err != nil {
		return ObjectCacheView{}, err
	}

	v, err := s.cacheView(ctx, site)
	if err != nil {
		return ObjectCacheView{}, err
	}

	st, err := s.cache.CacheStatus(ctx, name)
	if err != nil {
		s.log.WarnContext(ctx, "object cache status", "site", name, "err", err)

		return v, nil
	}

	v.Status = &st

	return v, nil
}

// checkCacheSize refuses a size that leaves the site's PHP no room under
// its memory limit: the cache must stay below half of it.
func checkCacheSize(mb int, limitBytes int64) error {
	if err := agentapi.ValidateCacheMB(mb); err != nil || mb == 0 {
		return fmt.Errorf("%w: size must be %d to %d MiB", ErrInvalid, agentapi.MinCacheMB, agentapi.MaxCacheMB)
	}

	if limitBytes > 0 && int64(mb)<<20 > limitBytes/2 {
		return fmt.Errorf("%w: %d MiB is more than half of the site's memory limit (%d MiB)", ErrInvalid, mb, limitBytes>>20)
	}

	return nil
}

// SetObjectCache turns a site's cache on or off. memoryMB 0 keeps the size
// (or the default); another value changes it, which the caller has checked
// the account may do. A refused change of the agent is undone.
func (s *Service) SetObjectCache(ctx context.Context, actor int64, name string, enabled bool, memoryMB int) (ObjectCacheView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.setObjectCache(ctx, actor, name, enabled, memoryMB)
}

// setObjectCache is SetObjectCache with s.mu held.
func (s *Service) setObjectCache(ctx context.Context, actor int64, name string, enabled bool, memoryMB int) (ObjectCacheView, error) {
	site, err := s.cacheSite(ctx, name)
	if err != nil {
		return ObjectCacheView{}, err
	}

	old, err := s.st.ObjectCache(ctx, site.ID)
	if err != nil {
		return ObjectCacheView{}, err
	}

	v, err := s.cacheView(ctx, site)
	if err != nil {
		return ObjectCacheView{}, err
	}

	if memoryMB == 0 {
		memoryMB = v.MemoryMB
	}

	if err := checkCacheSize(memoryMB, v.MemoryLimitBytes); err != nil {
		return ObjectCacheView{}, err
	}

	if enabled && site.Disabled && !old.Enabled {
		return ObjectCacheView{}, errCacheDisabled
	}

	if err := s.st.SetObjectCache(ctx, site.ID, enabled, memoryMB, actor, s.now()); err != nil {
		return ObjectCacheView{}, err
	}

	agentMB := 0
	if enabled {
		agentMB = memoryMB
	}

	st, err := s.cache.CacheApply(ctx, name, agentMB)
	if err != nil {
		s.restoreCache(ctx, site, old)

		return ObjectCacheView{}, err
	}

	action := "site.object_cache.off"
	if enabled {
		action = "site.object_cache.on"
	}

	s.audit(ctx, actor, action, name, map[string]string{"memoryMb": strconv.Itoa(memoryMB)})

	v.Enabled, v.MemoryMB, v.Status = enabled, memoryMB, &st

	return v, nil
}

func (s *Service) restoreCache(ctx context.Context, site store.Site, old store.ObjectCache) {
	ctx = context.WithoutCancel(ctx)

	var err error
	if old.MemoryMB == 0 {
		err = s.st.DeleteObjectCache(ctx, site.ID)
	} else {
		err = s.st.SetObjectCache(ctx, site.ID, old.Enabled, old.MemoryMB, old.UpdatedBy, old.UpdatedAt)
	}

	if err != nil {
		s.log.ErrorContext(ctx, "restore object cache setting after failed change", "site", site.Name, "err", err)
	}
}

// FlushObjectCache empties a site's cache.
func (s *Service) FlushObjectCache(ctx context.Context, actor int64, name string) (agentapi.CacheStatus, error) {
	site, err := s.cacheSite(ctx, name)
	if err != nil {
		return agentapi.CacheStatus{}, err
	}

	c, err := s.st.ObjectCache(ctx, site.ID)
	if err != nil {
		return agentapi.CacheStatus{}, err
	}

	if !c.Enabled {
		return agentapi.CacheStatus{}, ErrObjectCacheOff
	}

	st, err := s.cache.CacheFlush(ctx, name)
	if err != nil {
		return agentapi.CacheStatus{}, err
	}

	s.audit(ctx, actor, "site.object_cache.flush", name, nil)

	return st, nil
}

// CacheSocket is the socket of a site's cache, the same path the agent
// uses (objcache.Manager.Socket with the default directory).
func CacheSocket(site string) string { return "/run/hostashi-valkey/" + site + "/valkey.sock" }

// CheckPresetCaches checks the caches of a preset against its own limits:
// the object cache is off (0) or a valid size within half of the memory
// limit, as for a site.
func CheckPresetCaches(c store.PresetCaches, l store.Limits) error {
	if c.ObjectCacheMB == 0 {
		return nil
	}

	return checkCacheSize(int(c.ObjectCacheMB), l.MemoryBytes)
}

// presetObjectCache gives a site the object cache of the preset it just got
// (a new site, or one switched to the preset): on with the preset's size,
// or the size alone for a cache that is already on. A preset without a
// cache changes nothing. Failures are logged; the site stands without.
func (s *Service) presetObjectCache(ctx context.Context, actor int64, site store.Site, c store.PresetCaches) {
	if s.cache == nil || c.ObjectCacheMB == 0 || site.PHP == "" || site.Disabled {
		return
	}

	if _, err := s.setObjectCache(ctx, actor, site.Name, true, int(c.ObjectCacheMB)); err != nil {
		s.log.WarnContext(ctx, "object cache of the preset not applied", "site", site.Name, "err", err)
	}
}

// followPresetCache changes the size of a following site's cache after its
// preset changed. It never turns a cache on or off.
func (s *Service) followPresetCache(ctx context.Context, actor int64, site store.Site, old, c store.PresetCaches) {
	if s.cache == nil || c.ObjectCacheMB == 0 || c.ObjectCacheMB == old.ObjectCacheMB {
		return
	}

	cur, err := s.st.ObjectCache(ctx, site.ID)
	if err != nil || !cur.Enabled || int64(cur.MemoryMB) == c.ObjectCacheMB {
		return
	}

	if _, err := s.setObjectCache(ctx, actor, site.Name, true, int(c.ObjectCacheMB)); err != nil {
		s.log.WarnContext(ctx, "object cache size of the preset not applied", "site", site.Name, "err", err)
	}
}
