// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

type fakeResolver struct {
	addrs []netip.Addr
	err   error
	asked []string
}

func (r *fakeResolver) LookupNetIP(_ context.Context, network, host string) ([]netip.Addr, error) {
	r.asked = append(r.asked, network+" "+host)

	return r.addrs, r.err
}

func addrs(ss ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(ss))
	for _, s := range ss {
		out = append(out, netip.MustParseAddr(s))
	}

	return out
}

// remoteSetup gives a site shop with database shop_wp on the external
// server db1 (client host 10.0.0.2, so IPv4 only).
func remoteSetup(t *testing.T) (*Service, *fakeAgent, *fakeDBServers, *fakeResolver, *store.Store) {
	t.Helper()

	s, ag, st := newService(t)
	f := newFakeDBServers()
	s.EnableDBServers(f, "")

	r := &fakeResolver{addrs: addrs("2001:db8::5", "10.0.0.5", "127.0.0.1", "10.0.0.5", "::ffff:10.0.0.6")}
	s.SetResolver(r)

	ctx := t.Context()
	addServer(t, s, "db1")

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_wp", Server: "db1"}); err != nil {
		t.Fatal(err)
	}

	return s, ag, f, r, st
}

func jobUsers(f *fakeDBServers) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string

	for u := range f.users {
		if strings.HasPrefix(u, "hostashi_job_") {
			out = append(out, u)
		}
	}

	return out
}

func TestRemoteExport(t *testing.T) {
	s, ag, f, r, st := remoteSetup(t)
	ctx := t.Context()

	j, err := s.ExportDatabase(ctx, 1, "shop_wp", "backup.sql.gz")
	if err != nil || j.Kind != "export" || j.State != agentapi.JobRunning {
		t.Fatalf("export %+v %v", j, err)
	}

	if len(ag.remote) != 1 {
		t.Fatalf("agent requests: %+v", ag.remote)
	}

	req := ag.remote[0]
	if req.Site != "shop" || req.Database != "shop_wp" || req.Host != "db.example.net" || req.Port != 3306 || req.TLS != "verify" ||
		!reflect.DeepEqual(req.Addrs, []string{"10.0.0.5", "10.0.0.6"}) {
		t.Errorf("request %+v", req)
	}

	if !reflect.DeepEqual(r.asked, []string{"ip db.example.net"}) {
		t.Errorf("resolver asked %v", r.asked)
	}

	// The temporary user exists on the server with rights on the database.
	users := jobUsers(f)
	if len(users) != 1 || users[0] != req.User || f.users[req.User] != "shop_wp" {
		t.Fatalf("job users %v (%v)", users, f.users)
	}

	if rows, _ := st.DBRemoteJobs(ctx); len(rows) != 1 || rows[0].JobID != j.ID || rows[0].User != req.User {
		t.Fatalf("rows %+v", rows)
	}

	// Running: the user stays.
	if got, err := s.DatabaseJob(ctx, "shop_wp", j.ID); err != nil || got.State != agentapi.JobRunning || len(jobUsers(f)) != 1 {
		t.Fatalf("running job %+v %v, users %v", got, err, jobUsers(f))
	}

	// Done: the next look drops the user and forgets the row.
	ag.mu.Lock()
	done := ag.jobs[j.ID]
	done.State = agentapi.JobDone
	ag.jobs[j.ID] = done
	ag.mu.Unlock()

	if got, err := s.DatabaseJob(ctx, "shop_wp", j.ID); err != nil || got.State != agentapi.JobDone {
		t.Fatalf("done job %+v %v", got, err)
	}

	if u := jobUsers(f); len(u) != 0 {
		t.Errorf("user kept: %v", u)
	}

	if rows, _ := st.DBRemoteJobs(ctx); len(rows) != 0 {
		t.Errorf("rows kept: %+v", rows)
	}
}

func TestRemoteImportCleanup(t *testing.T) {
	s, ag, f, _, st := remoteSetup(t)
	ctx := t.Context()

	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	j, err := s.ImportDatabase(ctx, 1, "shop_wp", "dumps/old.sql")
	if err != nil || j.Kind != "import" {
		t.Fatalf("import %+v %v", j, err)
	}

	// The server check drops nothing while the job runs.
	s.FinishDBRemoteJobs(ctx)

	if len(jobUsers(f)) != 1 {
		t.Fatal("user dropped while the job runs")
	}

	// A user the server cannot drop stays recorded.
	f.opErr = errors.New("server gone")
	now = now.Add(remoteJobLimit + time.Minute)

	s.FinishDBRemoteJobs(ctx)

	if rows, _ := st.DBRemoteJobs(ctx); len(rows) != 1 {
		t.Fatalf("rows %+v", rows)
	}

	// Past the unit's limit the user goes even if the agent still says
	// running (it never wrote its end).
	f.opErr = nil

	s.FinishDBRemoteJobs(ctx)

	if len(jobUsers(f)) != 0 {
		t.Errorf("user kept: %v", jobUsers(f))
	}

	if rows, _ := st.DBRemoteJobs(ctx); len(rows) != 0 {
		t.Errorf("rows kept: %+v", rows)
	}

	_ = ag
}

func TestRemoteDumpRefused(t *testing.T) {
	s, ag, f, r, st := remoteSetup(t)
	ctx := t.Context()

	// No usable address: nothing is created.
	r.addrs = addrs("127.0.0.1", "fe80::1", "2001:db8::5")

	if _, err := s.ExportDatabase(ctx, 1, "shop_wp", "x.sql"); !errors.Is(err, ErrDBServerResolve) {
		t.Fatalf("resolve: %v", err)
	}

	r.addrs, r.err = nil, errors.New("no such host")

	if _, err := s.ExportDatabase(ctx, 1, "shop_wp", "x.sql"); !errors.Is(err, ErrDBServerResolve) {
		t.Fatalf("lookup: %v", err)
	}

	// The agent refuses: the user is dropped again.
	r.addrs, r.err = addrs("10.0.0.5"), nil
	ag.dbErr = errDown

	if _, err := s.ExportDatabase(ctx, 1, "shop_wp", "x.sql"); !errors.Is(err, errDown) {
		t.Fatalf("agent: %v", err)
	}

	ag.dbErr = nil

	if len(jobUsers(f)) != 0 || len(ag.remote) != 0 {
		t.Errorf("users %v, requests %+v", jobUsers(f), ag.remote)
	}

	if rows, _ := st.DBRemoteJobs(ctx); len(rows) != 0 {
		t.Errorf("rows %+v", rows)
	}

	// A job of another database is not found.
	j, err := s.ExportDatabase(ctx, 1, "shop_wp", "x.sql")
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_two", Server: "db1"}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.DatabaseJob(ctx, "shop_two", j.ID); !errors.Is(err, ErrJobNotFound) {
		t.Errorf("other database: %v", err)
	}
}

func TestServerAddrsByIP(t *testing.T) {
	s, _, _, r, _ := remoteSetup(t)

	got, err := s.serverAddrs(t.Context(), store.DBServer{Host: "2001:db8::7"})
	if err != nil || !reflect.DeepEqual(got, []string{"2001:db8::7"}) || len(r.asked) != 0 {
		t.Fatalf("addrs %v %v, asked %v", got, err, r.asked)
	}

	// An IPv6 client host keeps only IPv6 addresses; an unknown one keeps all.
	got, _ = s.serverAddrs(t.Context(), store.DBServer{Host: "db.example.net", Status: store.DBServerStatus{ClientHost: "2001:db8::2"}})
	if !reflect.DeepEqual(got, []string{"2001:db8::5"}) {
		t.Errorf("IPv6 client: %v", got)
	}

	got, _ = s.serverAddrs(t.Context(), store.DBServer{Host: "db.example.net", Status: store.DBServerStatus{ClientHost: "web2.example.net"}})
	if !reflect.DeepEqual(got, []string{"2001:db8::5", "10.0.0.5", "10.0.0.6"}) {
		t.Errorf("named client: %v", got)
	}
}

func TestRandomAlnum(t *testing.T) {
	a, b := randomAlnum(32), randomAlnum(32)
	if a == b || agentapi.ValidateRemoteJobUser("hostashi_job_"+randomHex(8), a) != nil {
		t.Fatalf("%q %q", a, b)
	}
}
