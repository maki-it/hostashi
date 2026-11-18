// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"testing"

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

	createErr, deleteErr, webErr error
}

func newFake() *fakeAgent { return &fakeAgent{accounts: map[string]bool{}} }

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
	}

	if f.webErr != nil {
		return agentapi.WebApplyResponse{}, f.webErr
	}

	f.applies++
	f.web = req

	return agentapi.WebApplyResponse{Generation: f.applies, Changed: true}, nil
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
