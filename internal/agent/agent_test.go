// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"os"
	"os/user"
	"path/filepath"
	"syscall"
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
	if err := s.Listen(context.Background(), sock, -1); err != nil {
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

	addrs, err := agentapi.ServerAddrs.Call(ctx, agentrpc.NewClient(sock), agentapi.ServerAddrsRequest{})
	if err != nil || addrs.Addrs == nil {
		t.Fatalf("addrs %+v %v", addrs, err)
	}
}

func TestRunFailsWithoutServeUser(t *testing.T) {
	fakeLookup(t, nil)

	err := Run(context.Background(), Config{Socket: filepath.Join(t.TempDir(), "a.sock"), ServeUser: "hostashi"})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestPrepareSocketDir(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to own and chown the directory")
	}

	base := t.TempDir()
	dir := filepath.Join(base, "run")

	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := prepareSocketDir(dir, 65534); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}

	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || st.Gid != 65534 || st.Uid != 0 {
		t.Fatalf("owner %+v", fi.Sys())
	}

	// Open to others, owned by someone else, or a symlink: refused.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := prepareSocketDir(dir, 65534); !errors.Is(err, agentrpc.ErrInsecureSocketDir) {
		t.Fatalf("0755: %v", err)
	}

	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}

	if err := os.Chown(dir, 65534, 65534); err != nil {
		t.Fatal(err)
	}

	if err := prepareSocketDir(dir, 65534); !errors.Is(err, agentrpc.ErrInsecureSocketDir) {
		t.Fatalf("foreign owner: %v", err)
	}

	link := filepath.Join(base, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}

	if err := prepareSocketDir(link, 65534); err == nil {
		t.Fatal("symlink accepted")
	}
}
