// SPDX-License-Identifier: AGPL-3.0-or-later

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
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/maki-it/hostashi/internal/reqid"
)

// Caller identifies the peer, as reported by the kernel (SO_PEERCRED).
// PID is for logs only: it can be reused and must not drive decisions.
type Caller struct {
	UID, GID uint32
	PID      int32
}

// Authorizer decides whether a peer may talk to the agent at all.
type Authorizer func(Caller) bool

// AllowUIDs returns an Authorizer that accepts exactly the given uids.
// Authorization is by uid, because SO_PEERCRED does not report
// supplementary groups (spike S1, finding 1).
func AllowUIDs(uids ...uint32) Authorizer {
	allowed := make(map[uint32]bool, len(uids))
	for _, u := range uids {
		allowed[u] = true
	}

	return func(c Caller) bool { return allowed[c.UID] }
}

type handler func(ctx context.Context, c Caller, params json.RawMessage) (any, error)

// Options tune a Server. Zero values select safe defaults.
type Options struct {
	Log            *slog.Logger
	MaxConns       int           // concurrent connections, default 16
	IdleTimeout    time.Duration // per connection, default 30s
	RequestTimeout time.Duration // per call, default 10s
}

// Server dispatches typed operations.
type Server struct {
	auth Authorizer
	log  *slog.Logger
	ops  map[string]handler
	sem  chan struct{}

	idleTimeout    time.Duration
	requestTimeout time.Duration

	mu       sync.Mutex
	listener *net.UnixListener
	wg       sync.WaitGroup
}

// NewServer creates a server. A nil Authorizer denies every peer.
func NewServer(auth Authorizer, o Options) *Server {
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}

	if o.MaxConns <= 0 {
		o.MaxConns = 16
	}

	if o.IdleTimeout <= 0 {
		o.IdleTimeout = 30 * time.Second
	}

	if o.RequestTimeout <= 0 {
		o.RequestTimeout = 10 * time.Second
	}

	if auth == nil {
		auth = func(Caller) bool { return false }
	}

	return &Server{
		auth:           auth,
		log:            o.Log,
		ops:            map[string]handler{},
		sem:            make(chan struct{}, o.MaxConns),
		idleTimeout:    o.IdleTimeout,
		requestTimeout: o.RequestTimeout,
	}
}

// ErrInsecureSocketDir is returned when the socket directory could let
// other users in during the window between bind and chmod.
var ErrInsecureSocketDir = errors.New("agentrpc: socket directory must not be accessible to others or writable by the group")

// Listen creates the socket at path with mode 0660 and, if gid >= 0, that
// group. The parent directory must already exist with no access for others
// and no group write (for example systemd RuntimeDirectoryMode=0750), which
// closes the window between bind and chmod (spike S1, finding 3).
// An existing socket file is replaced, any other file is left alone.
func (s *Server) Listen(ctx context.Context, path string, gid int) error {
	dir := filepath.Dir(path)

	fi, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("agentrpc: socket directory: %w", err)
	}

	if !fi.IsDir() || fi.Mode().Perm()&0o027 != 0 {
		return fmt.Errorf("%w: %s has mode %v", ErrInsecureSocketDir, dir, fi.Mode().Perm())
	}

	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode().Type() != os.ModeSocket {
			return fmt.Errorf("agentrpc: %s exists and is not a socket", path)
		}

		if err := os.Remove(path); err != nil {
			return err
		}
	}

	var lc net.ListenConfig

	nl, err := lc.Listen(ctx, "unix", path)
	if err != nil {
		return err
	}

	l, ok := nl.(*net.UnixListener)
	if !ok {
		_ = nl.Close()
		return fmt.Errorf("agentrpc: unexpected listener type %T", nl)
	}

	if gid >= 0 {
		if err := os.Chown(path, -1, gid); err != nil {
			_ = l.Close()
			return err
		}
	}
	// 0660 is the point: the serve user's group must be able to connect.
	if err := os.Chmod(path, 0o660); err != nil {
		_ = l.Close()
		return err
	}

	s.mu.Lock()
	s.listener = l
	s.mu.Unlock()

	return nil
}

// Serve accepts connections until ctx is canceled, then waits for open
// connections to finish their current call.
func (s *Server) Serve(ctx context.Context) error {
	s.mu.Lock()
	l := s.listener
	s.mu.Unlock()

	if l == nil {
		return errors.New("agentrpc: Listen not called")
	}

	stop := context.AfterFunc(ctx, func() { _ = l.Close() })
	defer stop()
	defer s.wg.Wait()

	for {
		conn, err := l.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}

			return err
		}

		select {
		case s.sem <- struct{}{}:
		default:
			// Too many open connections: drop instead of queueing (finding 5).
			s.log.WarnContext(ctx, "connection limit reached")

			_ = conn.Close()

			continue
		}

		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer func() { <-s.sem }()

			s.serveConn(ctx, conn)
		}()
	}
}

// PeerCred reads SO_PEERCRED from a Unix connection.
func PeerCred(c *net.UnixConn) (Caller, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return Caller{}, err
	}

	var (
		cred *syscall.Ucred
		serr error
	)

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

func (s *Server) serveConn(ctx context.Context, conn *net.UnixConn) {
	defer func() { _ = conn.Close() }()

	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()

	caller, err := PeerCred(conn)
	if err != nil {
		s.log.ErrorContext(ctx, "peercred failed", "err", err)
		return
	}

	if !s.auth(caller) {
		// Deliberately no reply: no protocol oracle for unauthorized peers.
		s.log.WarnContext(ctx, "peer rejected", "uid", caller.UID, "gid", caller.GID, "pid", caller.PID)
		return
	}

	for {
		if ctx.Err() != nil {
			return
		}

		_ = conn.SetReadDeadline(time.Now().Add(s.idleTimeout))

		body, err := readFrame(conn)
		if err != nil {
			if !errors.Is(err, io.EOF) && ctx.Err() == nil {
				s.log.WarnContext(ctx, "read failed", "uid", caller.UID, "err", err)
			}

			return
		}

		out, err := json.Marshal(s.dispatch(ctx, caller, body))
		if err != nil {
			out = []byte(`{"ok":false,"error":{"code":"internal","message":"internal error"}}`)
		}

		_ = conn.SetWriteDeadline(time.Now().Add(s.requestTimeout))
		if err := writeFrame(conn, out); err != nil {
			if errors.Is(err, ErrFrameTooLarge) {
				s.log.ErrorContext(ctx, "response too large", "uid", caller.UID)
			}

			return
		}
	}
}

func (s *Server) dispatch(ctx context.Context, c Caller, body []byte) (resp response) {
	var req request

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if err := dec.Decode(&req); err != nil || dec.More() {
		return failure(&Error{CodeBadRequest, "request is not a single valid request object"})
	}

	if req.RequestID != "" {
		if !reqid.Valid(req.RequestID) {
			return failure(&Error{CodeBadRequest, "request_id must be 32 lowercase hex characters"})
		}

		ctx = reqid.With(ctx, req.RequestID)
	}

	h, ok := s.ops[req.Op]
	if !ok {
		s.log.WarnContext(ctx, "unknown operation", "uid", c.UID, "op", req.Op)
		return failure(&Error{CodeUnknownOperation, "unknown operation"})
	}

	defer func() {
		if r := recover(); r != nil {
			s.log.ErrorContext(ctx, "handler panic", "op", req.Op, "panic", r)

			resp = failure(&Error{CodeInternal, "internal error"})
		}
	}()

	ctx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	out, err := h(ctx, c, req.Params)
	if err != nil {
		var e *Error
		if errors.As(err, &e) {
			s.log.InfoContext(ctx, "call failed", "uid", c.UID, "op", req.Op, "code", e.Code)
			return failure(e)
		}
		// Never send internal error text to the caller.
		s.log.ErrorContext(ctx, "call failed", "uid", c.UID, "op", req.Op, "err", err)

		return failure(&Error{CodeInternal, "internal error"})
	}

	raw, err := json.Marshal(out)
	if err != nil {
		s.log.ErrorContext(ctx, "encode failed", "op", req.Op, "err", err)
		return failure(&Error{CodeInternal, "internal error"})
	}

	s.log.InfoContext(ctx, "call ok", "uid", c.UID, "op", req.Op)

	return response{OK: true, Result: raw}
}

func failure(e *Error) response { return response{OK: false, Error: e} }
