// SPDX-License-Identifier: AGPL-3.0-or-later

package agentrpc

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Test operations. Echo has a validated field so input errors can be tested.
type emptyReq struct{}

func (emptyReq) Validate() error { return nil }

type whoResp struct {
	UID uint32 `json:"uid"`
}

type echoReq struct {
	Text string `json:"text"`
}

func (r echoReq) Validate() error {
	if r.Text == "" || len(r.Text) > 16 {
		return errors.New("text: 1..16 bytes")
	}
	return nil
}

type echoResp struct {
	Text string `json:"text"`
}

var (
	opWho   = Op[emptyReq, whoResp]{Name: "who"}
	opEcho  = Op[echoReq, echoResp]{Name: "echo"}
	opBoom  = Op[emptyReq, whoResp]{Name: "boom"}
	opPanic = Op[emptyReq, whoResp]{Name: "panic"}
	opSlow  = Op[emptyReq, whoResp]{Name: "slow"}
	opHuge  = Op[emptyReq, echoResp]{Name: "huge"}
	opExec  = Op[emptyReq, whoResp]{Name: "exec"} // never registered
)

func socketDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "run")
	if err := os.Mkdir(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	return dir
}

func newTestServer(auth Authorizer, o Options) *Server {
	s := NewServer(auth, o)
	Handle(s, opWho, func(_ context.Context, c Caller, _ emptyReq) (whoResp, error) {
		return whoResp{UID: c.UID}, nil
	})
	Handle(s, opEcho, func(_ context.Context, _ Caller, r echoReq) (echoResp, error) {
		return echoResp(r), nil
	})
	Handle(s, opBoom, func(context.Context, Caller, emptyReq) (whoResp, error) {
		return whoResp{}, errors.New("open /etc/shadow: permission denied")
	})
	Handle(s, opPanic, func(context.Context, Caller, emptyReq) (whoResp, error) {
		panic("bug")
	})
	Handle(s, opSlow, func(ctx context.Context, _ Caller, _ emptyReq) (whoResp, error) {
		<-ctx.Done()
		return whoResp{}, ctx.Err()
	})
	Handle(s, opHuge, func(context.Context, Caller, emptyReq) (echoResp, error) {
		return echoResp{Text: strings.Repeat("x", MaxFrame)}, nil
	})
	return s
}

func start(t *testing.T, auth Authorizer, o Options) (string, *Client) {
	t.Helper()
	sock := filepath.Join(socketDir(t), "agent.sock")
	s := newTestServer(auth, o)
	if err := s.Listen(context.Background(), sock, -1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Serve: %v", err)
		}
	})
	return sock, NewClient(sock)
}

func allowSelf() Authorizer { return AllowUIDs(uint32(os.Getuid())) }

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("want error code %q, got %v", code, err)
	}
}

func TestRoundTripReportsKernelUID(t *testing.T) {
	_, c := start(t, allowSelf(), Options{})
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		r, err := opWho.Call(ctx, c, emptyReq{})
		if err != nil {
			t.Fatal(err)
		}
		if r.UID != uint32(os.Getuid()) {
			t.Fatalf("uid %d, want %d", r.UID, os.Getuid())
		}
	}
	r, err := opEcho.Call(ctx, c, echoReq{Text: "hello"})
	if err != nil || r.Text != "hello" {
		t.Fatalf("echo: %+v, %v", r, err)
	}
}

func TestCallErrors(t *testing.T) {
	sock, c := start(t, allowSelf(), Options{})
	ctx := context.Background()

	_, err := opExec.Call(ctx, c, emptyReq{})
	wantCode(t, err, CodeUnknownOperation)

	// Client-side validation stops bad input before it is sent.
	_, err = opEcho.Call(ctx, c, echoReq{Text: ""})
	wantCode(t, err, CodeInvalidArgument)

	// Internal error text never reaches the client.
	_, err = opBoom.Call(ctx, c, emptyReq{})
	wantCode(t, err, CodeInternal)
	if strings.Contains(err.Error(), "shadow") {
		t.Fatalf("internal error leaked: %v", err)
	}

	// A panicking handler keeps the agent alive.
	_, err = opPanic.Call(ctx, c, emptyReq{})
	wantCode(t, err, CodeInternal)
	if _, err := opWho.Call(ctx, c, emptyReq{}); err != nil {
		t.Fatalf("agent unusable after panic: %v", err)
	}

	// A response larger than a frame closes the connection, agent survives.
	if _, err := opHuge.Call(ctx, c, emptyReq{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("huge response: %v", err)
	}
	if _, err := opWho.Call(ctx, c, emptyReq{}); err != nil {
		t.Fatalf("agent unusable after huge response: %v", err)
	}

	// Raw frames that bypass the typed client.
	cases := []struct {
		name, body, code string
	}{
		{"not json", `{"op":`, CodeBadRequest},
		{"server validates too", `{"op":"echo","params":{"text":""}}`, CodeInvalidArgument},
		{"unknown field", `{"op":"echo","params":{"text":"a","cmd":"id"}}`, CodeBadRequest},
		{"wrong type", `{"op":"echo","params":{"text":1}}`, CodeBadRequest},
		{"trailing data", `{"op":"echo","params":{"text":"a"} }`, ""},
		{"params not object", `{"op":"echo","params":"rm -rf /"}`, CodeBadRequest},
		{"missing params", `{"op":"who"}`, ""},
		{"empty op", `{"op":"","params":{}}`, CodeUnknownOperation},
		{"shell in op name", `{"op":"who; id","params":{}}`, CodeUnknownOperation},
		{"unknown top-level field", `{"op":"who","params":{},"exec":"id"}`, CodeBadRequest},
		{"two requests in one frame", `{"op":"who","params":{}}{"op":"who"}`, CodeBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := rawCall(t, sock, []byte(tc.body))
			if tc.code == "" {
				if !resp.OK {
					t.Fatalf("want ok, got %+v", resp.Error)
				}
				return
			}
			if resp.OK || resp.Error == nil || resp.Error.Code != tc.code {
				t.Fatalf("want %s, got %+v", tc.code, resp)
			}
		})
	}
}

func TestTrailingParamsRejected(t *testing.T) {
	// The top-level decoder keeps only one params value, so test the
	// per-operation decoder directly.
	s := newTestServer(nil, Options{})
	_, err := s.ops["echo"](context.Background(), Caller{}, json.RawMessage(`{"text":"a"}{"text":"b"}`))
	wantCode(t, err, CodeBadRequest)
}

func rawConn(t *testing.T, sock string) net.Conn {
	t.Helper()
	conn, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	return conn
}

func rawCall(t *testing.T, sock string, body []byte) response {
	t.Helper()
	conn := rawConn(t, sock)
	if err := writeFrame(conn, body); err != nil {
		t.Fatal(err)
	}
	rb, err := readFrame(conn)
	if err != nil {
		t.Fatal(err)
	}
	var resp response
	if err := json.Unmarshal(rb, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestOversizedFrameClosesConnection(t *testing.T) {
	sock, c := start(t, allowSelf(), Options{})
	conn := rawConn(t, sock)
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], MaxFrame+1)
	if _, err := conn.Write(hdr[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF, got %v", err)
	}
	if _, err := opWho.Call(context.Background(), c, emptyReq{}); err != nil {
		t.Fatalf("agent died: %v", err)
	}
}

func TestUnauthorizedPeerGetsNoReply(t *testing.T) {
	for name, auth := range map[string]Authorizer{
		"other uid": AllowUIDs(uint32(os.Getuid()) + 1),
		"nil":       nil,
	} {
		t.Run(name, func(t *testing.T) {
			_, c := start(t, auth, Options{})
			_, err := opWho.Call(context.Background(), c, emptyReq{})
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("want ErrUnavailable, got %v", err)
			}
		})
	}
}

func TestAgentNotRunning(t *testing.T) {
	c := NewClient(filepath.Join(t.TempDir(), "missing.sock"))
	if _, err := opWho.Call(context.Background(), c, emptyReq{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("want ErrUnavailable, got %v", err)
	}
}

func TestRequestTimeout(t *testing.T) {
	_, c := start(t, allowSelf(), Options{RequestTimeout: 50 * time.Millisecond})
	_, err := opSlow.Call(context.Background(), c, emptyReq{})
	wantCode(t, err, CodeInternal)
}

func TestClientContextDeadline(t *testing.T) {
	_, c := start(t, allowSelf(), Options{RequestTimeout: 5 * time.Second})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := opSlow.Call(ctx, c, emptyReq{}); err == nil {
		t.Fatal("want error")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("client ignored its deadline: %v", d)
	}
}

func TestConnectionLimit(t *testing.T) {
	sock, c := start(t, allowSelf(), Options{MaxConns: 2})
	// Two idle connections that hold their slot after a first call.
	for i := 0; i < 2; i++ {
		conn := rawConn(t, sock)
		if err := writeFrame(conn, []byte(`{"op":"who","params":{}}`)); err != nil {
			t.Fatal(err)
		}
		if _, err := readFrame(conn); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := opWho.Call(context.Background(), c, emptyReq{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("third connection: want ErrUnavailable, got %v", err)
	}
}

func TestIdleTimeout(t *testing.T) {
	sock, _ := start(t, allowSelf(), Options{IdleTimeout: 50 * time.Millisecond})
	conn := rawConn(t, sock)
	if _, err := conn.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
		t.Fatalf("want EOF after idle timeout, got %v", err)
	}
}

func TestSocketMode(t *testing.T) {
	sock, _ := start(t, allowSelf(), Options{})
	fi, err := os.Stat(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Type() != os.ModeSocket || fi.Mode().Perm() != 0o660 {
		t.Fatalf("socket mode %v", fi.Mode())
	}
}

func TestListenRejectsInsecureDirectory(t *testing.T) {
	for _, mode := range []os.FileMode{0o755, 0o770, 0o777, 0o751} {
		t.Run(mode.String(), func(t *testing.T) {
			dir := socketDir(t)
			if err := os.Chmod(dir, mode); err != nil {
				t.Fatal(err)
			}
			err := NewServer(nil, Options{}).Listen(context.Background(), filepath.Join(dir, "agent.sock"), -1)
			if !errors.Is(err, ErrInsecureSocketDir) {
				t.Fatalf("want ErrInsecureSocketDir, got %v", err)
			}
		})
	}
	err := NewServer(nil, Options{}).Listen(context.Background(), filepath.Join(t.TempDir(), "missing", "agent.sock"), -1)
	if err == nil {
		t.Fatal("missing directory accepted")
	}
}

func TestListenDoesNotRemoveRegularFile(t *testing.T) {
	dir := socketDir(t)
	path := filepath.Join(dir, "agent.sock")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := NewServer(nil, Options{}).Listen(context.Background(), path, -1); err == nil {
		t.Fatal("regular file was replaced")
	}
	if b, _ := os.ReadFile(path); string(b) != "keep" {
		t.Fatal("regular file changed")
	}
}

func TestListenReplacesStaleSocket(t *testing.T) {
	dir := socketDir(t)
	path := filepath.Join(dir, "agent.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	l.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = l.Close()
	s := NewServer(nil, Options{})
	if err := s.Listen(context.Background(), path, -1); err != nil {
		t.Fatalf("stale socket not replaced: %v", err)
	}
	_ = s.listener.Close()
}

func TestServeWithoutListen(t *testing.T) {
	if err := NewServer(nil, Options{}).Serve(context.Background()); err == nil {
		t.Fatal("want error")
	}
}

func TestHandleRejectsBadNames(t *testing.T) {
	for _, name := range []string{"", "Ping", "site create", "site/create", "a..b", "x;id", "1op"} {
		t.Run(fmt.Sprintf("%q", name), func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("no panic")
				}
			}()
			Handle(NewServer(nil, Options{}), Op[emptyReq, whoResp]{Name: name},
				func(context.Context, Caller, emptyReq) (whoResp, error) { return whoResp{}, nil })
		})
	}
}

func TestHandleRejectsDuplicates(t *testing.T) {
	s := newTestServer(nil, Options{})
	defer func() {
		if recover() == nil {
			t.Fatal("no panic")
		}
	}()
	Handle(s, opWho, func(context.Context, Caller, emptyReq) (whoResp, error) { return whoResp{}, nil })
}

func TestAllowUIDs(t *testing.T) {
	a := AllowUIDs(0, 1000)
	for uid, want := range map[uint32]bool{0: true, 1000: true, 1001: false, 65534: false} {
		if got := a(Caller{UID: uid}); got != want {
			t.Errorf("uid %d: got %v, want %v", uid, got, want)
		}
	}
	if AllowUIDs()(Caller{UID: 0}) {
		t.Error("empty allow list accepts root")
	}
}

// TestClientWriteFailureIsUnavailable covers the race where the agent closes
// the connection before the client has written its request.
func TestClientWriteFailureIsUnavailable(t *testing.T) {
	dir := socketDir(t)
	sock := filepath.Join(dir, "agent.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				return
			}
			_ = conn.Close()
		}
	}()
	c := NewClient(sock)
	for i := 0; i < 50; i++ {
		if _, err := opEcho.Call(context.Background(), c, echoReq{Text: "x"}); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("call %d: want ErrUnavailable, got %v", i, err)
		}
	}
}
