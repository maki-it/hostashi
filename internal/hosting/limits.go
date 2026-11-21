// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of the resource limits (ADR-0015).
var (
	ErrNoLimits         = errors.New("hosting: resource limits are not available")
	ErrPresetNotFound   = errors.New("hosting: no such limit preset")
	ErrPresetExists     = errors.New("hosting: a limit preset with this name exists")
	ErrPresetDefault    = errors.New("hosting: the default limit preset cannot be deleted")
	ErrUnlimitedDefault = fmt.Errorf("%w: the default preset must limit disk space, files, memory and processes", ErrInvalid)
	errLimitsNotActive  = fmt.Errorf("%w: the site is not active", ErrState)
)

// CPU priorities of the panel and their CPU weights (ADR-0015).
var cpuWeights = map[string]uint64{
	"low":    agentapi.CPUWeightLow,
	"normal": agentapi.CPUWeightNormal,
	"high":   agentapi.CPUWeightHigh,
}

// presetNameRe: lower-case, used in the API path.
var presetNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// LimitsAgent is the part of the agent the resource limits use.
type LimitsAgent interface {
	SiteQuotaSet(ctx context.Context, user string, bytes, inodes uint64) (agentapi.SiteQuota, error)
	SiteQuotaGet(ctx context.Context, user string) (agentapi.SiteQuota, error)
	SiteLimitsSet(ctx context.Context, user string, l agentapi.SiteLimits) error
}

// SiteQuotaSet calls site.quota.set.
func (a RPCAgent) SiteQuotaSet(ctx context.Context, user string, bytes, inodes uint64) (agentapi.SiteQuota, error) {
	return agentapi.SiteQuotaSet.Call(ctx, a.C, agentapi.SiteQuotaSetRequest{User: user, Bytes: bytes, Inodes: inodes})
}

// SiteQuotaGet calls site.quota.get.
func (a RPCAgent) SiteQuotaGet(ctx context.Context, user string) (agentapi.SiteQuota, error) {
	return agentapi.SiteQuotaGet.Call(ctx, a.C, agentapi.SiteQuotaGetRequest{User: user})
}

// SiteLimitsSet calls site.limits.set.
func (a RPCAgent) SiteLimitsSet(ctx context.Context, user string, l agentapi.SiteLimits) error {
	_, err := agentapi.SiteLimitsSet.Call(ctx, a.C, agentapi.SiteLimitsSetRequest{User: user, Limits: l})

	return err
}

// EnableLimits sets the agent for resource limits. Without it they answer
// ErrNoLimits and new sites get none.
func (s *Service) EnableLimits(a LimitsAgent) { s.limits = a }

// CheckLimits validates limit values. 0 means unlimited, except for the PHP
// workers; isDefault refuses unlimited disk space, files, memory and
// processes, because a new site must never get them without an admin's
// choice.
func CheckLimits(l store.Limits, isDefault bool) error {
	bad := func(format string, args ...any) error {
		return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
	}

	if _, ok := cpuWeights[l.CPUPriority]; !ok {
		return bad("cpu priority must be low, normal or high")
	}

	if l.DiskBytes < 0 || l.DiskInodes < 0 || l.MemoryBytes < 0 || l.Tasks < 0 || l.CPUQuotaPercent < 0 {
		return bad("limits must not be negative")
	}

	if l.PHPWorkers < 1 || l.PHPWorkers > agentapi.MaxPHPWorkers {
		return bad("php workers must be between 1 and %d", agentapi.MaxPHPWorkers)
	}

	if err := (agentapi.SiteQuotaSetRequest{User: "check", Bytes: uint64(l.DiskBytes), Inodes: uint64(l.DiskInodes)}).Validate(); err != nil {
		return bad("disk: %v", err)
	}

	if err := agentLimits(l).Validate(); err != nil {
		return bad("%v", err)
	}

	if isDefault && (l.DiskBytes == 0 || l.DiskInodes == 0 || l.MemoryBytes == 0 || l.Tasks == 0) {
		return ErrUnlimitedDefault
	}

	return nil
}

func agentLimits(l store.Limits) agentapi.SiteLimits {
	return agentapi.SiteLimits{
		MemoryMax: uint64(max(l.MemoryBytes, 0)), TasksMax: uint64(max(l.Tasks, 0)),
		CPUWeight: cpuWeights[l.CPUPriority], CPUQuotaPercent: uint64(max(l.CPUQuotaPercent, 0)),
	}
}

func checkPresetName(name string) error {
	if !presetNameRe.MatchString(name) {
		return fmt.Errorf("%w: a preset name has 1 to 32 lower-case letters, digits and dashes", ErrInvalid)
	}

	return nil
}

func limitsAudit(l store.Limits) map[string]string {
	return map[string]string{
		"disk_bytes": strconv.FormatInt(l.DiskBytes, 10), "disk_inodes": strconv.FormatInt(l.DiskInodes, 10),
		"memory_bytes": strconv.FormatInt(l.MemoryBytes, 10), "php_workers": strconv.FormatInt(l.PHPWorkers, 10),
		"tasks": strconv.FormatInt(l.Tasks, 10), "cpu_priority": l.CPUPriority,
		"cpu_quota_percent": strconv.FormatInt(l.CPUQuotaPercent, 10),
	}
}

// LimitPresets lists the presets by name.
func (s *Service) LimitPresets(ctx context.Context) ([]store.LimitPreset, error) {
	return s.st.LimitPresets(ctx)
}

// LimitPreset returns one preset.
func (s *Service) LimitPreset(ctx context.Context, name string) (store.LimitPreset, error) {
	p, err := s.st.LimitPreset(ctx, name)
	if errors.Is(err, store.ErrNotFound) {
		return store.LimitPreset{}, ErrPresetNotFound
	}

	return p, err
}

func presetErr(err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return ErrPresetNotFound
	case errors.Is(err, store.ErrPresetExists):
		return ErrPresetExists
	case errors.Is(err, store.ErrPresetDefault):
		return ErrPresetDefault
	}

	return err
}

// CreateLimitPreset adds a preset; with def it becomes the default for new
// sites.
func (s *Service) CreateLimitPreset(ctx context.Context, actor int64, name string, l store.Limits, c store.PresetCaches, def bool) (store.LimitPreset, error) {
	if err := checkPresetName(name); err != nil {
		return store.LimitPreset{}, err
	}

	if err := CheckLimits(l, def); err != nil {
		return store.LimitPreset{}, err
	}

	if err := CheckPresetCaches(c, l); err != nil {
		return store.LimitPreset{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	p, err := s.st.CreateLimitPreset(ctx, name, l, c, def, s.now())
	if err != nil {
		return store.LimitPreset{}, presetErr(err)
	}

	detail := limitsAudit(l)
	detail["objectCacheMb"] = strconv.FormatInt(c.ObjectCacheMB, 10)
	detail["default"] = strconv.FormatBool(def)
	s.audit(ctx, actor, "limits.preset.create", name, detail)

	return p, nil
}

// UpdateLimitPreset changes a preset and applies the new values to every
// site that follows it. A site where that fails keeps the new values as its
// desired state with the error recorded (see SiteLimits); the preset change
// itself stands. def makes the preset the default; the default flag is only
// moved by making another preset the default.
func (s *Service) UpdateLimitPreset(ctx context.Context, actor int64, name, newName string, l store.Limits, c store.PresetCaches, def bool) (store.LimitPreset, error) {
	if err := checkPresetName(newName); err != nil {
		return store.LimitPreset{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	cur, err := s.st.LimitPreset(ctx, name)
	if err != nil {
		return store.LimitPreset{}, presetErr(err)
	}

	if err := CheckLimits(l, def || cur.Default); err != nil {
		return store.LimitPreset{}, err
	}

	if err := CheckPresetCaches(c, l); err != nil {
		return store.LimitPreset{}, err
	}

	if _, err := s.st.UpdateLimitPreset(ctx, cur.ID, newName, l, c, def, s.now()); err != nil {
		return store.LimitPreset{}, presetErr(err)
	}

	followers, err := s.st.PresetFollowers(ctx, cur.ID)
	if err != nil {
		return store.LimitPreset{}, err
	}

	// The PHP workers live in the sites' pools, part of the web state.
	if len(followers) > 0 && cur.Limits.PHPWorkers != l.PHPWorkers {
		if err := s.applyWeb(ctx); err != nil {
			s.log.WarnContext(ctx, "apply web state after preset change", "preset", newName, "err", err)
		}
	}

	detail := limitsAudit(l)
	detail["objectCacheMb"] = strconv.FormatInt(c.ObjectCacheMB, 10)
	detail["name"] = newName
	detail["default"] = strconv.FormatBool(def || cur.Default)
	s.audit(ctx, actor, "limits.preset.update", name, detail)

	if s.limits != nil {
		for _, n := range followers {
			site, err := s.st.SiteByName(ctx, n)
			if err != nil {
				s.log.ErrorContext(ctx, "site of a preset follower", "site", n, "err", err)

				continue
			}

			if site.State != store.SiteActive {
				continue
			}

			if err := s.applyLimits(ctx, site, l); err != nil {
				s.log.WarnContext(ctx, "apply preset to site", "site", site.Name, "preset", newName, "err", err)
			}

			s.followPresetCache(ctx, actor, site, cur.Caches, c)
		}
	}

	p, err := s.st.LimitPresetByID(ctx, cur.ID)

	return p, presetErr(err)
}

// DeleteLimitPreset removes a preset that is not the default; its sites
// keep the values as custom values.
func (s *Service) DeleteLimitPreset(ctx context.Context, actor int64, name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur, err := s.st.LimitPreset(ctx, name)
	if err != nil {
		return presetErr(err)
	}

	if _, err := s.st.DeleteLimitPreset(ctx, cur.ID); err != nil {
		return presetErr(err)
	}

	s.audit(ctx, actor, "limits.preset.delete", name, nil)

	return nil
}

// SiteLimitsView is a site's limits with the name of its preset.
type SiteLimitsView struct {
	store.SiteLimits

	Set    bool   // false: the site has no limits (created before ADR-0015)
	Preset string // "": custom values
}

// SiteLimits returns the limits of a site.
func (s *Service) SiteLimits(ctx context.Context, name string) (SiteLimitsView, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return SiteLimitsView{}, err
	}

	return s.siteLimitsView(ctx, site)
}

func (s *Service) siteLimitsView(ctx context.Context, site store.Site) (SiteLimitsView, error) {
	sl, ok, err := s.st.SiteLimits(ctx, site.ID)
	if err != nil {
		return SiteLimitsView{}, err
	}

	v := SiteLimitsView{SiteLimits: sl, Set: ok}

	if sl.PresetID != 0 {
		p, err := s.st.LimitPresetByID(ctx, sl.PresetID)
		if err != nil {
			return SiteLimitsView{}, err
		}

		v.Preset = p.Name
	}

	return v, nil
}

// SiteUsage returns the disk use of a site and its limits on the server.
func (s *Service) SiteUsage(ctx context.Context, name string) (agentapi.SiteQuota, error) {
	if s.limits == nil {
		return agentapi.SiteQuota{}, ErrNoLimits
	}

	site, err := s.Get(ctx, name)
	if err != nil {
		return agentapi.SiteQuota{}, err
	}

	return s.limits.SiteQuotaGet(ctx, site.Name)
}

// SetSiteLimits gives a site the values of a preset (custom nil) or custom
// values (preset ""), stores them and applies them. The values are the
// site's desired state even when the agent fails: the error is recorded
// and returned, and setting the limits again retries.
func (s *Service) SetSiteLimits(ctx context.Context, actor int64, name, preset string, custom *store.Limits) (SiteLimitsView, error) {
	if s.limits == nil {
		return SiteLimitsView{}, ErrNoLimits
	}

	if (preset == "") == (custom == nil) {
		return SiteLimitsView{}, fmt.Errorf("%w: give either a preset or custom values", ErrInvalid)
	}

	if custom != nil {
		if err := CheckLimits(*custom, false); err != nil {
			return SiteLimitsView{}, err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return SiteLimitsView{}, err
	}

	if site.State != store.SiteActive {
		return SiteLimitsView{}, errLimitsNotActive
	}

	var (
		presetID int64
		l        store.Limits
		caches   store.PresetCaches
	)

	before, hadBefore, err := s.st.SiteLimits(ctx, site.ID)
	if err != nil {
		return SiteLimitsView{}, err
	}

	if custom != nil {
		l = *custom
	} else {
		p, err := s.st.LimitPreset(ctx, preset)
		if err != nil {
			return SiteLimitsView{}, presetErr(err)
		}

		presetID, l, caches = p.ID, p.Limits, p.Caches
	}

	if err := s.st.SetSiteLimits(ctx, site.ID, presetID, l, s.now()); err != nil {
		return SiteLimitsView{}, err
	}

	detail := limitsAudit(l)
	detail["preset"] = preset
	s.audit(ctx, actor, "site.limits", site.Name, detail)

	applyErr := s.applyLimits(ctx, site, l)

	// The PHP workers live in the site's pool, part of the web state.
	if site.PHP != "" && !site.Disabled && (!hadBefore || before.Limits.PHPWorkers != l.PHPWorkers) {
		if err := s.applyWeb(ctx); err != nil && applyErr == nil {
			applyErr = err
		}
	}

	// Switching to a preset gives the site its object cache; staying on
	// the same preset changes nothing there.
	if presetID != 0 && (!hadBefore || before.PresetID != presetID) {
		s.presetObjectCache(ctx, actor, site, caches)
	}

	v, err := s.siteLimitsView(ctx, site)
	if err != nil {
		return SiteLimitsView{}, err
	}

	return v, applyErr
}

// Usage is the measured disk use of a site's files.
type Usage struct {
	Bytes  uint64 // allocated bytes (st_blocks * 512)
	Inodes uint64 // files, directories and links
}

// ImportHeadroom is the share added to the measured use of an imported
// site, in percent (ADR-0015).
const ImportHeadroom = 50

// ImportLimits are the limits of an imported site: the default preset's
// values, with disk space and files raised to the measured use plus
// ImportHeadroom where that is more (rounded up to whole GiB and thousand
// files). It reports whether a value was raised: then the site no longer
// follows the preset.
func ImportLimits(def store.Limits, u Usage) (store.Limits, bool) {
	const gib = 1 << 30

	out, raised := def, false

	need := func(used, unit, maxV uint64) int64 {
		v := used + used*ImportHeadroom/100
		v = (v + unit - 1) / unit * unit

		return int64(min(v, maxV, math.MaxInt64))
	}

	if b := need(u.Bytes, gib, agentapi.MaxQuotaBytes); def.DiskBytes != 0 && b > def.DiskBytes {
		out.DiskBytes, raised = b, true
	}

	if i := need(u.Inodes, 1000, agentapi.MaxQuotaInodes); def.DiskInodes != 0 && i > def.DiskInodes {
		out.DiskInodes, raised = i, true
	}

	return out, raised
}

// storeDefaultLimits gives a new site the default preset, before its
// web state is applied; with usage (an import) the disk limits are raised
// to fit (ImportLimits). It returns the preset's caches for the caller to
// set up once the site runs, and reports false (and logs why) when the
// site gets no limits; the site itself stays.
func (s *Service) storeDefaultLimits(ctx context.Context, site store.Site, usage *Usage) (store.Limits, store.PresetCaches, bool) {
	if s.limits == nil {
		return store.Limits{}, store.PresetCaches{}, false
	}

	p, err := s.st.DefaultLimitPreset(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "default limit preset", "site", site.Name, "err", err)

		return store.Limits{}, store.PresetCaches{}, false
	}

	l, presetID := p.Limits, p.ID

	if usage != nil {
		if raised, ok := ImportLimits(p.Limits, *usage); ok {
			l, presetID = raised, 0
		}
	}

	if err := s.st.SetSiteLimits(ctx, site.ID, presetID, l, s.now()); err != nil {
		s.log.ErrorContext(ctx, "store limits of a new site", "site", site.Name, "err", err)

		return store.Limits{}, store.PresetCaches{}, false
	}

	return l, p.Caches, true
}

// phpWorkers is the worker count to send for a site: its limit, or 0 (the
// agent's default) for a site without limits.
func phpWorkers(workers map[int64]int64, siteID int64) int {
	n, ok := workers[siteID]
	if !ok || n < 1 || n > agentapi.MaxPHPWorkers {
		return 0
	}

	return int(n)
}

// applyLimits sends the limits to the agent and records the result. A
// file system without project quota does not fail the change: the disk
// limits are then not enforced, which SiteUsage reports (available false).
// The PHP workers reach the server with the site's PHP pool.
func (s *Service) applyLimits(ctx context.Context, site store.Site, l store.Limits) error {
	err := s.limits.SiteLimitsSet(ctx, site.Name, agentLimits(l))
	if err == nil {
		_, err = s.limits.SiteQuotaSet(ctx, site.Name, uint64(max(l.DiskBytes, 0)), uint64(max(l.DiskInodes, 0)))
		if isCode(err, agentrpc.CodePrecondition) {
			s.log.WarnContext(ctx, "disk limits not enforced: no project quota", "site", site.Name)

			err = nil
		}
	}

	// The record must be written even when the request was canceled.
	rec := context.WithoutCancel(ctx)

	if err != nil {
		if merr := s.st.MarkSiteLimits(rec, site.ID, 0, err.Error()); merr != nil {
			s.log.ErrorContext(ctx, "record failed limits", "site", site.Name, "err", merr)
		}

		return err
	}

	if merr := s.st.MarkSiteLimits(rec, site.ID, s.now().Unix(), ""); merr != nil {
		s.log.ErrorContext(ctx, "record applied limits", "site", site.Name, "err", merr)
	}

	return nil
}
