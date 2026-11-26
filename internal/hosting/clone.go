// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/profile"
	"github.com/maki-it/hostashi/internal/store"
)

// Cloning a site (ADR-0070): the panel creates a new site with the
// source's type and settings, creates new local databases, then asks the
// agent to copy the home and each database. RunClones follows the jobs;
// a first copy that fails deletes the half-made clone again.

// Errors of cloning.
var (
	// ErrNoClone: this panel runs without an agent for copies.
	ErrNoClone = errors.New("hosting: cloning is not available")
	// ErrNoSpace: the target pool or the database disk has too little room.
	ErrNoSpace = errors.New("hosting: not enough free space for the clone")
	// ErrCloneBusy: a copy into or out of the site runs already.
	ErrCloneBusy = errors.New("hosting: a copy of this site is running")
	// ErrNotClone: the site was not made by cloning, or its source is gone.
	ErrNotClone = errors.New("hosting: the site is no clone of an existing site")
)

// CloneAgent is the part of the agent cloning uses.
type CloneAgent interface {
	SiteHomeCopy(ctx context.Context, req agentapi.SiteHomeCopyRequest) (agentapi.SiteHomeCopyJob, error)
	SiteHomeCopyStatus(ctx context.Context, id string) (agentapi.SiteHomeCopyJob, error)
	DBCopy(ctx context.Context, source, target string) (agentapi.DBJob, error)
	PoolList(ctx context.Context) ([]agentapi.PoolInfo, error)
	HostStats(ctx context.Context) (agentapi.HostStats, error)
	// WPCloneStart and WPCloneStatus run the WordPress configuration of
	// a clone (site.wordpress.clone, site.wordpress.status).
	WPCloneStart(ctx context.Context, req agentapi.WPCloneRequest) (agentapi.WPInstallStatus, error)
	WPCloneStatus(ctx context.Context, site string) (agentapi.WPInstallStatus, error)
	// DotEnvCloneStart runs the .env step of a Laravel or Symfony clone
	// in the same unit (site.dotenv.clone).
	DotEnvCloneStart(ctx context.Context, req agentapi.DotEnvCloneRequest) (agentapi.WPInstallStatus, error)
}

// DotEnvCloneStart calls site.dotenv.clone.
func (a RPCAgent) DotEnvCloneStart(ctx context.Context, req agentapi.DotEnvCloneRequest) (agentapi.WPInstallStatus, error) {
	return agentapi.DotEnvCloneStart.Call(ctx, a.C, req)
}

// WPCloneStart calls site.wordpress.clone.
func (a RPCAgent) WPCloneStart(ctx context.Context, req agentapi.WPCloneRequest) (agentapi.WPInstallStatus, error) {
	return agentapi.WPCloneStart.Call(ctx, a.C, req)
}

// WPCloneStatus calls site.wordpress.status.
func (a RPCAgent) WPCloneStatus(ctx context.Context, site string) (agentapi.WPInstallStatus, error) {
	return agentapi.WPInstallStatOp.Call(ctx, a.C, agentapi.WPStatusRequest{Site: site})
}

// SiteHomeCopy calls site.home.copy.
func (a RPCAgent) SiteHomeCopy(ctx context.Context, req agentapi.SiteHomeCopyRequest) (agentapi.SiteHomeCopyJob, error) {
	return agentapi.SiteHomeCopy.Call(ctx, a.C, req)
}

// SiteHomeCopyStatus calls site.home.copy.status.
func (a RPCAgent) SiteHomeCopyStatus(ctx context.Context, id string) (agentapi.SiteHomeCopyJob, error) {
	return agentapi.SiteHomeCopyStatus.Call(ctx, a.C, agentapi.SiteHomeCopyStatusRequest{ID: id})
}

// DBCopy calls db.copy.
func (a RPCAgent) DBCopy(ctx context.Context, source, target string) (agentapi.DBJob, error) {
	return agentapi.DBCopy.Call(ctx, a.C, agentapi.DBCopyRequest{Source: source, Target: target})
}

// PoolList calls pool.list.
func (a RPCAgent) PoolList(ctx context.Context) ([]agentapi.PoolInfo, error) {
	r, err := agentapi.PoolList.Call(ctx, a.C, agentapi.PoolListRequest{})

	return r.Pools, err
}

// HostStats calls host.stats.
func (a RPCAgent) HostStats(ctx context.Context) (agentapi.HostStats, error) {
	return agentapi.HostStatsOp.Call(ctx, a.C, agentapi.HostStatsRequest{})
}

// EnableClones sets the agent for copies. Without it a clone answers
// ErrNoClone.
func (s *Service) EnableClones(a CloneAgent) {
	s.clones = a

	if s.cloneKick == nil {
		s.cloneKick = make(chan struct{}, 1)
	}
}

// clonePoll is how often RunClones asks the agent about running copies.
var clonePoll = 3 * time.Second

// cloneSpare is the room a clone needs beyond the source's size, in
// percent (ADR-0070: 10 %, an estimate).
const cloneSpare = 10

// CloneInput is what the person chooses for a clone.
type CloneInput struct {
	Name   string
	Domain string
	// Pool is the target storage pool; empty: the source's pool.
	Pool string
	// Staging marks the clone as staging copy of the source.
	Staging bool
	// Maintenance puts the source in maintenance (ADR-0027) while its
	// files and databases are copied, so the copy is consistent.
	Maintenance bool
}

// CloneView is the clone row of a site with the names the API shows.
type CloneView struct {
	store.SiteClone
	// Source is the source site's name, empty once it is deleted.
	Source string
	// Home is the agent's report on the running home copy, nil when none
	// runs or the agent does not know it.
	Home *agentapi.SiteHomeCopyJob
}

// CloneStarted is a clone that has started copying. AccessUser and
// AccessPassword are the generated basic auth user of its password
// protection, shown once; empty when the clone has none.
type CloneStarted struct {
	store.Site
	AccessUser     string
	AccessPassword string
}

// CloneAccessUser is the name of the basic auth user generated for a
// clone with password protection.
const CloneAccessUser = "staging"

// CloneSite creates a new site as a copy of source and starts copying its
// files and local databases. The new site is returned at once; RunClones
// finishes the copy.
func (s *Service) CloneSite(ctx context.Context, actor int64, source string, in CloneInput) (CloneStarted, error) {
	if s.clones == nil {
		return CloneStarted{}, ErrNoClone
	}

	src, err := s.Get(ctx, source)
	if err != nil {
		return CloneStarted{}, err
	}

	if !src.Account || noFiles(src.Type) {
		return CloneStarted{}, ErrSiteType
	}

	if src.State != store.SiteActive {
		return CloneStarted{}, ErrState
	}

	if busy, err := s.cloneBusy(ctx, src.ID); err != nil {
		return CloneStarted{}, err
	} else if busy {
		return CloneStarted{}, ErrCloneBusy
	}

	if in.Pool == "" {
		in.Pool = src.Pool
	}

	dbs, err := s.cloneDatabases(ctx, src, in.Name)
	if err != nil {
		return CloneStarted{}, err
	}

	if err := s.checkCloneSpace(ctx, src, in.Pool, dbs); err != nil {
		return CloneStarted{}, err
	}

	req := CreateRequest{
		Name: in.Name, Pool: in.Pool, Domains: []string{in.Domain}, PHP: src.PHP,
		TLS: src.TLS && s.issuer != nil, PHPPrograms: src.PHPPrograms,
	}

	if src.App != nil {
		a := *src.App
		req.App = &a
	}

	created, err := s.Create(ctx, actor, req)
	if err != nil {
		return CloneStarted{}, err
	}

	// From here on a failure removes the new site again.
	undo := context.WithoutCancel(ctx)
	fail := func(err error) (CloneStarted, error) {
		if derr := s.Delete(undo, actor, created.Name); derr != nil {
			s.log.ErrorContext(ctx, "remove a clone that failed to start", "site", created.Name, "err", derr)
		}

		return CloneStarted{}, err
	}

	user, pw, err := s.cloneSettings(ctx, actor, src, created, in.Staging)
	if err != nil {
		return fail(err)
	}

	for i, d := range dbs {
		if _, _, err := s.CreateDatabase(ctx, actor, NewDatabase{Site: created.Name, Name: d.Target, Server: store.LocalDBServer, User: d.User}); err != nil {
			return fail(fmt.Errorf("create database %d of the clone: %w", i+1, err))
		}
	}

	// A source already in maintenance stays as it is, also afterwards. It
	// goes into maintenance before the row exists, so the poller never
	// copies it before.
	hold := in.Maintenance && !src.Maintenance
	if hold {
		if err := s.sourceMaintenance(ctx, actor, src, true); err != nil {
			return fail(fmt.Errorf("put %s in maintenance: %w", src.Name, err))
		}
	}

	if err := s.st.CreateSiteClone(ctx, store.SiteClone{SiteID: created.ID, SourceID: src.ID, Staging: in.Staging, Databases: dbs, SourceMaintenance: hold, StartedBy: actor}, s.now()); err != nil {
		if hold {
			s.releaseSource(undo, src, &store.SiteClone{StartedBy: actor})
		}

		return fail(err)
	}

	s.audit(ctx, actor, "site.clone", created.Name, map[string]string{
		"source": src.Name, "pool": in.Pool, "domain": in.Domain, "staging": strconv.FormatBool(in.Staging),
		"maintenance": strconv.FormatBool(hold), "databases": cloneDBList(dbs),
	})

	s.kickClones()

	site, err := s.Get(ctx, created.Name)
	if err != nil {
		return CloneStarted{}, err
	}

	return CloneStarted{Site: site, AccessUser: user, AccessPassword: pw}, nil
}

// RefreshClone copies the files and databases of the source into the
// clone again ("refresh from source"); the clone's own files and data are
// replaced. Only for staging clones whose source still exists.
func (s *Service) RefreshClone(ctx context.Context, actor int64, name string, maintenance bool) error {
	if s.clones == nil {
		return ErrNoClone
	}

	site, err := s.Get(ctx, name)
	if err != nil {
		return err
	}

	c, err := s.st.SiteClone(ctx, site.ID)
	if errors.Is(err, store.ErrNotFound) || err == nil && (c.SourceID == 0 || !c.Staging) {
		return ErrNotClone
	}

	if err != nil {
		return err
	}

	src, err := s.st.SiteByID(ctx, c.SourceID)
	if err != nil {
		return err
	}

	if src.State != store.SiteActive || site.State != store.SiteActive {
		return ErrState
	}

	if busy, err := s.cloneBusy(ctx, src.ID); err != nil {
		return err
	} else if busy {
		return ErrCloneBusy
	}

	// The pairs of the first copy; a database the clone lost meanwhile is
	// skipped, one the source gained is not added. Names are unique only
	// while a database exists: a name that another site took since then
	// is skipped too, so a refresh never reads or overwrites the database
	// of a third site.
	dbs := make([]store.CloneDatabase, 0, len(c.Databases))

	for _, d := range c.Databases {
		if sd, err := s.st.DatabaseByName(ctx, d.Source); err != nil || sd.Site != src.Name {
			continue
		}

		if td, err := s.st.DatabaseByName(ctx, d.Target); err != nil || td.Site != site.Name {
			continue
		}

		dbs = append(dbs, store.CloneDatabase{Source: d.Source, Target: d.Target, User: d.User})
	}

	if err := s.checkCloneSpace(ctx, src, site.Pool, nil); err != nil {
		return err
	}

	hold := maintenance && !src.Maintenance
	if hold {
		if err := s.sourceMaintenance(ctx, actor, src, true); err != nil {
			return fmt.Errorf("put %s in maintenance: %w", src.Name, err)
		}
	}

	if err := s.st.StartCloneRefresh(ctx, site.ID, actor, dbs, hold, s.now()); err != nil {
		if hold {
			s.releaseSource(context.WithoutCancel(ctx), src, &store.SiteClone{StartedBy: actor})
		}

		if errors.Is(err, store.ErrCloneBusy) {
			return ErrCloneBusy
		}

		return err
	}

	s.audit(ctx, actor, "site.clone.refresh", site.Name, map[string]string{"source": src.Name, "maintenance": strconv.FormatBool(hold), "databases": cloneDBList(dbs)})
	s.kickClones()

	return nil
}

// Clone returns the clone row of a site with the running home copy.
func (s *Service) Clone(ctx context.Context, name string) (CloneView, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return CloneView{}, err
	}

	c, err := s.st.SiteClone(ctx, site.ID)
	if errors.Is(err, store.ErrNotFound) {
		return CloneView{}, ErrNotClone
	}

	if err != nil {
		return CloneView{}, err
	}

	v := CloneView{SiteClone: c}

	if c.SourceID != 0 {
		if src, err := s.st.SiteByID(ctx, c.SourceID); err == nil {
			v.Source = src.Name
		}
	}

	if s.clones != nil && c.State == store.CloneCopying && c.HomeJob != "" && !c.HomeDone {
		if j, err := s.clones.SiteHomeCopyStatus(ctx, c.HomeJob); err == nil {
			v.Home = &j
		}
	}

	return v, nil
}

// cloneBusy reports whether a copy runs out of the site or into it.
func (s *Service) cloneBusy(ctx context.Context, siteID int64) (bool, error) {
	all, err := s.st.SiteClones(ctx, 0)
	if err != nil {
		return false, err
	}

	for _, c := range all {
		if c.State == store.CloneCopying && (c.SourceID == siteID || c.SiteID == siteID) {
			return true, nil
		}
	}

	return false, nil
}

// cloneDatabases names the clone's databases: the source's local ones,
// each renamed from the source's prefix to the clone's (or prefixed with
// the clone's name), cut to the length of a database user, and made unique.
// Databases on external servers are not cloned (ADR-0070).
func (s *Service) cloneDatabases(ctx context.Context, src store.Site, name string) ([]store.CloneDatabase, error) {
	if err := (agentapi.SiteCreateRequest{User: name}).Validate(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	list, err := s.st.Databases(ctx, src.Name)
	if err != nil {
		return nil, err
	}

	used := map[string]bool{}
	out := []store.CloneDatabase{}

	for _, d := range list {
		if d.Server != store.LocalDBServer {
			continue
		}

		target, err := s.freeCloneDBName(ctx, cloneDBName(d.Name, src.Name, name), used)
		if err != nil {
			return nil, err
		}

		used[target] = true
		out = append(out, store.CloneDatabase{Source: d.Name, Target: target, User: target})
	}

	return out, nil
}

// maxCloneDBName is the length of a database user name, which takes the
// database's name.
const maxCloneDBName = 32

func dbBase(site string) string { return strings.ReplaceAll(site, "-", "_") }

// cloneDBName maps a database name of src to one of dst.
func cloneDBName(db, src, dst string) string {
	rest := db
	if p := dbBase(src) + "_"; strings.HasPrefix(db, p) && len(db) > len(p) {
		rest = db[len(p):]
	}

	n := dbBase(dst) + "_" + rest
	if len(n) > maxCloneDBName {
		n = n[:maxCloneDBName]
	}

	return strings.TrimRight(n, "_")
}

// freeCloneDBName returns name or name2 … name9 (shortened to fit), the
// first that no database or user takes.
func (s *Service) freeCloneDBName(ctx context.Context, name string, used map[string]bool) (string, error) {
	for i := 1; i <= 9; i++ {
		n := name
		if i > 1 {
			n = name[:min(len(name), maxCloneDBName-1)] + strconv.Itoa(i)
		}

		if used[n] || agentapi.ValidateDBName(n) != nil || agentapi.ValidateDBUser(n) != nil {
			continue
		}

		if _, err := s.st.DatabaseByName(ctx, n); err == nil {
			continue
		}

		if _, err := s.st.DatabaseOfUser(ctx, n); err == nil {
			continue
		}

		return n, nil
	}

	return "", fmt.Errorf("%w: no free name for a copy of database %s", ErrDatabaseExists, name)
}

func cloneDBList(dbs []store.CloneDatabase) string {
	parts := make([]string, 0, len(dbs))
	for _, d := range dbs {
		parts = append(parts, d.Source+">"+d.Target)
	}

	return strings.Join(parts, " ")
}

// checkCloneSpace refuses a clone when the target pool has less room than
// the source's home plus cloneSpare percent, or the root file system (where
// /var/lib/mysql lies, an assumption) less than the source's databases
// plus cloneSpare. A size the panel cannot read is not checked.
func (s *Service) checkCloneSpace(ctx context.Context, src store.Site, pool string, dbs []store.CloneDatabase) error {
	if s.limits != nil {
		if q, err := s.limits.SiteQuotaGet(ctx, src.Name); err == nil && q.Available {
			pools, err := s.clones.PoolList(ctx)
			if err != nil {
				return err
			}

			for _, p := range pools {
				if p.Name == pool && p.Available && p.FreeBytes < q.Bytes+q.Bytes*cloneSpare/100 {
					return fmt.Errorf("%w: pool %s has %d bytes free, the home needs %d", ErrNoSpace, pool, p.FreeBytes, q.Bytes)
				}
			}
		}
	}

	if len(dbs) == 0 || s.dbSizer == nil {
		return nil
	}

	sizes, err := s.DatabaseSizes(ctx)
	if err != nil {
		return nil //nolint:nilerr // sizes are a check, not a requirement
	}

	var need uint64

	for _, d := range dbs {
		if b := sizes.Bytes[d.Source]; b > 0 {
			need += uint64(b)
		}
	}

	hs, err := s.clones.HostStats(ctx)
	if err != nil {
		return nil //nolint:nilerr // as above
	}

	for _, d := range hs.Disks {
		if d.Name == "root" && d.FreeBytes < need+need*cloneSpare/100 {
			return fmt.Errorf("%w: the database disk has %d bytes free, the databases need %d", ErrNoSpace, d.FreeBytes, need)
		}
	}

	return nil
}

// cloneSettings takes over the stored settings of src (store part) and
// what is sealed to the site or lives in the agent: app environment
// variables and the object cache. A staging clone gets its cron jobs
// paused, password protection on and noindex (ADR-0070). When the clone
// asks for a password, which basic auth users are never copied, it gets
// a generated user; its name and password are returned.
func (s *Service) cloneSettings(ctx context.Context, actor int64, src, dst store.Site, staging bool) (user, pw string, err error) {
	if err := s.st.CopySiteSettings(ctx, src.ID, dst.ID, store.CloneSettings{PauseCron: staging}, s.now()); err != nil {
		return "", "", err
	}

	site, err := s.Get(ctx, dst.Name)
	if err != nil {
		return "", "", err
	}

	pw = agentapi.GenerateAccessPassword(GeneratedAccessPasswordLen)

	hash, err := hashAccessPassword(pw)
	if err != nil {
		return "", "", err
	}

	au := store.AuthUser{SiteID: site.ID, Name: CloneAccessUser, Hash: hash, UpdatedBy: actor}

	if staging {
		if err := s.stagingDefaults(ctx, actor, site, au); err != nil {
			return "", "", err
		}
	}

	if user, err = s.cloneAccessUser(ctx, actor, site, au); err != nil || user == "" {
		pw = ""
	}

	if err != nil {
		return "", "", err
	}

	if s.envBox != nil && src.App != nil {
		env, err := s.AppEnv(ctx, src.Name)
		if err != nil {
			return "", "", err
		}

		if len(env) > 0 {
			if _, err := s.SetAppEnv(ctx, actor, dst.Name, env); err != nil {
				return "", "", err
			}
		}
	}

	if s.cache != nil {
		if oc, err := s.st.ObjectCache(ctx, src.ID); err == nil && oc.Enabled {
			if _, err := s.SetObjectCache(ctx, actor, dst.Name, true, oc.MemoryMB); err != nil {
				return "", "", err
			}
		}
	}

	if l, ok, err := s.st.SiteLimits(ctx, dst.ID); err == nil && ok {
		if err := s.applyLimits(ctx, site, l.Limits); err != nil {
			s.log.WarnContext(ctx, "apply the limits of a clone", "site", dst.Name, "err", err)
		}
	}

	if s.cron != nil {
		if err := s.applyCron(ctx, site); err != nil {
			return "", "", err
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	return user, pw, s.applyWeb(ctx)
}

// stagingDefaults stores what a staging clone gets on top of the copied
// settings: password protection for the whole site (the source's IP rules
// and bypass addresses stay) and X-Robots-Tag noindex. A snippet that
// already sets one of them makes the clone fail with ErrInvalid.
func (s *Service) stagingDefaults(ctx context.Context, actor int64, site store.Site, au store.AuthUser) error {
	sn, err := s.st.SiteSnippets(ctx, site.ID)
	if err != nil {
		return err
	}

	h, err := s.st.SiteHeaders(ctx, site.ID)
	if err != nil {
		return err
	}

	h.Headers.NoIndex = true
	if err := checkHeaders(h.Headers, sn); err != nil {
		return err
	}

	if _, err := s.st.SetSiteHeaders(ctx, store.SiteHeaders{SiteID: site.ID, Headers: h.Headers, UpdatedBy: actor}, s.now()); err != nil {
		return err
	}

	a, err := s.st.SiteAccess(ctx, site.ID)
	if err != nil {
		return err
	}

	a.Settings.Password, a.Settings.Paths = true, nil

	// The generated user comes next; check the rules with it.
	if err := s.checkAccess(a.Settings, []store.AuthUser{au}, sn); err != nil {
		return err
	}

	_, err = s.st.SetSiteAccess(ctx, store.SiteAccess{SiteID: site.ID, Settings: a.Settings, UpdatedBy: actor}, s.now())

	return err
}

// cloneAccessUser adds the generated basic auth user au to a clone whose
// access setting asks for a password (users are never copied) and
// returns its name; "" when the clone asks for none.
func (s *Service) cloneAccessUser(ctx context.Context, actor int64, site store.Site, au store.AuthUser) (string, error) {
	a, err := s.st.SiteAccess(ctx, site.ID)
	if err != nil || !a.Settings.Password {
		return "", err
	}

	if err := s.st.AddAuthUser(ctx, au, s.now()); err != nil {
		return "", err
	}

	s.audit(ctx, actor, "site.access.user.add", site.Name, map[string]string{"user": au.Name, "generated": "true"})

	return au.Name, nil
}

// kickClones wakes RunClones.
func (s *Service) kickClones() {
	select {
	case s.cloneKick <- struct{}{}:
	default:
	}
}

// RunClones follows the running copies until ctx ends.
func (s *Service) RunClones(ctx context.Context) {
	if s.clones == nil {
		return
	}

	t := time.NewTicker(clonePoll)
	defer t.Stop()

	for {
		s.pollClones(ctx)

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.cloneKick:
		}
	}
}

func (s *Service) pollClones(ctx context.Context) {
	all, err := s.st.SiteClones(ctx, 0)
	if err != nil {
		s.log.ErrorContext(ctx, "list clones", "err", err)

		return
	}

	for _, c := range all {
		if ctx.Err() != nil {
			return
		}

		if c.State == store.CloneCopying {
			s.stepClone(ctx, c)
		}
	}
}

// stepClone moves one copy on: it starts the home copy, then each
// database copy in turn, and finishes the clone when all are done.
func (s *Service) stepClone(ctx context.Context, c store.SiteClone) {
	site, err := s.st.SiteByID(ctx, c.SiteID)
	if err != nil {
		s.log.ErrorContext(ctx, "read a clone", "site", c.SiteID, "err", err)

		return
	}

	if c.SourceID == 0 {
		s.failClone(ctx, site, c, "the source site was deleted")

		return
	}

	src, err := s.st.SiteByID(ctx, c.SourceID)
	if err != nil {
		s.failClone(ctx, site, c, "the source site was deleted")

		return
	}

	changed, msg, err := s.advanceClone(ctx, src, site, &c)

	switch {
	case msg != "":
		s.failClone(ctx, site, c, msg)
	case err != nil:
		// The agent is unavailable or busy: try again on the next poll.
		s.log.WarnContext(ctx, "clone step", "site", site.Name, "err", err)
	case c.HomeDone && allDBsDone(c.Databases):
		// The source is copied: it leaves maintenance before the clone's
		// configuration, which touches only the clone.
		if c.SourceMaintenance {
			s.releaseSource(ctx, src, &c)

			changed = true
		}

		// Keep the last copy's result before the configuration starts.
		if changed {
			if err := s.st.UpdateSiteClone(ctx, c, s.now()); err != nil {
				s.log.ErrorContext(ctx, "save clone progress", "site", site.Name, "err", err)

				return
			}
		}

		s.configureStep(ctx, src, site, c)
	case changed:
		if err := s.st.UpdateSiteClone(ctx, c, s.now()); err != nil {
			s.log.ErrorContext(ctx, "save clone progress", "site", site.Name, "err", err)
		}
	}
}

// advanceClone does at most one step: start or check the home copy, else
// start or check the next database copy. msg is set when the copy failed.
func (s *Service) advanceClone(ctx context.Context, src, site store.Site, c *store.SiteClone) (changed bool, msg string, err error) {
	if !c.HomeDone {
		if c.HomeJob == "" {
			j, err := s.clones.SiteHomeCopy(ctx, agentapi.SiteHomeCopyRequest{Source: src.Name, Target: site.Name, Replace: c.Refresh})
			if err != nil {
				if isCode(err, agentrpc.CodeConflict) {
					return false, "", err
				}

				return false, "the copy of the files could not start", nil
			}

			c.HomeJob = j.ID

			return true, "", nil
		}

		j, err := s.clones.SiteHomeCopyStatus(ctx, c.HomeJob)

		switch {
		case isCode(err, agentrpc.CodeNotFound):
			return false, "the copy of the files was cut off by an agent restart", nil
		case err != nil:
			return false, "", err
		case j.State == agentapi.JobFailed:
			return false, "copy of the files: " + j.Error, nil
		case j.State == agentapi.JobDone:
			c.HomeDone = true

			return true, "", nil
		}

		return false, "", nil
	}

	for i := range c.Databases {
		d := &c.Databases[i]
		if d.Done {
			continue
		}

		if d.Job == "" {
			j, err := s.clones.DBCopy(ctx, d.Source, d.Target)
			if err != nil {
				if isCode(err, agentrpc.CodeConflict) || isCode(err, agentrpc.CodePrecondition) {
					return false, "", err
				}

				return false, "the copy of database " + d.Source + " could not start", nil
			}

			d.Job = j.ID

			return true, "", nil
		}

		j, err := s.ag.DBJob(ctx, d.Job)

		switch {
		case isCode(err, agentrpc.CodeNotFound):
			return false, "the copy of database " + d.Source + " was lost by the agent", nil
		case err != nil:
			return false, "", err
		case j.State == agentapi.JobFailed:
			return false, "copy of database " + d.Source + ": " + j.Error, nil
		case j.State == agentapi.JobDone:
			d.Done = true

			return true, "", nil
		}

		return false, "", nil
	}

	return false, "", nil
}

// configureStep runs the configuration step once files and databases are
// copied, then finishes the clone.
func (s *Service) configureStep(ctx context.Context, src, site store.Site, c store.SiteClone) {
	changed, done, msg, err := s.configureClone(ctx, src, site, &c)

	switch {
	case msg != "":
		s.failClone(ctx, site, c, msg)
	case err != nil:
		s.log.WarnContext(ctx, "clone configuration", "site", site.Name, "err", err)
	case done:
		s.finishClone(ctx, src, site, c)
	case changed:
		if err := s.st.UpdateSiteClone(ctx, c, s.now()); err != nil {
			s.log.ErrorContext(ctx, "save clone progress", "site", site.Name, "err", err)
		}
	}
}

// configureClone points a WordPress, Laravel or Symfony clone at its
// databases and its domain (ADR-0070): it sets new passwords for the
// clone's database users, which the panel never stores, and starts the
// agent's run (WP-CLI, or the .env edits), then waits for it. A clone of
// another application needs no step (done at once).
func (s *Service) configureClone(ctx context.Context, src, site store.Site, c *store.SiteClone) (changed, done bool, msg string, err error) {
	if c.Config == store.CloneConfigDone {
		return false, true, "", nil
	}

	if c.Config == store.CloneConfigNone {
		p, err := s.st.SiteProfile(ctx, site.ID)
		if err != nil {
			return false, false, "", err
		}

		kind := map[string]string{profile.WordPress: profile.WordPress, profile.Laravel: agentapi.DotEnvLaravel, profile.Symfony: agentapi.DotEnvSymfony}[p.Profile]
		if kind == "" || !hasDBUser(c.Databases) {
			c.Config = store.CloneConfigDone

			return true, true, "", nil
		}

		req, err := s.wpCloneRequest(ctx, src, site, *c)
		if err != nil {
			s.log.WarnContext(ctx, "set the database passwords of a clone", "site", site.Name, "err", err)

			return false, false, "the database passwords of the clone could not be set", nil
		}

		if kind == profile.WordPress {
			_, err = s.clones.WPCloneStart(ctx, req)
		} else {
			_, err = s.clones.DotEnvCloneStart(ctx, agentapi.DotEnvCloneRequest{
				Site: req.Site, Kind: kind, WebDir: req.WebDir, URL: req.URL, Databases: req.Databases, DBPasswords: req.DBPasswords,
			})
		}

		if err != nil {
			if isCode(err, agentrpc.CodeConflict) {
				// Another run of the site is busy: try again (with new
				// passwords then, which is fine).
				return false, false, "", err
			}

			return false, false, "the configuration could not start: " + agentMessage(err), nil
		}

		c.Config = store.CloneConfigRunning

		return true, false, "", nil
	}

	st, err := s.clones.WPCloneStatus(ctx, site.Name)

	switch {
	case err != nil:
		return false, false, "", err
	case st.State == agentapi.DeploySucceeded:
		c.Config = store.CloneConfigDone

		return true, true, "", nil
	case st.State == agentapi.DeployRunning:
		return false, false, "", nil
	}

	reason := st.Error
	if reason == "" {
		reason = "the run failed"
	}

	if last := lastLogLine(st.Log); last != "" {
		reason += ": " + last
	}

	return false, false, "configuration: " + reason, nil
}

// maxReasonLog bounds the log line a failed configuration step adds to
// the clone's error.
const maxReasonLog = 300

// lastLogLine is the last non-empty line of a run's log, which names the
// step that failed, after WP-CLI's last error line; the runner keeps
// passwords out of the log.
func lastLogLine(log string) string {
	lines := strings.Split(strings.TrimSpace(log), "\n")
	last := strings.TrimSpace(lines[len(lines)-1])

	// WP-CLI's own message ("Error: ...") comes before the runner's line
	// that names the command.
	for i := len(lines) - 2; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "Error: ") {
			last = l + " (" + last + ")"

			break
		}
	}

	if len(last) > maxReasonLog {
		last = strings.ToValidUTF8(last[:maxReasonLog], "") + "…"
	}

	return last
}

// wpCloneRequest sets a new password for every database user of the
// clone and builds the agent's request. The clone's address is https when
// it wants HTTPS: WordPress then loads once its certificate exists.
func (s *Service) wpCloneRequest(ctx context.Context, src, site store.Site, c store.SiteClone) (agentapi.WPCloneRequest, error) {
	scheme := "http"
	if site.TLS {
		scheme = "https"
	}

	req := agentapi.WPCloneRequest{
		Site: site.Name, PHP: site.PHP, WebDir: site.WebDir, URL: scheme + "://" + site.Domains[0],
		From: src.Domains, Staging: c.Staging, DBPasswords: map[string]string{},
	}

	// A database without a user cannot be WordPress's.
	for _, d := range c.Databases {
		if d.User == "" {
			continue
		}

		req.Databases = append(req.Databases, agentapi.WPCloneDB{Source: d.Source, Target: d.Target, User: d.User})

		if _, ok := req.DBPasswords[d.User]; ok {
			continue
		}

		pw, err := s.SetDatabaseUserPassword(ctx, c.StartedBy, d.Target, d.User, "")
		if err != nil {
			return agentapi.WPCloneRequest{}, err
		}

		req.DBPasswords[d.User] = pw
	}

	return req, nil
}

func hasDBUser(dbs []store.CloneDatabase) bool {
	for _, d := range dbs {
		if d.User != "" {
			return true
		}
	}

	return false
}

func allDBsDone(dbs []store.CloneDatabase) bool {
	for _, d := range dbs {
		if !d.Done {
			return false
		}
	}

	return true
}

func (s *Service) finishClone(ctx context.Context, src, site store.Site, c store.SiteClone) {
	c.State = store.CloneDone

	if err := s.st.UpdateSiteClone(ctx, c, s.now()); err != nil {
		s.log.ErrorContext(ctx, "finish a clone", "site", site.Name, "err", err)

		return
	}

	// An app starts on the copied files.
	if site.App != nil && s.apps != nil {
		if _, err := s.AppAction(ctx, c.StartedBy, site.Name, "restart"); err != nil {
			s.log.WarnContext(ctx, "restart the app of a clone", "site", site.Name, "err", err)
		}
	}

	s.audit(ctx, c.StartedBy, "site.clone.done", site.Name, map[string]string{"source": src.Name, "refresh": strconv.FormatBool(c.Refresh)})
}

// failClone records the error. A first copy that failed is deleted with
// its databases (ADR-0070); a refresh keeps the clone, marked failed.
func (s *Service) failClone(ctx context.Context, site store.Site, c store.SiteClone, msg string) {
	if c.SourceMaintenance && c.SourceID != 0 {
		if src, err := s.st.SiteByID(ctx, c.SourceID); err == nil {
			s.releaseSource(ctx, src, &c)
		}
	}

	c.State, c.Error = store.CloneFailed, msg

	if err := s.st.UpdateSiteClone(ctx, c, s.now()); err != nil {
		s.log.ErrorContext(ctx, "record a failed clone", "site", site.Name, "err", err)
	}

	s.audit(ctx, c.StartedBy, "site.clone.failed", site.Name, map[string]string{"error": msg, "refresh": strconv.FormatBool(c.Refresh)})

	if c.Refresh {
		return
	}

	if err := s.Delete(ctx, c.StartedBy, site.Name); err != nil {
		s.log.ErrorContext(ctx, "remove a failed clone", "site", site.Name, "err", err)
	}
}

// sourceMaintenance switches the maintenance of a clone's source and
// keeps its page and allowed addresses.
func (s *Service) sourceMaintenance(ctx context.Context, actor int64, src store.Site, on bool) error {
	_, err := s.SetMaintenance(ctx, actor, src.Name, Maintenance{Enabled: on, Page: pageIDString(src.MaintenancePage), AllowIPs: src.MaintenanceAllow})

	return err
}

// releaseSource takes the source out of the maintenance the copy put it
// in. A failure is logged and the flag kept, so the next step tries again.
func (s *Service) releaseSource(ctx context.Context, src store.Site, c *store.SiteClone) {
	if err := s.sourceMaintenance(ctx, c.StartedBy, src, false); err != nil {
		s.log.ErrorContext(ctx, "take the source of a clone out of maintenance", "source", src.Name, "err", err)

		return
	}

	c.SourceMaintenance = false
}

// agentMessage is the agent's text of an error, for the clone's error.
func agentMessage(err error) string {
	var e *agentrpc.Error
	if errors.As(err, &e) {
		return e.Message
	}

	return "the agent did not answer"
}
