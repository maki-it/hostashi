package agentrpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func startServer(t *testing.T, auth Authorizer, extra func(*Server)) string {
	t.Helper()
	dir := t.TempDir()
	sock := filepath.Join(dir, "agent.sock")
	s := NewServer(auth, nil)
	RegisterAll(s)
	if extra != nil {
		extra(s)
	}
	if err := s.Listen(sock, -1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = s.Serve(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return sock
}

func allowAll(Caller) bool { return true }

func dial(t *testing.T, sock string) *Client {
	t.Helper()
	c, err := Dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want error code %q, got %v", code, err)
	}
}

func TestRoundTripAndPeerUID(t *testing.T) {
	c := dial(t, startServer(t, allowAll, nil))
	r, err := Call[PingReq, PingResp](c, "Ping", PingReq{})
	if err != nil {
		t.Fatal(err)
	}
	if r.UID != uint32(os.Getuid()) {
		t.Fatalf("peer uid %d, want %d", r.UID, os.Getuid())
	}
	// Several calls on one connection.
	sc, err := Call[SiteCreateReq, SiteCreateResp](c, "SiteCreate",
		SiteCreateReq{Domain: "example.com", User: "site1", Type: SitePHP, PHPVersion: "8.3"})
	if err != nil {
		t.Fatal(err)
	}
	if sc.Home != "/home/site1" || sc.WebRoot != "/home/site1/htdocs/example.com" {
		t.Fatalf("unexpected plan %+v", sc)
	}
}

func TestErrors(t *testing.T) {
	sock := startServer(t, allowAll, func(s *Server) {
		Register(s, "Boom", func(context.Context, Caller, PingReq) (PingResp, error) {
			return PingResp{}, errors.New("secret path /etc/shadow")
		})
		Register(s, "Panic", func(context.Context, Caller, PingReq) (PingResp, error) {
			panic("bug")
		})
	})
	c := dial(t, sock)

	_, err := Call[PingReq, PingResp](c, "Exec", PingReq{})
	wantCode(t, err, CodeUnknownOperation)

	_, err = Call[SiteCreateReq, SiteCreateResp](c, "SiteCreate", SiteCreateReq{Domain: "x", User: "site1", Type: SiteStatic})
	wantCode(t, err, CodeInvalidArgument)

	// Unknown field in params (e.g. smuggling a "cmd") is rejected.
	_, err = Call[map[string]string, SiteDeleteResp](c, "SiteDelete", map[string]string{"user": "site1", "cmd": "id"})
	wantCode(t, err, CodeBadRequest)

	// Wrong JSON type for a field.
	_, err = Call[map[string]int, SiteDeleteResp](c, "SiteDelete", map[string]int{"user": 1})
	wantCode(t, err, CodeBadRequest)

	// Internal error text does not reach the client.
	_, err = Call[PingReq, PingResp](c, "Boom", PingReq{})
	wantCode(t, err, CodeInternal)
	if strings.Contains(err.Error(), "shadow") {
		t.Fatalf("internal error leaked: %v", err)
	}

	// A panicking handler does not kill the agent or the connection.
	_, err = Call[PingReq, PingResp](c, "Panic", PingReq{})
	wantCode(t, err, CodeInternal)
	if _, err := Call[PingReq, PingResp](c, "Ping", PingReq{}); err != nil {
		t.Fatalf("connection unusable after panic: %v", err)
	}
}

func rawCall(t *testing.T, sock string, frame []byte) ([]byte, error) {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(frame); err != nil {
		return nil, err
	}
	return readFrame(conn)
}

func frame(body string) []byte {
	var b bytes.Buffer
	_ = writeFrame(&b, []byte(body))
	return b.Bytes()
}

func TestMalformedInput(t *testing.T) {
	sock := startServer(t, allowAll, nil)

	out, err := rawCall(t, sock, frame("not json"))
	if err != nil || !bytes.Contains(out, []byte(CodeBadRequest)) {
		t.Fatalf("not json: %s %v", out, err)
	}
	out, err = rawCall(t, sock, frame(`{"op":"SiteDelete","params":{"user":"site1"}{"x":1}}`))
	if err != nil || !bytes.Contains(out, []byte(CodeBadRequest)) {
		t.Fatalf("trailing: %s %v", out, err)
	}
	// Oversized length header: server closes without allocating.
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], MaxFrame+1)
	if _, err := rawCall(t, sock, hdr[:]); err == nil {
		t.Fatal("oversized frame: expected closed connection")
	}
	// Server survives all of that.
	if _, err := Call[PingReq, PingResp](dial(t, sock), "Ping", PingReq{}); err != nil {
		t.Fatal(err)
	}
}

func TestFrameLimits(t *testing.T) {
	if err := writeFrame(io.Discard, make([]byte, MaxFrame+1)); err == nil {
		t.Fatal("writeFrame accepted oversized body")
	}
	if _, err := readFrame(bytes.NewReader([]byte{0, 0, 0, 5, 'a'})); err == nil {
		t.Fatal("readFrame accepted truncated body")
	}
}

func TestUnauthorizedPeerGetsNoReply(t *testing.T) {
	sock := startServer(t, func(c Caller) bool { return c.UID == 4242 }, nil)
	c := dial(t, sock)
	_, err := Call[PingReq, PingResp](c, "Ping", PingReq{})
	var e *Error
	if err == nil || errors.As(err, &e) {
		t.Fatalf("expected closed connection, got %v", err)
	}
}

func TestNilAuthorizerDeniesAll(t *testing.T) {
	c := dial(t, startServer(t, nil, nil))
	if _, err := Call[PingReq, PingResp](c, "Ping", PingReq{}); err == nil {
		t.Fatal("nil authorizer must deny")
	}
}

func TestSocketMode(t *testing.T) {
	sock := startServer(t, allowAll, nil)
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o660 {
		t.Fatalf("socket mode %v, want 0660", fi.Mode().Perm())
	}
}

func TestDuplicateRegisterPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	s := NewServer(allowAll, nil)
	RegisterAll(s)
	RegisterAll(s)
}

// TestCrossUser runs a real client process as another uid. It needs root,
// so it is skipped in normal CI. It checks two things the kernel enforces:
// the 0660 socket mode, and the uid reported by SO_PEERCRED.
func TestCrossUser(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("needs root to switch uid")
	}
	const nobody = 65534
	dir, err := os.MkdirTemp("", "s1-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "s1client")
	build := exec.Command("go", "build", "-o", bin, "../cmd/s1client")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	sock := filepath.Join(dir, "agent.sock")
	s := NewServer(func(c Caller) bool { return c.UID == nobody }, nil)
	RegisterAll(s)
	if err := s.Listen(sock, -1); err != nil { // root:root 0660
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Serve(ctx) }()

	run := func() (string, int) {
		cmd := exec.Command(bin, sock)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: nobody, Gid: nobody}}
		out, _ := cmd.CombinedOutput()
		return string(out), cmd.ProcessState.ExitCode()
	}

	// 1. Socket is root:root 0660, so nobody cannot even connect.
	out, code := run()
	if code != 2 || !strings.Contains(out, "permission denied") {
		t.Fatalf("expected EACCES on connect, got exit %d: %s", code, out)
	}

	// 2. Grant the group, as the installer would for group "hostashi".
	if err := os.Chown(sock, -1, nobody); err != nil {
		t.Fatal(err)
	}
	out, code = run()
	if code != 0 || strings.TrimSpace(out) != "uid 65534" {
		t.Fatalf("expected uid 65534, got exit %d: %s", code, out)
	}

	// 3. Root connects at the socket level, but the authorizer rejects it.
	c := dial(t, sock)
	if _, err := Call[PingReq, PingResp](c, "Ping", PingReq{}); err == nil {
		t.Fatal("root should be rejected by authorizer")
	}
}
