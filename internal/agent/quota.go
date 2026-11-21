// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/backup"
	"github.com/maki-it/hostashi/internal/quota"
	"github.com/maki-it/hostashi/internal/sites"
)

// QuotaFS is the project quota of the file systems (ADR-0015). The
// production one is package quota; tests use a fake.
type QuotaFS interface {
	Available(dir string) (bool, error)
	Assign(dir string, id uint32, force bool) error
	SetLimits(dir string, id uint32, bytes, inodes uint64) error
	Get(dir string, id uint32) (quota.Usage, error)
	// Status tells counting from enforced limits; Enforce switches
	// enforcement on where usage is counted (spike S8 and S9).
	Status(dir string) (quota.State, error)
	Enforce(dir string) error
}

// KernelQuota is QuotaFS on the running kernel.
type KernelQuota struct{}

// Available implements QuotaFS.
func (KernelQuota) Available(dir string) (bool, error) { return quota.Available(dir) }

// Assign implements QuotaFS.
func (KernelQuota) Assign(dir string, id uint32, force bool) error {
	return quota.Assign(dir, id, force)
}

// SetLimits implements QuotaFS.
func (KernelQuota) SetLimits(dir string, id uint32, bytes, inodes uint64) error {
	return quota.SetLimits(dir, id, bytes, inodes)
}

// Get implements QuotaFS.
func (KernelQuota) Get(dir string, id uint32) (quota.Usage, error) { return quota.Get(dir, id) }

// Status implements QuotaFS.
func (KernelQuota) Status(dir string) (quota.State, error) { return quota.Status(dir) }

// Enforce implements QuotaFS.
func (KernelQuota) Enforce(dir string) error { return quota.Enforce(dir) }

var errQuotaUnavailable = &agentrpc.Error{
	Code: agentrpc.CodePrecondition, Message: "disk quota is not enabled on the file system of this site",
}

// site resolves a registered site and its home.
func (h *siteOps) site(user string) (sites.Site, string, error) {
	for _, s := range h.m.List() {
		if s.User != user {
			continue
		}

		home, ok := h.m.Home(user)
		if !ok {
			break
		}

		return s, home, nil
	}

	return sites.Site{}, "", mapSiteError(sites.ErrNotFound)
}

func (h *siteOps) quotaSet(_ context.Context, _ agentrpc.Caller, req agentapi.SiteQuotaSetRequest) (agentapi.SiteQuota, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	site, home, err := h.site(req.User)
	if err != nil {
		return agentapi.SiteQuota{}, err
	}

	ok, err := h.quota.Available(home)
	if err != nil {
		return agentapi.SiteQuota{}, err
	}

	if !ok {
		if req.Bytes == 0 && req.Inodes == 0 {
			return agentapi.SiteQuota{}, nil
		}

		return agentapi.SiteQuota{}, errQuotaUnavailable
	}

	// A limit that is only counted would be no limit at all.
	if err := h.quota.Enforce(poolOf(home)); err != nil {
		return agentapi.SiteQuota{}, mapQuotaError(err)
	}

	if err := h.quota.Assign(home, site.UID, false); err != nil {
		return agentapi.SiteQuota{}, mapQuotaError(err)
	}

	if err := h.quota.SetLimits(home, site.UID, req.Bytes, req.Inodes); err != nil {
		return agentapi.SiteQuota{}, mapQuotaError(err)
	}

	return h.readQuota(home, site.UID)
}

func (h *siteOps) quotaGet(_ context.Context, _ agentrpc.Caller, req agentapi.SiteQuotaGetRequest) (agentapi.SiteQuota, error) {
	site, home, err := h.site(req.User)
	if err != nil {
		return agentapi.SiteQuota{}, err
	}

	ok, err := h.quota.Available(home)
	if err != nil {
		return agentapi.SiteQuota{}, err
	}

	if !ok {
		return agentapi.SiteQuota{}, nil
	}

	return h.readQuota(home, site.UID)
}

func (h *siteOps) readQuota(home string, id uint32) (agentapi.SiteQuota, error) {
	u, err := h.quota.Get(home, id)
	if err != nil {
		return agentapi.SiteQuota{}, mapQuotaError(err)
	}

	return agentapi.SiteQuota{
		Available: true, Bytes: u.Bytes, Inodes: u.Inodes, BytesLimit: u.BytesLimit, InodesLimit: u.InodesLimit,
	}, nil
}

// freeSpace is what a site may still write under its disk limits, for
// the restore check (backup.SpaceFunc).
func (h *siteOps) freeSpace(user string) (backup.Space, error) {
	if h.quota == nil {
		return backup.Space{}, nil
	}

	site, home, err := h.site(user)
	if err != nil {
		return backup.Space{}, err
	}

	if ok, err := h.quota.Available(home); err != nil || !ok {
		return backup.Space{}, err
	}

	u, err := h.quota.Get(home, site.UID)
	if err != nil {
		return backup.Space{}, err
	}

	sp := backup.Space{Limited: u.BytesLimit > 0 || u.InodesLimit > 0, BytesLimit: u.BytesLimit, InodesLimit: u.InodesLimit}
	if u.BytesLimit > u.Bytes {
		sp.FreeBytes = u.BytesLimit - u.Bytes
	}

	if u.InodesLimit > u.Inodes {
		sp.FreeInodes = u.InodesLimit - u.Inodes
	}

	return sp, nil
}

// clearQuota drops the limits of a deleted site, so a later site that gets
// the same UID does not inherit them. dir is the site's pool. Failures are
// only logged.
func (h *siteOps) clearQuota(ctx context.Context, dir string, id uint32) {
	if h.quota == nil {
		return
	}

	if ok, err := h.quota.Available(dir); err != nil || !ok {
		return
	}

	if err := h.quota.SetLimits(dir, id, 0, 0); err != nil {
		h.log.ErrorContext(ctx, "clear disk limits of deleted site", "uid", id, "err", err)
	}
}

func mapQuotaError(err error) error {
	switch {
	case errors.Is(err, quota.ErrUnavailable):
		return errQuotaUnavailable
	case errors.Is(err, quota.ErrUnsafe):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "site directory failed safety checks"}
	default:
		return err
	}
}

func (h *siteOps) registerQuota(s *agentrpc.Server) {
	if h.quota == nil {
		h.quota = KernelQuota{}
	}

	agentrpc.Handle(s, agentapi.SiteQuotaSet, h.quotaSet)
	agentrpc.Handle(s, agentapi.SiteQuotaGet, h.quotaGet)

	for _, p := range h.m.Pools() {
		h.enforceQuota(p)
	}
}

// enforceQuota switches on the enforcement of disk limits on a pool whose
// file system only counts usage: a volume mounted with prjquota can come up
// that way, and the installer's quotaon unit covers only /. The agent does
// it at start, when a pool is added and before it sets limits. A pool whose
// volume is missing is left alone; failures are logged, doctor shows them.
func (h *siteOps) enforceQuota(p sites.Pool) {
	if err := h.m.CheckPool(p.Name); err != nil {
		return
	}

	st, err := h.quota.Status(p.Path)
	if err != nil || st != quota.Counting {
		return
	}

	if err := h.quota.Enforce(p.Path); err != nil {
		h.log.Error("enforce disk limits", "pool", p.Name, "path", p.Path, "err", err)

		return
	}

	h.log.Info("disk limits enforced", "pool", p.Name, "path", p.Path)
}

// quotaState is the quota state of a usable pool for PoolInfo; empty when
// it cannot be read.
func (h *siteOps) quotaState(p sites.Pool) string {
	if h.quota == nil {
		return ""
	}

	st, err := h.quota.Status(p.Path)
	if err != nil {
		return ""
	}

	return st.String()
}

// poolOf is the pool directory of a home.
func poolOf(home string) string { return filepath.Dir(home) }
