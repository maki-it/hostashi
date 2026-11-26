// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/profile"
	"github.com/maki-it/hostashi/internal/store"
)

// fakeClones plays the agent's copy jobs; db jobs go into the fake
// agent's job list, so DBJob finds them.
type fakeClones struct {
	mu     sync.Mutex
	ag     *fakeAgent
	homes  map[string]agentapi.SiteHomeCopyJob
	copies []agentapi.SiteHomeCopyRequest
	dbs    []agentapi.DBCopyRequest
	n      int
	// state is the state a new job reports on its first status call.
	state, dbState string
	homeErr        error
	poolFree       uint64
	// wp are the WordPress configurations started; wpState is what their
	// status reports, wpErr what a start answers.
	wp      []agentapi.WPCloneRequest
	wpState string
	wpErr   error
}

func newFakeClones(ag *fakeAgent) *fakeClones {
	return &fakeClones{ag: ag, homes: map[string]agentapi.SiteHomeCopyJob{}, state: agentapi.JobDone, dbState: agentapi.JobDone, poolFree: 1 << 40, wpState: agentapi.DeploySucceeded}
}

func (f *fakeClones) id() string {
	f.n++

	return fmt.Sprintf("%016x", f.n)
}

func (f *fakeClones) SiteHomeCopy(_ context.Context, req agentapi.SiteHomeCopyRequest) (agentapi.SiteHomeCopyJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.homeErr != nil {
		return agentapi.SiteHomeCopyJob{}, f.homeErr
	}

	f.copies = append(f.copies, req)
	j := agentapi.SiteHomeCopyJob{ID: f.id(), Source: req.Source, Target: req.Target, Replace: req.Replace, State: agentapi.JobRunning}
	f.homes[j.ID] = j

	return j, nil
}

func (f *fakeClones) SiteHomeCopyStatus(_ context.Context, id string) (agentapi.SiteHomeCopyJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.homes[id]
	if !ok {
		return j, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown copy job"}
	}

	j.State = f.state
	if j.State == agentapi.JobFailed {
		j.Error = "an entry exists in the target home already"
	}

	return j, nil
}

func (f *fakeClones) DBCopy(_ context.Context, source, target string) (agentapi.DBJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.dbs = append(f.dbs, agentapi.DBCopyRequest{Source: source, Target: target})
	j := agentapi.DBJob{ID: f.id(), Kind: "copy", Source: source, Database: target, State: f.dbState}

	if j.State == agentapi.JobFailed {
		j.Error = "server error 1146 at line 3 of the dump"
	}

	f.ag.mu.Lock()
	f.ag.jobs[j.ID] = j
	f.ag.mu.Unlock()

	return j, nil
}

func (f *fakeClones) WPCloneStart(_ context.Context, req agentapi.WPCloneRequest) (agentapi.WPInstallStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := req.Validate(); err != nil {
		return agentapi.WPInstallStatus{}, &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: err.Error()}
	}

	if f.wpErr != nil {
		return agentapi.WPInstallStatus{}, f.wpErr
	}

	f.wp = append(f.wp, req)

	return agentapi.WPInstallStatus{Site: req.Site, State: agentapi.DeployRunning}, nil
}

func (f *fakeClones) WPCloneStatus(_ context.Context, site string) (agentapi.WPInstallStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	st := agentapi.WPInstallStatus{Site: site, State: f.wpState}
	if st.State == agentapi.DeployFailed {
		st.Error = "a step failed, see the log"
	}

	return st, nil
}

func (f *fakeClones) PoolList(context.Context) ([]agentapi.PoolInfo, error) {
	return []agentapi.PoolInfo{{Name: agentapi.DefaultPool, Available: true, FreeBytes: f.poolFree}}, nil
}

func (f *fakeClones) HostStats(context.Context) (agentapi.HostStats, error) {
	return agentapi.HostStats{Disks: []agentapi.HostDisk{{Name: "root", FreeBytes: 1 << 40}}}, nil
}

// cloneEnv has the PHP site "shop" with a database shop_wp and its user.
func cloneEnv(t *testing.T) (*Service, *fakeAgent, *fakeClones, *store.Store) {
	t.Helper()

	s, ag, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", PHP: "8.4", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_wp", User: "shop_wp"}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "legacy"}); err != nil {
		t.Fatal(err)
	}

	f := newFakeClones(ag)
	s.EnableClones(f)

	return s, ag, f, st
}

func cloneRow(t *testing.T, s *Service, name string) CloneView {
	t.Helper()

	v, err := s.Clone(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}

	return v
}

// pollUntil runs the clone poller until the clone leaves copying.
func pollUntil(t *testing.T, s *Service, name string) CloneView {
	t.Helper()

	for range 20 {
		s.pollClones(t.Context())

		v, err := s.Clone(t.Context(), name)
		if errors.Is(err, ErrNotFound) {
			return CloneView{}
		}

		if err != nil {
			t.Fatal(err)
		}

		if v.State != store.CloneCopying {
			return v
		}
	}

	t.Fatal("clone did not finish")

	return CloneView{}
}

func TestCloneSite(t *testing.T) {
	s, ag, f, st := cloneEnv(t)
	ctx := t.Context()

	cron := &fakeCron{applied: map[string][]agentapi.CronJob{}}
	s.EnableCron(cron)

	if _, err := s.CreateCronJob(ctx, 1, "shop", CronInput{Schedule: "*/5 * * * *", Command: "php cron.php"}); err != nil {
		t.Fatal(err)
	}

	site, err := s.CloneSite(ctx, 1, "shop", CloneInput{Name: "shop-stage", Domain: "stage.shop.example.com", Staging: true})
	if err != nil {
		t.Fatal(err)
	}

	if site.PHP != "8.4" || site.Pool != agentapi.DefaultPool || site.Domains[0] != "stage.shop.example.com" {
		t.Fatalf("site %+v", site)
	}

	checkStagingAccess(t, ag, site)

	v := cloneRow(t, s, "shop-stage")
	if v.State != store.CloneCopying || v.Source != "shop" || !v.Staging || len(v.Databases) != 2 {
		t.Fatalf("clone %+v", v)
	}

	// The source's prefix is replaced, other names get the clone's prefix.
	if v.Databases[0].Source != "legacy" || v.Databases[0].Target != "shop_stage_legacy" || v.Databases[1].Target != "shop_stage_wp" {
		t.Fatalf("databases %+v", v.Databases)
	}

	if ag.dbs["shop_stage_wp"] != "shop-stage" || ag.dbUsers["shop_stage_wp"] != "shop_stage_wp" {
		t.Fatalf("clone databases %v %v", ag.dbs, ag.dbUsers)
	}

	// A staging clone has its cron jobs paused.
	jobs, err := s.CronJobs(ctx, "shop-stage")
	if err != nil || len(jobs) != 1 || !jobs[0].Paused {
		t.Fatalf("cron %+v %v", jobs, err)
	}

	if _, ok := cron.applied["shop-stage"]; !ok {
		t.Error("cron jobs of the clone not sent to the agent")
	}

	// While it copies, neither site can be cloned again.
	if _, err := s.CloneSite(ctx, 1, "shop", CloneInput{Name: "shop-two", Domain: "two.shop.example.com"}); !errors.Is(err, ErrCloneBusy) {
		t.Fatalf("second clone while copying: %v", err)
	}

	v = pollUntil(t, s, "shop-stage")
	if v.State != store.CloneDone || !v.HomeDone || !allDBsDone(v.Databases) || v.FinishedAt.IsZero() {
		t.Fatalf("finished %+v", v)
	}

	if len(f.copies) != 1 || f.copies[0].Source != "shop" || f.copies[0].Target != "shop-stage" || f.copies[0].Replace {
		t.Fatalf("home copies %+v", f.copies)
	}

	if len(f.dbs) != 2 || f.dbs[1] != (agentapi.DBCopyRequest{Source: "shop_wp", Target: "shop_stage_wp"}) {
		t.Fatalf("db copies %+v", f.dbs)
	}

	entries := auditActions(t, st)
	for _, want := range []string{"site.clone", "site.clone.done"} {
		if !entries[want] {
			t.Errorf("no audit entry %s in %v", want, entries)
		}
	}

	// Refresh copies again, replacing the clone's files.
	if err := s.RefreshClone(ctx, 1, "shop-stage"); err != nil {
		t.Fatal(err)
	}

	if err := s.RefreshClone(ctx, 1, "shop-stage"); !errors.Is(err, ErrCloneBusy) {
		t.Fatalf("second refresh: %v", err)
	}

	v = pollUntil(t, s, "shop-stage")
	if v.State != store.CloneDone || !v.Refresh || len(f.copies) != 2 || !f.copies[1].Replace || len(f.dbs) != 4 {
		t.Fatalf("refresh %+v %+v", v, f.copies)
	}

	// The source is no clone and cannot be refreshed.
	if err := s.RefreshClone(ctx, 1, "shop"); !errors.Is(err, ErrNotClone) {
		t.Fatalf("refresh of the source: %v", err)
	}
}

// checkStagingAccess asserts the staging defaults that reached the agent:
// the whole site behind the generated user, whose password is the one
// returned, and noindex.
func checkStagingAccess(t *testing.T, ag *fakeAgent, c CloneStarted) {
	t.Helper()

	if c.AccessUser != CloneAccessUser || len(c.AccessPassword) != GeneratedAccessPasswordLen {
		t.Fatalf("access user %q password %q", c.AccessUser, c.AccessPassword)
	}

	for _, w := range ag.web.Sites {
		if w.User != c.Name {
			continue
		}

		if w.Headers == nil || !w.Headers.NoIndex {
			t.Errorf("headers %+v", w.Headers)
		}

		if w.Access == nil || w.Access.Auth == nil || len(w.Access.Auth.Paths) != 0 || len(w.Access.Auth.Users) != 1 {
			t.Fatalf("access %+v", w.Access)
		}

		u := w.Access.Auth.Users[0]
		if u.Name != CloneAccessUser || bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(c.AccessPassword)) != nil {
			t.Errorf("user %s does not match the returned password", u.Name)
		}

		return
	}

	t.Fatalf("clone %s not in the web state", c.Name)
}

// A plain clone of a password-protected site gets a generated user: the
// users are not copied, and the rule without one would break nginx.
func TestCloneSitePasswordSource(t *testing.T) {
	s, _, _, _ := cloneEnv(t)
	ctx := t.Context()

	s.localAddrs = func() ([]netip.Addr, error) { return nil, nil }

	if _, err := s.AddAccessUser(ctx, 1, "shop", "owner", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetAccess(ctx, 1, "shop", store.AccessSettings{Password: true, Paths: []string{"/wp-admin"}}); err != nil {
		t.Fatal(err)
	}

	c, err := s.CloneSite(ctx, 1, "shop", CloneInput{Name: "shop-copy", Domain: "copy.shop.example.com"})
	if err != nil {
		t.Fatal(err)
	}

	if c.AccessUser != CloneAccessUser || c.AccessPassword == "" {
		t.Fatalf("access %q %q", c.AccessUser, c.AccessPassword)
	}

	v, err := s.Access(ctx, "shop-copy")
	if err != nil || !v.Settings.Password || len(v.Settings.Paths) != 1 || len(v.Users) != 1 || v.Users[0].Name != CloneAccessUser {
		t.Fatalf("access of the clone %+v %v", v, err)
	}

	h, err := s.Headers(ctx, "shop-copy")
	if err != nil || h.Headers.NoIndex {
		t.Fatalf("a plain clone got noindex: %+v %v", h, err)
	}

	// A plain clone of a site without a password has no user.
	if _, err := s.SetAccess(ctx, 1, "shop", store.AccessSettings{}); err != nil {
		t.Fatal(err)
	}

	pollUntil(t, s, "shop-copy")

	c, err = s.CloneSite(ctx, 1, "shop", CloneInput{Name: "shop-two", Domain: "two.shop.example.com"})
	if err != nil || c.AccessUser != "" || c.AccessPassword != "" {
		t.Fatalf("clone without password: %+v %v", c, err)
	}
}

// A snippet that sets X-Robots-Tag itself conflicts with noindex.
func TestCloneSiteStagingSnippetConflict(t *testing.T) {
	s, _, _, st := cloneEnv(t)
	ctx := t.Context()

	src, err := st.SiteByName(ctx, "shop")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.SetSiteSnippets(ctx, store.SiteSnippets{SiteID: src.ID, Server: `add_header X-Robots-Tag "all";`}, s.now()); err != nil {
		t.Fatal(err)
	}

	if _, err := s.CloneSite(ctx, 1, "shop", CloneInput{Name: "shop-stage", Domain: "stage.shop.example.com", Staging: true}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("got %v", err)
	}

	if _, err := s.Get(ctx, "shop-stage"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the failed clone is left: %v", err)
	}
}

func auditActions(t *testing.T, st *store.Store) map[string]bool {
	t.Helper()

	list, _, err := st.AuditEntries(t.Context(), store.AuditFilter{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}

	out := map[string]bool{}
	for _, e := range list {
		out[e.Action] = true
	}

	return out
}

func TestCloneSiteFailureDeletesClone(t *testing.T) {
	s, ag, f, _ := cloneEnv(t)
	ctx := t.Context()

	f.dbState = agentapi.JobFailed

	if _, err := s.CloneSite(ctx, 1, "shop", CloneInput{Name: "shop-copy", Domain: "copy.shop.example.com"}); err != nil {
		t.Fatal(err)
	}

	pollUntil(t, s, "shop-copy")

	if _, err := s.Get(ctx, "shop-copy"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed clone kept: %v", err)
	}

	if _, ok := ag.dbs["shop_copy_wp"]; ok || ag.accounts["shop-copy"] {
		t.Fatalf("databases or account of the failed clone kept: %v %v", ag.dbs, ag.accounts)
	}

	// The source is untouched.
	if _, ok := ag.dbs["shop_wp"]; !ok {
		t.Fatal("source database removed")
	}
}

func TestCloneRefreshFailureKeepsClone(t *testing.T) {
	s, _, f, _ := cloneEnv(t)
	ctx := t.Context()

	if _, err := s.CloneSite(ctx, 1, "shop", CloneInput{Name: "shop-stage", Domain: "stage.shop.example.com", Staging: true}); err != nil {
		t.Fatal(err)
	}

	pollUntil(t, s, "shop-stage")

	f.state = agentapi.JobFailed

	if err := s.RefreshClone(ctx, 1, "shop-stage"); err != nil {
		t.Fatal(err)
	}

	v := pollUntil(t, s, "shop-stage")
	if v.State != store.CloneFailed || !strings.Contains(v.Error, "entry exists") {
		t.Fatalf("failed refresh %+v", v)
	}
}

func TestCloneHomeJobLost(t *testing.T) {
	s, _, f, _ := cloneEnv(t)
	ctx := t.Context()

	f.state = agentapi.JobRunning

	if _, err := s.CloneSite(ctx, 1, "shop", CloneInput{Name: "shop-copy", Domain: "copy.shop.example.com"}); err != nil {
		t.Fatal(err)
	}

	s.pollClones(ctx) // starts the home copy
	s.pollClones(ctx) // still running

	if v := cloneRow(t, s, "shop-copy"); v.State != store.CloneCopying || v.Home == nil {
		t.Fatalf("running %+v", v)
	}

	// The agent restarted and forgot the job.
	f.mu.Lock()
	f.homes = map[string]agentapi.SiteHomeCopyJob{}
	f.mu.Unlock()

	s.pollClones(ctx)

	if _, err := s.Get(ctx, "shop-copy"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("clone with a lost job kept: %v", err)
	}
}

func TestCloneSiteRefuses(t *testing.T) {
	s, _, f, _ := cloneEnv(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "go", Domains: []string{"go.example.com"}, Redirect: &store.SiteRedirect{Target: "https://example.com"}}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, source string
		in           CloneInput
		want         error
	}{
		{"unknown source", "nope", CloneInput{Name: "x-copy", Domain: "x.example.com"}, ErrNotFound},
		{"redirect site", "go", CloneInput{Name: "go-copy", Domain: "go2.example.com"}, ErrSiteType},
		{"bad name", "shop", CloneInput{Name: "../x", Domain: "x.example.com"}, ErrInvalid},
		{"name taken", "shop", CloneInput{Name: "shop", Domain: "x.example.com"}, ErrExists},
		{"domain taken", "shop", CloneInput{Name: "shop-copy", Domain: "shop.example.com"}, ErrDomainTaken},
	}

	for _, c := range cases {
		if _, err := s.CloneSite(ctx, 1, c.source, c.in); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}

	// A failed create leaves no clone row and no databases behind.
	if _, err := s.Get(ctx, "shop-copy"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("half clone kept: %v", err)
	}

	if dbs, _ := s.Databases(ctx, ""); len(dbs) != 2 {
		t.Fatalf("databases %+v", dbs)
	}

	s.clones = nil
	if _, err := s.CloneSite(ctx, 1, "shop", CloneInput{Name: "shop-copy", Domain: "x.example.com"}); !errors.Is(err, ErrNoClone) {
		t.Fatalf("without agent: %v", err)
	}

	_ = f
}

func TestCloneDBName(t *testing.T) {
	cases := []struct{ db, src, dst, want string }{
		{"shop_wp", "shop", "shop-stage", "shop_stage_wp"},
		{"legacy", "shop", "stage", "stage_legacy"},
		{"my_blog_wp", "my-blog", "blog2", "blog2_wp"},
		{"shop_", "shop", "stage", "stage_shop"},
		{"averyveryverylongdatabasename_x", "shop", "a-long-staging-name", "a_long_staging_name_averyveryver"},
	}

	for _, c := range cases {
		if got := cloneDBName(c.db, c.src, c.dst); got != c.want {
			t.Errorf("cloneDBName(%q, %q, %q) = %q, want %q", c.db, c.src, c.dst, got, c.want)
		}
	}
}

func TestCloneNoSpace(t *testing.T) {
	s, _, f, _ := cloneEnv(t)

	s.EnableLimits(newFakeLimits()) // reports 1 GiB in the home

	f.poolFree = 1<<30 + 1

	if _, err := s.CloneSite(t.Context(), 1, "shop", CloneInput{Name: "shop-copy", Domain: "copy.shop.example.com"}); !errors.Is(err, ErrNoSpace) {
		t.Fatalf("no space: %v", err)
	}

	f.poolFree = 1 << 31

	if _, err := s.CloneSite(t.Context(), 1, "shop", CloneInput{Name: "shop-copy", Domain: "copy.shop.example.com"}); err != nil {
		t.Fatal(err)
	}
}

// wordPressSource gives the source the WordPress profile, which the
// clone takes over.
func wordPressSource(t *testing.T, st *store.Store) {
	t.Helper()

	src, err := st.SiteByName(t.Context(), "shop")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := st.SetSiteProfile(t.Context(), store.SiteProfile{SiteID: src.ID, Profile: profile.WordPress}, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestCloneSiteWordPress(t *testing.T) {
	s, _, f, st := cloneEnv(t)
	ctx := t.Context()

	wordPressSource(t, st)

	f.wpState = agentapi.DeployRunning
	f.wpErr = &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "a WordPress install of this site is running"}

	if _, err := s.CloneSite(ctx, 1, "shop", CloneInput{Name: "shop-stage", Domain: "stage.shop.example.com", Staging: true}); err != nil {
		t.Fatal(err)
	}

	// Files and databases are copied; the run of the site is busy, so the
	// configuration waits.
	for range 10 {
		s.pollClones(ctx)
	}

	if v := cloneRow(t, s, "shop-stage"); v.State != store.CloneCopying || v.Config != store.CloneConfigNone || !v.HomeDone || !allDBsDone(v.Databases) {
		t.Fatalf("while busy %+v", v)
	}

	f.wpErr = nil

	s.pollClones(ctx)

	if v := cloneRow(t, s, "shop-stage"); v.State != store.CloneCopying || v.Config != store.CloneConfigRunning || len(f.wp) != 1 {
		t.Fatalf("running %+v %d", v, len(f.wp))
	}

	// Every copied database goes along; the run picks wp-config.php's.
	req := f.wp[0]
	if req.Site != "shop-stage" || req.URL != "http://stage.shop.example.com" || len(req.From) != 1 || req.From[0] != "shop.example.com" || !req.Staging ||
		len(req.Databases) != 2 || req.Databases[1] != (agentapi.WPCloneDB{Source: "shop_wp", Target: "shop_stage_wp", User: "shop_stage_wp"}) ||
		len(req.DBPasswords["shop_stage_wp"]) < agentapi.MinWPPassword || len(req.DBPasswords) != 2 {
		t.Fatalf("request %+v", req)
	}

	f.wpState = agentapi.DeploySucceeded

	v := pollUntil(t, s, "shop-stage")
	if v.State != store.CloneDone || v.Config != store.CloneConfigDone {
		t.Fatalf("done %+v", v)
	}

	// A refresh configures again.
	if err := s.RefreshClone(ctx, 1, "shop-stage"); err != nil {
		t.Fatal(err)
	}

	if v := pollUntil(t, s, "shop-stage"); v.State != store.CloneDone || len(f.wp) != 2 {
		t.Fatalf("refresh %+v %d", v, len(f.wp))
	}
}

func TestCloneSiteWordPressFails(t *testing.T) {
	s, _, f, st := cloneEnv(t)
	ctx := t.Context()

	wordPressSource(t, st)

	f.wpState = agentapi.DeployFailed

	if _, err := s.CloneSite(ctx, 1, "shop", CloneInput{Name: "shop-copy", Domain: "copy.shop.example.com"}); err != nil {
		t.Fatal(err)
	}

	pollUntil(t, s, "shop-copy")

	if _, err := s.Get(ctx, "shop-copy"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a clone whose configuration failed is left: %v", err)
	}

	list, _, err := st.AuditEntries(ctx, store.AuditFilter{}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range list {
		if e.Action == "site.clone.failed" {
			if !strings.Contains(string(e.Detail), "WordPress configuration: a step failed") {
				t.Errorf("detail %s", e.Detail)
			}

			return
		}
	}

	t.Fatal("no audit entry site.clone.failed")
}

// A clone of a site without the WordPress profile needs no step.
func TestCloneSiteNoConfiguration(t *testing.T) {
	s, _, f, _ := cloneEnv(t)

	if _, err := s.CloneSite(t.Context(), 1, "shop", CloneInput{Name: "shop-copy", Domain: "copy.shop.example.com"}); err != nil {
		t.Fatal(err)
	}

	if v := pollUntil(t, s, "shop-copy"); v.State != store.CloneDone || v.Config != store.CloneConfigDone || len(f.wp) != 0 {
		t.Fatalf("%+v %d", v, len(f.wp))
	}
}
