// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

type fakeToolUnits struct{ calls []string }

func (f *fakeToolUnits) Reload(context.Context) error {
	f.calls = append(f.calls, "reload")
	return nil
}

func (f *fakeToolUnits) RestartDBTool(context.Context) error {
	f.calls = append(f.calls, "restart")

	return nil
}

// remoteTool is a tool with its files below a temporary directory. As root
// the tool user gets another uid, so the ticket can be handed to it.
func remoteTool(t *testing.T) (*dbTool, *fakeToolUnits, string) {
	t.Helper()

	uid, gid := os.Getuid(), os.Getgid()
	if uid == 0 {
		uid, gid = 4242, 4242
	}

	fakeLookup(t, map[string]*user.User{agentapi.DBToolUser: {Uid: strconv.Itoa(uid), Gid: strconv.Itoa(gid)}})

	root := t.TempDir()
	tool := newDBTool(nil, filepath.Join(root, "run", "tickets"), slog.New(slog.DiscardHandler))
	u := &fakeToolUnits{}
	tool.units, tool.serversDir, tool.unitDir = u, filepath.Join(root, "servers"), filepath.Join(root, "units")
	tool.after = func(time.Duration, func()) {}

	return tool, u, root
}

var toolServers = agentapi.DBToolServersRequest{Servers: []agentapi.DBToolServer{
	{ID: 3, Host: "db.example.net", Addrs: []string{"192.0.2.5", "2001:db8::5"}, CAPEM: "-----BEGIN CERTIFICATE-----\nx\n"},
	{ID: 2, Host: "192.0.2.9", Addrs: []string{"192.0.2.9"}},
}}

func TestDBToolServers(t *testing.T) {
	tool, u, _ := remoteTool(t)

	res, err := tool.setServers(t.Context(), agentrpc.Caller{}, toolServers)
	if err != nil || !res.Restarted || strings.Join(u.calls, ",") != "reload,restart" {
		t.Fatalf("set %+v %v, calls %v", res, err, u.calls)
	}

	drop, err := os.ReadFile(tool.dropInPath())
	if err != nil {
		t.Fatal(err)
	}

	hostsPath := filepath.Join(tool.serverDir(), "hosts")
	for _, want := range []string{
		"RestrictAddressFamilies=AF_INET AF_INET6\n",
		"IPAddressAllow=192.0.2.9 192.0.2.5 2001:db8::5\n",
		"BindReadOnlyPaths=" + hostsPath + ":/etc/hosts\n",
	} {
		if !strings.Contains(string(drop), want) {
			t.Errorf("drop-in lacks %q:\n%s", want, drop)
		}
	}

	hs, _ := os.ReadFile(hostsPath)
	if !strings.HasSuffix(string(hs), "::1 localhost\n192.0.2.5 db.example.net\n2001:db8::5 db.example.net\n") {
		t.Errorf("hosts:\n%s", hs)
	}

	if ca, err := os.ReadFile(tool.caFile(3)); err != nil || string(ca) != toolServers.Servers[0].CAPEM {
		t.Errorf("CA %q %v", ca, err)
	}

	if fi, err := os.Stat(hostsPath); err != nil || fi.Mode().Perm() != 0o644 {
		t.Errorf("hosts mode %v %v", fi, err)
	}

	// The same list again changes nothing and keeps the sessions.
	u.calls = nil

	if res, err := tool.setServers(t.Context(), agentrpc.Caller{}, toolServers); err != nil || res.Restarted || len(u.calls) != 0 {
		t.Fatalf("again %+v %v, calls %v", res, err, u.calls)
	}

	// Without servers the drop-in and the CA go: no network again.
	if res, err := tool.setServers(t.Context(), agentrpc.Caller{}, agentapi.DBToolServersRequest{}); err != nil || !res.Restarted {
		t.Fatalf("empty %+v %v", res, err)
	}

	if _, err := os.Stat(tool.dropInPath()); !os.IsNotExist(err) {
		t.Errorf("drop-in kept: %v", err)
	}

	if _, err := os.Stat(tool.caFile(3)); !os.IsNotExist(err) {
		t.Errorf("CA kept: %v", err)
	}
}

func remoteReq() agentapi.DBToolRemoteRequest {
	return agentapi.DBToolRemoteRequest{
		Database: "shop_wp", Minutes: 30, ServerID: 3, Host: "db.example.net", Port: 3306, TLS: "verify-ca",
		User: "hostashi_tool_0123456789abcdef", Password: "Abcdefghijklmnopqrstuvwxyz012345",
	}
}

func TestDBToolRemote(t *testing.T) {
	tool, _, _ := remoteTool(t)
	now := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	tool.now = func() time.Time { return now }

	var pe *agentrpc.Error

	// Unknown server until the list arrives.
	if _, err := tool.remote(t.Context(), agentrpc.Caller{}, remoteReq()); !errors.As(err, &pe) || pe.Code != agentrpc.CodePrecondition {
		t.Fatalf("unknown server: %v", err)
	}

	if _, err := tool.setServers(t.Context(), agentrpc.Caller{}, toolServers); err != nil {
		t.Fatal(err)
	}

	res, err := tool.remote(t.Context(), agentrpc.Caller{}, remoteReq())
	if err != nil || !agentapi.ValidDBToolTicket(res.Ticket) || !res.Expires.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("remote %+v %v", res, err)
	}

	raw, err := os.ReadFile(filepath.Join(tool.dir, res.Ticket))
	if err != nil {
		t.Fatal(err)
	}

	var tk agentapi.DBToolTicket
	if err := json.Unmarshal(raw, &tk); err != nil {
		t.Fatal(err)
	}

	if tk.Host != "db.example.net" || tk.Port != 3306 || tk.TLS != "verify-ca" || tk.CAFile != agentapi.DBToolCAFile(3) ||
		tk.User != remoteReq().User || tk.Password != remoteReq().Password || tk.Database != "shop_wp" {
		t.Errorf("ticket %+v", tk)
	}

	if tk.Driver != "" {
		t.Errorf("MariaDB ticket with driver %q", tk.Driver)
	}

	// A PostgreSQL server gets Adminer's pgsql driver (ADR-0051).
	pg := remoteReq()
	pg.Port, pg.Engine = 5432, agentapi.DBToolEnginePostgreSQL

	res, err = tool.remote(t.Context(), agentrpc.Caller{}, pg)
	if err != nil {
		t.Fatal(err)
	}

	raw, err = os.ReadFile(filepath.Join(tool.dir, res.Ticket))
	if err != nil {
		t.Fatal(err)
	}

	tk = agentapi.DBToolTicket{}
	if err := json.Unmarshal(raw, &tk); err != nil || tk.Driver != agentapi.DBToolDriverPgSQL || tk.Port != 5432 {
		t.Errorf("PostgreSQL ticket %+v %v", tk, err)
	}

	// Another host than the list's is refused, so is a server without CA.
	other := remoteReq()
	other.Host = "evil.example.net"

	if _, err := tool.remote(t.Context(), agentrpc.Caller{}, other); !errors.As(err, &pe) {
		t.Errorf("other host: %v", err)
	}

	noCA := remoteReq()
	noCA.ServerID, noCA.Host = 2, "192.0.2.9"

	if _, err := tool.remote(t.Context(), agentrpc.Caller{}, noCA); !errors.As(err, &pe) {
		t.Errorf("no CA: %v", err)
	}

	noCA.TLS = "none"

	if _, err := tool.remote(t.Context(), agentrpc.Caller{}, noCA); err != nil {
		t.Errorf("no TLS: %v", err)
	}
}

func TestDBToolRemoteValidation(t *testing.T) {
	for name, mutate := range map[string]func(*agentapi.DBToolRemoteRequest){
		"user":     func(r *agentapi.DBToolRemoteRequest) { r.User = "shop_user" },
		"password": func(r *agentapi.DBToolRemoteRequest) { r.Password = "short" },
		"host":     func(r *agentapi.DBToolRemoteRequest) { r.Host = "db\nx" },
		"tls":      func(r *agentapi.DBToolRemoteRequest) { r.TLS = "off" },
		"minutes":  func(r *agentapi.DBToolRemoteRequest) { r.Minutes = 5 },
		"server":   func(r *agentapi.DBToolRemoteRequest) { r.ServerID = 0 },
		"engine":   func(r *agentapi.DBToolRemoteRequest) { r.Engine = "sqlite" },
	} {
		r := remoteReq()
		mutate(&r)

		if r.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}

	bad := agentapi.DBToolServersRequest{Servers: []agentapi.DBToolServer{{ID: 1, Host: "db.example.net", Addrs: []string{"127.0.0.1"}}}}
	if bad.Validate() == nil {
		t.Error("loopback server accepted")
	}

	dup := agentapi.DBToolServersRequest{Servers: []agentapi.DBToolServer{toolServers.Servers[0], toolServers.Servers[0]}}
	if dup.Validate() == nil {
		t.Error("duplicate id accepted")
	}
}

// TestDBToolServerFilesReadable: the agent runs with UMask=0077, the pool's
// user still reads the CAs and the hosts file.
func TestDBToolServerFilesReadable(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)

	p := filepath.Join(t.TempDir(), "ca-1.pem")
	if _, err := writeIfChanged(p, []byte("x")); err != nil {
		t.Fatal(err)
	}

	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o644 {
		t.Fatalf("mode %v, %v", st.Mode(), err)
	}
}
