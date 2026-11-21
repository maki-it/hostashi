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

// remoteToolSetup is remoteSetup with the tool ready and a fixed clock.
func remoteToolSetup(t *testing.T) (*Service, *fakeDBTool, *fakeDBServers, *fakeResolver, *time.Time) {
	t.Helper()

	s, _, f, r, _ := remoteSetup(t)
	ctx := t.Context()

	tool := &fakeDBTool{}
	if err := s.EnableDBTool(tool, 8444); err != nil {
		t.Fatal(err)
	}

	if err := s.EnablePanel("panel.example.com"); err != nil {
		t.Fatal(err)
	}

	if err := s.savePanelTLS(ctx, PanelTLS{Domains: []string{"panel.example.com"}, NotAfter: time.Now().Add(30 * 24 * time.Hour), Live: true}); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return now }

	return s, tool, f, r, &now
}

func toolUsers(f *fakeDBServers) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string

	for u := range f.users {
		if strings.HasPrefix(u, agentapi.DBToolUserPrefix) {
			out = append(out, u)
		}
	}

	return out
}

func TestRemoteDatabaseTool(t *testing.T) {
	s, tool, f, r, now := remoteToolSetup(t)
	ctx := t.Context()

	var scheduled []time.Duration

	s.afterFunc = func(d time.Duration, _ func()) { scheduled = append(scheduled, d) }

	sess, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30)
	if err != nil {
		t.Fatal(err)
	}

	if sess.URL != "https://panel.example.com:8444/?ticket=fedcba9876543210fedcba9876543210" {
		t.Errorf("url %s", sess.URL)
	}

	// The tool got the server list first, sorted addresses of its family.
	want := agentapi.DBToolServersRequest{Servers: []agentapi.DBToolServer{{ID: 2, Host: "db.example.net", Addrs: []string{"10.0.0.5", "10.0.0.6"}}}}
	if len(tool.servers) != 1 || !reflect.DeepEqual(tool.servers[0], want) {
		t.Fatalf("servers %+v", tool.servers)
	}

	if len(tool.remote) != 1 {
		t.Fatalf("remote %+v", tool.remote)
	}

	req := tool.remote[0]
	if req.Database != "shop_wp" || req.Minutes != 30 || req.ServerID != 2 || req.Host != "db.example.net" || req.Port != 3306 || req.TLS != "verify" {
		t.Errorf("request %+v", req)
	}

	users := toolUsers(f)
	if len(users) != 1 || users[0] != req.User || f.users[req.User] != "shop_wp" {
		t.Fatalf("tool users %v", users)
	}

	if rows, _ := s.st.DBRemoteTools(ctx); len(rows) != 1 || rows[0].User != req.User || !rows[0].Expires.Equal(now.Add(31*time.Minute)) {
		t.Fatalf("rows %+v", rows)
	}

	if len(scheduled) != 1 || scheduled[0] != 31*time.Minute {
		t.Errorf("scheduled %v", scheduled)
	}

	// Before the end the user stays; after it, it goes with its row.
	s.FinishDBRemoteTools(ctx)

	if len(toolUsers(f)) != 1 {
		t.Fatal("user dropped early")
	}

	*now = now.Add(31 * time.Minute)

	s.FinishDBRemoteTools(ctx)

	if len(toolUsers(f)) != 0 {
		t.Fatalf("user kept: %v", toolUsers(f))
	}

	if rows, _ := s.st.DBRemoteTools(ctx); len(rows) != 0 {
		t.Fatalf("rows kept %+v", rows)
	}

	// A name without address fails, and nothing is created.
	r.err = errors.New("no such host")

	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); !errors.Is(err, ErrDBServerResolve) {
		t.Fatalf("unresolved: %v", err)
	}

	if len(tool.remote) != 1 || len(toolUsers(f)) != 0 {
		t.Fatalf("remote %d, users %v", len(tool.remote), toolUsers(f))
	}

	// The agent refusing drops the user again.
	r.err = nil
	tool.remoteErr = errDown

	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); !errors.Is(err, errDown) {
		t.Fatalf("agent error: %v", err)
	}

	if len(toolUsers(f)) != 0 {
		t.Fatalf("user kept after agent error: %v", toolUsers(f))
	}

	if rows, _ := s.st.DBRemoteTools(ctx); len(rows) != 0 {
		t.Fatalf("rows kept after agent error %+v", rows)
	}
}

// hostResolver fails for the names in down.
type hostResolver struct {
	fakeResolver

	down map[string]bool
}

func (r *hostResolver) LookupNetIP(ctx context.Context, network, host string) ([]netip.Addr, error) {
	if r.down[host] {
		return nil, errors.New("no such host")
	}

	return r.fakeResolver.LookupNetIP(ctx, network, host)
}

func TestRemoteDatabaseDeleteEndsTools(t *testing.T) {
	s, _, f, _, _ := remoteToolSetup(t)
	ctx := t.Context()

	s.afterFunc = func(time.Duration, func()) {}

	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 120); err != nil {
		t.Fatal(err)
	}

	if len(toolUsers(f)) != 1 {
		t.Fatalf("users %v", toolUsers(f))
	}

	if err := s.DeleteDatabase(ctx, 1, "shop_wp"); err != nil {
		t.Fatal(err)
	}

	if len(toolUsers(f)) != 0 {
		t.Fatalf("tool user kept: %v", toolUsers(f))
	}

	if rows, _ := s.st.DBRemoteTools(ctx); len(rows) != 0 {
		t.Fatalf("rows %+v", rows)
	}
}

func TestToolServersKeepOthers(t *testing.T) {
	s, tool, _, _, _ := remoteToolSetup(t)
	ctx := t.Context()

	s.afterFunc = func(time.Duration, func()) {}

	r := &hostResolver{fakeResolver: fakeResolver{addrs: addrs("10.0.0.9")}, down: map[string]bool{}}
	s.SetResolver(r)

	other, _, err := s.AddDBServer(ctx, 1, NewDBServer{Name: "db2", Host: "db2.example.net", AdminUser: "hostashi_admin", AdminPassword: "Admin-pw-1"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); err != nil {
		t.Fatal(err)
	}

	first := tool.servers[len(tool.servers)-1]
	if len(first.Servers) != 2 {
		t.Fatalf("servers %+v", first)
	}

	// db2 no longer resolves: it keeps its last addresses, so the list
	// stays the same and the pool keeps its sessions.
	r.down["db2.example.net"] = true

	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(tool.servers[len(tool.servers)-1], first) {
		t.Errorf("list changed: %+v", tool.servers[len(tool.servers)-1])
	}

	// Without addresses sent before, a server that does not resolve is left
	// out; the one a session needs is an error.
	s.toolServers = nil

	if err := s.syncToolServers(ctx, 0); err != nil {
		t.Fatal(err)
	}

	if last := tool.servers[len(tool.servers)-1]; len(last.Servers) != 1 || last.Servers[0].Host != "db.example.net" {
		t.Errorf("unresolved server kept: %+v", last)
	}

	if err := s.syncToolServers(ctx, other.ID); !errors.Is(err, ErrDBServerResolve) {
		t.Errorf("needed server: %v", err)
	}

	// Deleting a server takes it off the list.
	delete(r.down, "db2.example.net")

	if err := s.syncToolServers(ctx, 0); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteDBServer(ctx, 1, other.Name); err != nil {
		t.Fatal(err)
	}

	if last := tool.servers[len(tool.servers)-1]; len(last.Servers) != 1 || last.Servers[0].ID == other.ID {
		t.Fatalf("after delete %+v", last)
	}
}

func TestRemoteToolCA(t *testing.T) {
	s, _, _, _, _ := remoteToolSetup(t)

	entry, err := s.toolServer(t.Context(), storeServer("verify-ca", "-----BEGIN CERTIFICATE-----\nx\n"))
	if err != nil || entry.CAPEM == "" {
		t.Fatalf("verify-ca %+v %v", entry, err)
	}

	entry, err = s.toolServer(t.Context(), storeServer("none", ""))
	if err != nil || entry.CAPEM != "" {
		t.Fatalf("none %+v %v", entry, err)
	}
}

func storeServer(mode, ca string) store.DBServer {
	return store.DBServer{ID: 7, Name: "db7", Host: "192.0.2.7", Port: 3306, TLSMode: mode, CAPEM: ca}
}
