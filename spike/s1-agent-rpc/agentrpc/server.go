package agentrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

// Caller identifies the peer, as reported by the kernel (SO_PEERCRED).
type Caller struct {
	UID, GID uint32
	PID      int32
}

// Authorizer decides whether a peer may talk to the agent at all.
type Authorizer func(Caller) bool

// Validator is implemented by every request type. Validation is positive:
// anything not explicitly allowed is rejected.
type Validator interface{ Validate() error }

type handler func(ctx context.Context, c Caller, params json.RawMessage) (any, error)

// Server dispatches typed operations. There is no generic exec operation.
type Server struct {
	auth Authorizer
	log  *slog.Logger
	ops  map[string]handler

	mu       sync.Mutex
	listener net.Listener
}

func NewServer(auth Authorizer, log *slog.Logger) *Server {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Server{auth: auth, log: log, ops: map[string]handler{}}
}

// Register adds one operation. This is the whole per-operation plumbing:
// the request type carries its own validation, the response is any JSON value.
func Register[Req Validator, Resp any](s *Server, op string, fn func(ctx context.Context, c Caller, req Req) (Resp, error)) {
	if _, dup := s.ops[op]; dup {
		panic("agentrpc: duplicate operation " + op)
	}
	s.ops[op] = func(ctx context.Context, c Caller, raw json.RawMessage) (any, error) {
		var req Req
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&req); err != nil {
			return nil, &Error{CodeBadRequest, "params: " + err.Error()}
		}
		if dec.More() {
			return nil, &Error{CodeBadRequest, "params: trailing data"}
		}
		if err := req.Validate(); err != nil {
			return nil, &Error{CodeInvalidArgument, err.Error()}
		}
		return fn(ctx, c, req)
	}
}

// Listen creates the socket with mode 0660 and optional group ownership.
// The umask is not touched (process-wide); the mode is set after bind, and the
// directory is expected to be 0750 so nobody can connect in the short window.
func (s *Server) Listen(path string, gid int) error {
	_ = os.Remove(path) // stale socket from a previous run
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	if gid >= 0 {
		if err := os.Chown(path, -1, gid); err != nil {
			l.Close()
			return err
		}
	}
	if err := os.Chmod(path, 0o660); err != nil {
		l.Close()
		return err
	}
	s.mu.Lock()
	s.listener = l
	s.mu.Unlock()
	return nil
}

// Serve accepts connections until ctx is cancelled.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	l := s.listener
	s.mu.Unlock()
	if l == nil {
		return errors.New("agentrpc: Listen not called")
	}
	go func() { <-ctx.Done(); l.Close() }()
	for {
		conn, err := l.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		go s.serveConn(ctx, conn.(*net.UnixConn))
	}
}

// PeerCred reads SO_PEERCRED from a Unix connection.
func PeerCred(c *net.UnixConn) (Caller, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return Caller{}, err
	}
	var cred *syscall.Ucred
	var serr error
	if err := raw.Control(func(fd uintptr) {
		cred, serr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return Caller{}, err
	}
	if serr != nil {
		return Caller{}, serr
	}
	return Caller{UID: cred.Uid, GID: cred.Gid, PID: cred.Pid}, nil
}

const (
	idleTimeout    = 30 * time.Second
	requestTimeout = 10 * time.Second
)

func (s *Server) serveConn(ctx context.Context, conn *net.UnixConn) {
	defer conn.Close()
	caller, err := PeerCred(conn)
	if err != nil {
		s.log.Error("peercred failed", "err", err)
		return
	}
	if s.auth == nil || !s.auth(caller) {
		// Deliberately no reply: do not give unauthorized peers a protocol oracle.
		s.log.Warn("peer rejected", "uid", caller.UID, "gid", caller.GID, "pid", caller.PID)
		return
	}
	for {
		_ = conn.SetReadDeadline(time.Now().Add(idleTimeout))
		body, err := readFrame(conn)
		if err != nil {
			if !errors.Is(err, io.EOF) {
				s.log.Warn("read failed", "uid", caller.UID, "err", err)
			}
			return
		}
		resp := s.dispatch(ctx, caller, body)
		out, err := json.Marshal(resp)
		if err != nil {
			out = []byte(`{"ok":false,"error":{"code":"internal","message":"encode failed"}}`)
		}
		_ = conn.SetWriteDeadline(time.Now().Add(requestTimeout))
		if err := writeFrame(conn, out); err != nil {
			return
		}
	}
}

func (s *Server) dispatch(ctx context.Context, c Caller, body []byte) (resp Response) {
	var req Request
	if err := json.Unmarshal(body, &req); err != nil {
		return failure(&Error{CodeBadRequest, "request is not valid JSON"})
	}
	h, ok := s.ops[req.Op]
	if !ok {
		s.log.Warn("unknown operation", "uid", c.UID, "op", req.Op)
		return failure(&Error{CodeUnknownOperation, fmt.Sprintf("unknown operation %q", req.Op)})
	}
	defer func() {
		if r := recover(); r != nil {
			s.log.Error("handler panic", "op", req.Op, "panic", r)
			resp = failure(&Error{CodeInternal, "internal error"})
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	out, err := h(ctx, c, req.Params)
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			s.log.Info("call failed", "uid", c.UID, "op", req.Op, "code", e.Code)
			return failure(e)
		}
		// Never leak internal error text to the caller.
		s.log.Error("call failed", "uid", c.UID, "op", req.Op, "err", err)
		return failure(&Error{CodeInternal, "internal error"})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return failure(&Error{CodeInternal, "internal error"})
	}
	s.log.Info("call ok", "uid", c.UID, "op", req.Op)
	return Response{OK: true, Result: raw}
}

func failure(e *Error) Response { return Response{OK: false, Error: e} }
