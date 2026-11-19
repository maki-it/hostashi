// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/store"
)

// fakeAgent keeps accounts and the live web state in memory, like the real
// agent: delete is refused while the site has a virtual host.
type fakeAgent struct {
	mu       sync.Mutex
	accounts map[string]bool
	web      agentapi.WebApplyRequest
	applies  int

	createErr, deleteErr, webErr, tlsErr error
	installed                            int

	dbs     map[string]string // database -> site
	dbUsers map[string]string // user -> database
	dbErr   error             // returned by every db call while set
	userErr error             // returned by DBUserCreate while set
	jobs    map[string]agentapi.DBJob

	pages     map[string]map[string][]byte // "<id>.<digest>" -> files
	uploads   int
	pageErr   error
	pageGones []string
}

func newFake() *fakeAgent {
	return &fakeAgent{accounts: map[string]bool{}, dbs: map[string]string{}, dbUsers: map[string]string{}, jobs: map[string]agentapi.DBJob{}, pages: map[string]map[string][]byte{}}
}

func (f *fakeAgent) SiteCreate(_ context.Context, req agentapi.SiteCreateRequest) (agentapi.SiteInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.createErr != nil {
		return agentapi.SiteInfo{}, f.createErr
	}

	if f.accounts[req.User] {
		return agentapi.SiteInfo{}, &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "exists"}
	}

	f.accounts[req.User] = true

	return agentapi.SiteInfo{User: req.User, UID: 1001, GID: 1001, Pool: req.Pool}, nil
}

func (f *fakeAgent) SiteDelete(_ context.Context, req agentapi.SiteDeleteRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.deleteErr != nil {
		return f.deleteErr
	}

	if !f.accounts[req.User] {
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site"}
	}

	for _, s := range f.web.Sites {
		if s.User == req.User {
			return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "site still has a virtual host"}
		}
	}

	delete(f.accounts, req.User)

	return nil
}

func (f *fakeAgent) WebApply(_ context.Context, req agentapi.WebApplyRequest) (agentapi.WebApplyResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err := req.Validate(); err != nil {
		return agentapi.WebApplyResponse{}, &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: err.Error()}
	}

	for _, s := range req.Sites {
		if !f.accounts[s.User] {
			return agentapi.WebApplyResponse{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site"}
		}

		if m := s.Maintenance; m != nil && f.pages[m.Page+"."+m.Digest] == nil {
			return agentapi.WebApplyResponse{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "page not stored"}
		}
	}

	if f.webErr != nil {
		return agentapi.WebApplyResponse{}, f.webErr
	}

	f.applies++
	f.web = req

	return agentapi.WebApplyResponse{Generation: f.applies, Changed: true}, nil
}

// TLSCSR returns a fake request that names the domains.
func (f *fakeAgent) TLSCSR(_ context.Context, user string, domains []string) (string, error) {
	return "csr " + user + " " + strings.Join(domains, ","), nil
}

// TLSInstall accepts a fake chain from fakeIssuer.
func (f *fakeAgent) TLSInstall(_ context.Context, user, chain string) (agentapi.TLSCertInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.tlsErr != nil {
		return agentapi.TLSCertInfo{}, f.tlsErr
	}

	parts := strings.Fields(chain)
	if len(parts) != 4 || parts[0] != "cert" || parts[1] != user {
		return agentapi.TLSCertInfo{}, &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "bad chain"}
	}

	f.installed++

	return agentapi.TLSCertInfo{Domains: strings.Split(parts[2], ","), NotAfter: time.Unix(0, 0).Add(parseDays(parts[3]))}, nil
}

func parseDays(s string) time.Duration {
	n, _ := strconv.Atoi(s)

	return time.Duration(n) * 24 * time.Hour
}

func (f *fakeAgent) PHPVersions(context.Context) ([]string, error) {
	return []string{"8.3", "8.4"}, nil
}

// live returns "user=domain,domain" for every site in the live web state.
func (f *fakeAgent) live() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string

	for _, s := range f.web.Sites {
		entry := fmt.Sprintf("%s=%v", s.User, s.Domains)
		if s.PHP != "" {
			entry += "+php" + s.PHP
		}

		if s.TLS {
			entry += "+tls"
		}

		if s.Disabled {
			entry += "+disabled"
		}

		if m := s.Maintenance; m != nil {
			entry += fmt.Sprintf("+maint(%s%v)", m.Page, m.AllowIPs)
		}

		out = append(out, entry)
	}

	sort.Strings(out)

	return fmt.Sprint(out)
}

func newService(t *testing.T) (*Service, *fakeAgent, *store.Store) {
	t.Helper()

	st, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = st.Close() })

	u, err := st.CreateFirstUser(t.Context(), "kim", "hash")
	if err != nil || u.ID != 1 {
		t.Fatal(err)
	}

	ag := newFake()

	return New(st, ag, nil), ag, st
}

var errDown = errors.New("agent unavailable")

func TestCreateChangeDelete(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	site, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{" Blog.Example.com ", "www.blog.example.com"}})
	if err != nil {
		t.Fatal(err)
	}

	if site.State != store.SiteActive || site.Pool != "default" || fmt.Sprint(site.Domains) != "[blog.example.com www.blog.example.com]" {
		t.Fatalf("site %+v", site)
	}

	if got := ag.live(); got != "[blog=[blog.example.com www.blog.example.com]]" {
		t.Fatalf("live %s", got)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"x.example.com"}}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"www.blog.example.com"}}); !errors.Is(err, ErrDomainTaken) {
		t.Fatalf("taken domain: %v", err)
	}

	if ag.accounts["shop"] {
		t.Fatal("account created for a rejected site")
	}

	if _, err := s.SetDomains(ctx, 1, "blog", []string{"blog.example.org"}); err != nil {
		t.Fatal(err)
	}

	if got := ag.live(); got != "[blog=[blog.example.org]]" {
		t.Fatalf("live %s", got)
	}

	if err := s.Delete(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	if ag.accounts["blog"] || ag.live() != "[]" {
		t.Fatalf("after delete: accounts %v live %s", ag.accounts, ag.live())
	}

	if _, err := s.Get(ctx, "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}

	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE target = ?`, "blog", &n); err != nil || n != 3 {
		t.Fatalf("audit entries %d %v", n, err)
	}
}

func TestCreateRejectsBadInputWithoutCallingTheAgent(t *testing.T) {
	s, ag, _ := newService(t)

	for name, req := range map[string]CreateRequest{
		"bad name":       {Name: "../etc", Domains: []string{"a.example.com"}},
		"reserved name":  {Name: "root", Domains: []string{"a.example.com"}},
		"bad pool":       {Name: "blog", Pool: "/etc", Domains: []string{"a.example.com"}},
		"no domain":      {Name: "blog"},
		"bad domain":     {Name: "blog", Domains: []string{"a.example.com;"}},
		"twice":          {Name: "blog", Domains: []string{"a.example.com", "A.example.com"}},
		"control domain": {Name: "blog", Domains: []string{"_hostashi_control"}},
		"bad php":        {Name: "blog", PHP: "8.0", Domains: []string{"a.example.com"}},
	} {
		if _, err := s.Create(t.Context(), 1, req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}

	if len(ag.accounts) != 0 || ag.applies != 0 {
		t.Fatal("the agent was called")
	}
}

func TestCreateFailuresLeaveNothing(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	ag.createErr = errDown
	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); !errors.Is(err, errDown) {
		t.Fatalf("err %v", err)
	}

	if _, err := s.Get(ctx, "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row stayed: %v", err)
	}

	// nginx refuses: the account is removed again.
	ag.createErr = nil
	ag.webErr = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "nginx rejected the configuration"}

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); !isCode(err, agentrpc.CodePrecondition) {
		t.Fatalf("err %v", err)
	}

	if ag.accounts["blog"] {
		t.Fatal("account stayed")
	}

	if _, err := s.Get(ctx, "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row stayed: %v", err)
	}

	// Afterwards the same site can be created.
	ag.webErr = nil

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}
}

func TestFailedDomainChangeKeepsTheOldDomains(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	ag.webErr = errDown

	if _, err := s.SetDomains(ctx, 1, "blog", []string{"new.example.com"}); !errors.Is(err, errDown) {
		t.Fatalf("err %v", err)
	}

	site, err := s.Get(ctx, "blog")
	if err != nil || fmt.Sprint(site.Domains) != "[blog.example.com]" {
		t.Fatalf("site %+v %v", site, err)
	}

	if _, err := s.SetDomains(ctx, 1, "nope", []string{"a.example.com"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}
}

func TestDeleteCanBeRepeatedAfterAFailure(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	// The web server change fails: the site stays active and served.
	ag.webErr = errDown
	if err := s.Delete(ctx, 1, "blog"); !errors.Is(err, errDown) {
		t.Fatalf("err %v", err)
	}

	if site, _ := s.Get(ctx, "blog"); site.State != store.SiteActive {
		t.Fatalf("state %s", site.State)
	}

	// The account cannot be removed (processes running): the virtual host
	// is gone, the site waits in state deleting.
	ag.webErr = nil
	ag.deleteErr = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "site user still has running processes"}

	if err := s.Delete(ctx, 1, "blog"); !isCode(err, agentrpc.CodePrecondition) {
		t.Fatalf("err %v", err)
	}

	if site, _ := s.Get(ctx, "blog"); site.State != store.SiteDeleting || ag.live() != "[]" {
		t.Fatalf("state %s live %s", site.State, ag.live())
	}

	// A domain change is refused meanwhile, the retry finishes the job.
	if _, err := s.SetDomains(ctx, 1, "blog", []string{"a.example.com"}); !errors.Is(err, ErrState) {
		t.Fatalf("set domains while deleting: %v", err)
	}

	ag.deleteErr = nil

	if err := s.Delete(ctx, 1, "blog"); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Get(ctx, "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("row stayed: %v", err)
	}
}

func TestPHPVersionChange(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	site, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}})
	if err != nil || site.PHP != "8.4" {
		t.Fatalf("%+v %v", site, err)
	}

	if got := ag.live(); got != "[blog=[blog.example.com]+php8.4]" {
		t.Fatalf("live %s", got)
	}

	if site, err = s.SetPHP(ctx, 1, "blog", "8.3"); err != nil || site.PHP != "8.3" || ag.live() != "[blog=[blog.example.com]+php8.3]" {
		t.Fatalf("%+v %v live %s", site, err, ag.live())
	}

	// The agent refuses: the stored version stays the previous one.
	ag.webErr = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the requested PHP version is not installed"}

	if _, err := s.SetPHP(ctx, 1, "blog", "8.5"); !isCode(err, agentrpc.CodePrecondition) {
		t.Fatalf("err %v", err)
	}

	if got, _ := s.Get(ctx, "blog"); got.PHP != "8.3" {
		t.Fatalf("stored %q", got.PHP)
	}

	ag.webErr = nil

	// "" turns PHP off.
	if _, err := s.SetPHP(ctx, 1, "blog", ""); err != nil || ag.live() != "[blog=[blog.example.com]]" {
		t.Fatalf("%v live %s", err, ag.live())
	}

	for _, v := range []string{"8.0", "8", "../8.4"} {
		if _, err := s.SetPHP(ctx, 1, "blog", v); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", v, err)
		}
	}

	if _, err := s.SetPHP(ctx, 1, "nope", "8.4"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	if v, err := s.PHPVersions(ctx); err != nil || fmt.Sprint(v) != "[8.3 8.4]" {
		t.Fatalf("versions %v %v", v, err)
	}

	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action = ?`, "site.php", &n); err != nil || n != 2 {
		t.Fatalf("audit entries %d %v", n, err)
	}
}

func TestDisableKeepsTheSiteButStopsPHP(t *testing.T) {
	s, ag, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", PHP: "8.4", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	site, err := s.SetDisabled(ctx, 1, "blog", true)
	if err != nil || !site.Disabled || site.PHP != "8.4" || ag.live() != "[blog=[blog.example.com]+disabled]" {
		t.Fatalf("%+v %v live %s", site, err, ag.live())
	}

	// Other changes keep the site disabled and its PHP off.
	if _, err := s.SetPHP(ctx, 1, "blog", "8.3"); err != nil || ag.live() != "[blog=[blog.example.com]+disabled]" {
		t.Fatalf("%v live %s", err, ag.live())
	}

	// The agent refuses: the site stays disabled.
	ag.webErr = &agentrpc.Error{Code: agentrpc.CodeInternal, Message: "nginx down"}

	if _, err := s.SetDisabled(ctx, 1, "blog", false); err == nil {
		t.Fatal("enable succeeded although the agent refused")
	}

	if got, _ := s.Get(ctx, "blog"); !got.Disabled {
		t.Fatal("failed enable was stored")
	}

	ag.webErr = nil

	if site, err := s.SetDisabled(ctx, 1, "blog", false); err != nil || site.Disabled || ag.live() != "[blog=[blog.example.com]+php8.3]" {
		t.Fatalf("%+v %v live %s", site, err, ag.live())
	}

	if _, err := s.SetDisabled(ctx, 1, "nope", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown site: %v", err)
	}

	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action LIKE ?`, "site.%able", &n); err != nil || n != 2 {
		t.Fatalf("audit entries %d %v", n, err)
	}
}

// fakeIssuer signs fake requests; the certificate is valid until day
// validDays after the Unix epoch.
type fakeIssuer struct {
	mu        sync.Mutex
	calls     int
	err       error
	validDays int
	before    func() // runs before each signature
}

func (i *fakeIssuer) Obtain(_ context.Context, csr []byte) ([]byte, error) {
	i.mu.Lock()
	defer i.mu.Unlock()

	i.calls++

	if i.before != nil {
		i.before()
	}

	if i.err != nil {
		return nil, i.err
	}

	p := strings.Fields(string(csr)) // "csr user domains"

	return []byte(fmt.Sprintf("cert %s %s %d", p[1], p[2], i.validDays)), nil
}

func TestTLSLifecycle(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	day := func(d int) time.Time { return time.Unix(0, 0).Add(time.Duration(d) * 24 * time.Hour) }
	now := day(1000)
	s.now = func() time.Time { return now }

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetTLS(ctx, 1, "blog", true); !errors.Is(err, ErrNoTLS) {
		t.Fatalf("without issuer: %v", err)
	}

	iss := &fakeIssuer{validDays: 1090}
	s.EnableTLS(iss)

	site, err := s.SetTLS(ctx, 1, "blog", true)
	if err != nil || !site.TLS || ag.live() != "[blog=[blog.example.com]]" {
		t.Fatalf("tls on: %+v %v live %s", site, err, ag.live())
	}

	s.RenewDue(ctx)

	if got := ag.live(); got != "[blog=[blog.example.com]+tls]" || iss.calls != 1 {
		t.Fatalf("after issue: live %s, calls %d", got, iss.calls)
	}

	if got, _ := s.Get(ctx, "blog"); !s.TLSActive(got) {
		t.Fatalf("not active after the web server switch: %+v", got)
	}

	// Still valid for 90 days: nothing to do.
	s.RenewDue(ctx)

	if iss.calls != 1 {
		t.Fatalf("renewed a fresh certificate: %d", iss.calls)
	}

	// A new domain: HTTP only until the new certificate is there.
	if _, err := s.SetDomains(ctx, 1, "blog", []string{"blog.example.com", "www.blog.example.com"}); err != nil {
		t.Fatal(err)
	}

	if got := ag.live(); got != "[blog=[blog.example.com www.blog.example.com]]" {
		t.Fatalf("after domain change: %s", got)
	}

	if got, _ := s.Get(ctx, "blog"); s.TLSActive(got) {
		t.Fatalf("active while served over HTTP only: %+v", got)
	}

	s.RenewDue(ctx)

	if got := ag.live(); got != "[blog=[blog.example.com www.blog.example.com]+tls]" || iss.calls != 2 {
		t.Fatalf("after reissue: %s, calls %d", got, iss.calls)
	}

	// 70 days later the renewal fails: the error is kept, the old
	// certificate keeps serving, and the next try waits RetryAfter.
	now = day(1070)
	iss.err = errors.New("acme: error: 400 :: urn:ietf:params:acme:error:dns :: NXDOMAIN")

	s.RenewDue(ctx)

	site, _ = s.Get(ctx, "blog")
	if !strings.Contains(site.TLSError, "NXDOMAIN") || !site.TLSFailedAt.Equal(now) || ag.live() != "[blog=[blog.example.com www.blog.example.com]+tls]" {
		t.Fatalf("after failure: %+v live %s", site, ag.live())
	}

	s.RenewDue(ctx)

	if iss.calls != 3 {
		t.Fatalf("retried within RetryAfter: %d calls", iss.calls)
	}

	now = now.Add(RetryAfter + time.Minute)
	iss.err = nil
	iss.validDays = 1160

	s.RenewDue(ctx)

	site, _ = s.Get(ctx, "blog")
	if iss.calls != 4 || site.TLSError != "" || !site.TLSNotAfter.Equal(day(1160)) {
		t.Fatalf("after retry: calls %d site %+v", iss.calls, site)
	}

	// Expired without renewal: back to HTTP.
	now = day(1200)
	iss.err = errors.New("down")

	s.RenewDue(ctx)

	if err := s.Sync(ctx); err != nil || ag.live() != "[blog=[blog.example.com www.blog.example.com]]" {
		t.Fatalf("expired: %v live %s", err, ag.live())
	}

	// Off: no more attempts.
	if site, err := s.SetTLS(ctx, 1, "blog", false); err != nil || site.TLS {
		t.Fatalf("off: %+v %v", site, err)
	}

	calls := iss.calls
	now = now.Add(2 * RetryAfter)

	s.RenewDue(ctx)

	if iss.calls != calls {
		t.Fatal("certificate requested for a site without TLS")
	}
}

// The API must not report HTTPS before nginx serves it: the certificate is
// stored first, the web server switch comes after.
func TestTLSNotActiveWhenSwitchFails(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	// The fake certificate counts its days from 1970.
	s.now = func() time.Time { return time.Unix(0, 0).Add(1000 * 24 * time.Hour) }
	s.EnableTLS(&fakeIssuer{validDays: 1090})

	if _, err := s.SetTLS(ctx, 1, "blog", true); err != nil {
		t.Fatal(err)
	}

	ag.mu.Lock()
	ag.webErr = errors.New("agent down")
	ag.mu.Unlock()

	s.RenewDue(ctx)

	got, err := s.Get(ctx, "blog")
	if err != nil || got.TLSNotAfter.IsZero() || s.TLSActive(got) {
		t.Fatalf("certificate stored %v, active %v: %v", got.TLSNotAfter, s.TLSActive(got), err)
	}

	ag.mu.Lock()
	ag.webErr = nil
	ag.mu.Unlock()

	if err := s.Sync(ctx); err != nil {
		t.Fatal(err)
	}

	if got, _ := s.Get(ctx, "blog"); !s.TLSActive(got) {
		t.Fatal("not active after the switch went through")
	}
}

func TestTLSInstallFailureIsRecorded(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	s.now = func() time.Time { return time.Unix(0, 0).Add(1000 * 24 * time.Hour) }
	s.EnableTLS(&fakeIssuer{validDays: 1090})

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.SetTLS(ctx, 1, "blog", true); err != nil {
		t.Fatal(err)
	}

	ag.tlsErr = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the certificate does not belong to the pending key of this site"}

	s.RenewDue(ctx)

	site, _ := s.Get(ctx, "blog")
	if !strings.Contains(site.TLSError, "pending key") || len(site.TLSDomains) != 0 || ag.live() != "[blog=[blog.example.com]]" {
		t.Fatalf("%+v live %s", site, ag.live())
	}
}

func TestCreateWithTLS(t *testing.T) {
	s, ag, _ := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}, TLS: true}); !errors.Is(err, ErrNoTLS) {
		t.Fatalf("without issuer: %v", err)
	}

	if _, err := s.Get(ctx, "blog"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("refused create left a row: %v", err)
	}

	s.now = func() time.Time { return time.Unix(0, 0).Add(1000 * 24 * time.Hour) }
	s.EnableTLS(&fakeIssuer{validDays: 1090})

	site, err := s.Create(ctx, 1, CreateRequest{Name: "blog", Domains: []string{"blog.example.com"}, TLS: true, HSTS: true})
	if err != nil || !site.TLS || !site.HSTS || s.TLSActive(site) || site.State != store.SiteActive {
		t.Fatalf("create: %+v %v", site, err)
	}

	s.RenewDue(ctx)

	if got := ag.live(); got != "[blog=[blog.example.com]+tls]" {
		t.Fatalf("after issue: %s", got)
	}

	ag.mu.Lock()
	hsts := ag.web.Sites[0].HSTS
	ag.mu.Unlock()

	if !hsts {
		t.Fatal("HSTS not sent")
	}
}

func (f *fakeAgent) DBCreate(_ context.Context, site, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.dbErr != nil {
		return f.dbErr
	}

	if !f.accounts[site] {
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site"}
	}

	if _, ok := f.dbs[name]; ok {
		return &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "exists"}
	}

	f.dbs[name] = site

	return nil
}

func (f *fakeAgent) DBDelete(_ context.Context, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.dbErr != nil {
		return f.dbErr
	}

	if _, ok := f.dbs[name]; !ok {
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown database"}
	}

	for u, d := range f.dbUsers {
		if d == name {
			delete(f.dbUsers, u)
		}
	}

	delete(f.dbs, name)

	return nil
}

func (f *fakeAgent) DBUserCreate(_ context.Context, database, user, password string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.dbErr != nil {
		return f.dbErr
	}

	if f.userErr != nil {
		return f.userErr
	}

	if agentapi.ValidateDBPassword(password) != nil {
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "bad password"}
	}

	if _, ok := f.dbs[database]; !ok {
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown database"}
	}

	f.dbUsers[user] = database

	return nil
}

func (f *fakeAgent) DBUserDelete(_ context.Context, user string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.dbErr != nil {
		return f.dbErr
	}

	if _, ok := f.dbUsers[user]; !ok {
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown user"}
	}

	delete(f.dbUsers, user)

	return nil
}

func (f *fakeAgent) DBUserPassword(_ context.Context, user, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.dbErr != nil {
		return f.dbErr
	}

	if _, ok := f.dbUsers[user]; !ok {
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown user"}
	}

	return nil
}

func (f *fakeAgent) DBImport(_ context.Context, database, path string) (agentapi.DBJob, error) {
	return f.startJob("import", database, path)
}

func (f *fakeAgent) DBExport(_ context.Context, database, path string) (agentapi.DBJob, error) {
	return f.startJob("export", database, path)
}

func (f *fakeAgent) startJob(kind, database, path string) (agentapi.DBJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.dbErr != nil {
		return agentapi.DBJob{}, f.dbErr
	}

	if _, ok := f.dbs[database]; !ok {
		return agentapi.DBJob{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown database"}
	}

	j := agentapi.DBJob{ID: fmt.Sprintf("%016x", len(f.jobs)+1), Kind: kind, Database: database, Path: path, State: agentapi.JobRunning}
	f.jobs[j.ID] = j

	return j, nil
}

func (f *fakeAgent) DBJob(_ context.Context, id string) (agentapi.DBJob, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	j, ok := f.jobs[id]
	if !ok {
		return agentapi.DBJob{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown job"}
	}

	return j, nil
}

func (f *fakeAgent) PageList(context.Context) ([]agentapi.PageVersion, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.pageErr != nil {
		return nil, f.pageErr
	}

	out := []agentapi.PageVersion{}

	for k := range f.pages {
		id, digest, _ := strings.Cut(k, ".")
		out = append(out, agentapi.PageVersion{ID: id, Digest: digest})
	}

	return out, nil
}

func (f *fakeAgent) PageUpload(_ context.Context, id string, files map[string][]byte, digest string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.pageErr != nil {
		return f.pageErr
	}

	if agentapi.PageDigest(files) != digest {
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "digest"}
	}

	f.uploads++
	f.pages[id+"."+digest] = files

	return nil
}

func (f *fakeAgent) PageDelete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.pageGones = append(f.pageGones, id)

	return nil
}
