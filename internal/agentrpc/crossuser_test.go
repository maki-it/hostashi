// SPDX-License-Identifier: AGPL-3.0-or-later

package agentrpc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const crossUserEnv = "HOSTASHI_TEST_CROSSUSER_SOCKET"

// TestMain lets the test binary act as an unprivileged client when it is
// re-executed by TestCrossUser.
func TestMain(m *testing.M) {
	if sock := os.Getenv(crossUserEnv); sock != "" {
		r, err := opWho.Call(context.Background(), NewClient(sock), emptyReq{})
		switch {
		case err == nil:
			fmt.Printf("uid=%d\n", r.UID)
		case errors.Is(err, ErrUnavailable) && strings.Contains(err.Error(), "permission denied"):
			fmt.Println("denied-by-mode")
		case errors.Is(err, ErrUnavailable):
			fmt.Println("no-reply")
		default:
			fmt.Println("error:", err)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestCrossUser runs a real client process as uid/gid 65534 against an
// agent running as root. It needs root and skips otherwise.
func TestCrossUser(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	const nobody = 65534

	// Copy the test binary somewhere uid 65534 can execute it.
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// t.TempDir nests in a 0700 directory, so create one that uid 65534 can enter.
	base, err := os.MkdirTemp("", "hostashi-crossuser-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	if err := os.Chmod(base, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(base, "client")
	copyFile(t, self, bin)

	runDir := filepath.Join(base, "run")
	if err := os.Mkdir(runDir, 0o750); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(runDir, "agent.sock")

	runAs := func() string {
		t.Helper()
		cmd := exec.Command(bin)
		cmd.Env = []string{crossUserEnv + "=" + sock}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: nobody, Gid: nobody}}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("client: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}

	serve := func(auth Authorizer, gid int) func() {
		s := newTestServer(auth, Options{})
		if err := s.Listen(context.Background(), sock, gid); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { _ = s.Serve(ctx); close(done) }()
		return func() { cancel(); <-done }
	}

	// 1. Directory root:root 0750: uid 65534 cannot even reach the socket.
	stop := serve(AllowUIDs(0, nobody), -1)
	if got := runAs(); got != "denied-by-mode" {
		t.Fatalf("step 1: %s", got)
	}
	stop()

	// 2. Group access granted, but only root is authorized: no reply.
	if err := os.Chown(runDir, 0, nobody); err != nil {
		t.Fatal(err)
	}
	stop = serve(AllowUIDs(0), nobody)
	if got := runAs(); got != "no-reply" {
		t.Fatalf("step 2: %s", got)
	}
	stop()

	// 3. Authorized by uid: the agent sees the kernel-reported uid.
	stop = serve(AllowUIDs(0, nobody), nobody)
	defer stop()
	if got := runAs(); got != fmt.Sprintf("uid=%d", nobody) {
		t.Fatalf("step 3: %s", got)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
