// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

func TestCheckEUID(t *testing.T) {
	if err := CheckEUID(0); err != nil {
		t.Fatal(err)
	}
	if err := CheckEUID(1000); !errors.Is(err, ErrNotRoot) {
		t.Fatalf("got %v", err)
	}
}

func fakeLookup(t *testing.T, users map[string]*user.User) {
	t.Helper()
	old := lookupUser
	lookupUser = func(name string) (*user.User, error) {
		if u, ok := users[name]; ok {
			return u, nil
		}
		return nil, user.UnknownUserError(name)
	}
	t.Cleanup(func() { lookupUser = old })
}

func TestServeIdentity(t *testing.T) {
	fakeLookup(t, map[string]*user.User{
		"hostashi": {Uid: "998", Gid: "997"},
		"toor":     {Uid: "0", Gid: "0"},
		"badgid":   {Uid: "998", Gid: "-1"},
		"baduid":   {Uid: "x", Gid: "1"},
	})
	uid, gid, err := serveIdentity("hostashi")
	if err != nil || uid != 998 || gid != 997 {
		t.Fatalf("got %d %d %v", uid, gid, err)
	}
	for _, name := range []string{"", "root", "toor", "missing", "badgid", "baduid"} {
		if _, _, err := serveIdentity(name); err == nil {
			t.Errorf("%q accepted", name)
		}
	}
}

func TestPingThroughServer(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "agent.sock")
	// Allow the current test user as the "serve user".
	s := NewServer(Config{Version: "1.2.3"}, uint32(os.Getuid()))
	if err := s.Listen(sock, -1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Serve(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	r, err := agentapi.Ping.Call(ctx, agentrpc.NewClient(sock), agentapi.PingRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "1.2.3" || r.CallerUID != uint32(os.Getuid()) {
		t.Fatalf("got %+v", r)
	}
}

func TestRunFailsWithoutServeUser(t *testing.T) {
	fakeLookup(t, nil)
	err := Run(context.Background(), Config{Socket: filepath.Join(t.TempDir(), "a.sock"), ServeUser: "hostashi"})
	if err == nil {
		t.Fatal("want error")
	}
}
