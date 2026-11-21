// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

type fakeDBTool struct {
	calls []agentapi.DBToolOpenRequest
	res   agentapi.DBToolOpenResponse
	err   error

	servers    []agentapi.DBToolServersRequest
	serversErr error
	remote     []agentapi.DBToolRemoteRequest
	remoteErr  error
}

func (f *fakeDBTool) DBToolServers(_ context.Context, req agentapi.DBToolServersRequest) (agentapi.DBToolServersResponse, error) {
	if f.serversErr != nil {
		return agentapi.DBToolServersResponse{}, f.serversErr
	}

	restarted := len(f.servers) == 0 || !reflect.DeepEqual(f.servers[len(f.servers)-1], req)
	f.servers = append(f.servers, req)

	return agentapi.DBToolServersResponse{Restarted: restarted}, nil
}

func (f *fakeDBTool) DBToolRemote(_ context.Context, req agentapi.DBToolRemoteRequest) (agentapi.DBToolOpenResponse, error) {
	f.remote = append(f.remote, req)
	if f.remoteErr != nil {
		return agentapi.DBToolOpenResponse{}, f.remoteErr
	}

	return agentapi.DBToolOpenResponse{Ticket: "fedcba9876543210fedcba9876543210", User: req.User, Expires: time.Now().Add(time.Duration(req.Minutes) * time.Minute)}, nil
}

func (f *fakeDBTool) DBToolOpen(_ context.Context, database string, minutes int) (agentapi.DBToolOpenResponse, error) {
	f.calls = append(f.calls, agentapi.DBToolOpenRequest{Database: database, Minutes: minutes})

	return f.res, f.err
}

func TestOpenDatabaseTool(t *testing.T) {
	s, _, st := newService(t)
	ctx := t.Context()

	if _, err := s.Create(ctx, 1, CreateRequest{Name: "shop", Domains: []string{"shop.example.com"}}); err != nil {
		t.Fatal(err)
	}

	if _, _, err := s.CreateDatabase(ctx, 1, NewDatabase{Site: "shop", Name: "shop_wp"}); err != nil {
		t.Fatal(err)
	}

	exp := time.Date(2026, 11, 21, 2, 0, 0, 0, time.UTC)
	tool := &fakeDBTool{res: agentapi.DBToolOpenResponse{Ticket: "0123456789abcdef0123456789abcdef", User: "hostashi_tool_0123456789abcdef", Expires: exp}}

	// Off: no tool configured.
	if st, err := s.DBTool(ctx); err != nil || st.Available || len(st.Minutes) != 2 {
		t.Fatalf("status %+v %v", st, err)
	}

	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); !errors.Is(err, ErrNoDBTool) {
		t.Fatalf("no tool: %v", err)
	}

	if err := s.EnableDBTool(tool, 0); err == nil {
		t.Fatal("port 0 accepted")
	}

	if err := s.EnableDBTool(tool, 8444); err != nil {
		t.Fatal(err)
	}

	// Configured, but without a panel address and certificate.
	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); !errors.Is(err, ErrNoDBTool) {
		t.Fatalf("no panel: %v", err)
	}

	if err := s.EnablePanel("panel.example.com"); err != nil {
		t.Fatal(err)
	}

	tls := PanelTLS{Domains: []string{"panel.example.com"}, NotAfter: time.Now().Add(30 * 24 * time.Hour)}
	if err := s.savePanelTLS(ctx, tls); err != nil {
		t.Fatal(err)
	}

	// A certificate nginx does not serve yet is not enough.
	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); !errors.Is(err, ErrNoDBTool) {
		t.Fatalf("certificate not live: %v", err)
	}

	tls.Live = true
	if err := s.savePanelTLS(ctx, tls); err != nil {
		t.Fatal(err)
	}

	if st, err := s.DBTool(ctx); err != nil || !st.Available {
		t.Fatalf("status %+v %v", st, err)
	}

	for _, c := range []struct {
		db      string
		minutes int
		want    error
	}{
		{"shop_wp", 31, ErrInvalid},
		{"mysql", 30, ErrInvalid},
		{"other", 30, ErrDatabaseNotFound},
	} {
		if _, err := s.OpenDatabaseTool(ctx, 1, c.db, c.minutes); !errors.Is(err, c.want) {
			t.Errorf("%s %d: %v", c.db, c.minutes, err)
		}
	}

	if len(tool.calls) != 0 {
		t.Fatalf("agent called: %+v", tool.calls)
	}

	sess, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 120)
	if err != nil {
		t.Fatal(err)
	}

	if sess.URL != "https://panel.example.com:8444/?ticket=0123456789abcdef0123456789abcdef" || !sess.Expires.Equal(exp) {
		t.Fatalf("session %+v", sess)
	}

	if len(tool.calls) != 1 || tool.calls[0] != (agentapi.DBToolOpenRequest{Database: "shop_wp", Minutes: 120}) {
		t.Fatalf("calls %+v", tool.calls)
	}

	var n int
	if err := st.RawCount(ctx, `SELECT count(*) FROM audit_log WHERE action = 'database.tool.open' AND target = 'shop_wp' AND detail LIKE ?`,
		`%"minutes":"120"%`, &n); err != nil || n != 1 {
		t.Fatalf("audit %d %v", n, err)
	}

	// An agent answer without a valid ticket is not passed on.
	tool.res.Ticket = "../x"

	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); err == nil {
		t.Fatal("invalid ticket passed on")
	}

	tool.err = errDown

	if _, err := s.OpenDatabaseTool(ctx, 1, "shop_wp", 30); !errors.Is(err, errDown) {
		t.Fatalf("agent error: %v", err)
	}
}
