// SPDX-License-Identifier: AGPL-3.0-or-later

package files

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

// DefaultIdle is how long the helper waits for a request before it exits;
// systemd starts it again on the next connection (ADR-0040).
const DefaultIdle = 5 * time.Minute

// listenFDsStart is the first file descriptor systemd passes (sd_listen_fds).
const listenFDsStart = 3

// SystemdListener returns the one Unix socket systemd passed to this process
// (hostashi-files@<site>.socket).
func SystemdListener() (net.Listener, error) {
	pid, fds := os.Getenv("LISTEN_PID"), os.Getenv("LISTEN_FDS")

	for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		_ = os.Unsetenv(k)
	}

	if pid != strconv.Itoa(os.Getpid()) || fds != "1" {
		return nil, errors.New("files: start through hostashi-files@<site>.socket (systemd must pass exactly one socket)")
	}

	f := os.NewFile(listenFDsStart, "hostashi-files.sock")
	defer func() { _ = f.Close() }()

	ln, err := net.FileListener(f)
	if err != nil {
		return nil, fmt.Errorf("files: socket from systemd: %w", err)
	}

	if ln.Addr().Network() != "unix" {
		_ = ln.Close()

		return nil, errors.New("files: socket from systemd is not a Unix socket")
	}

	return ln, nil
}

// idleTracker counts running requests and remembers the last one.
type idleTracker struct {
	mu     sync.Mutex
	active int
	last   time.Time
	now    func() time.Time
}

func (t *idleTracker) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.mu.Lock()
		t.active++
		t.mu.Unlock()

		defer func() {
			t.mu.Lock()
			t.active--
			t.last = t.now()
			t.mu.Unlock()
		}()

		next.ServeHTTP(w, r)
	})
}

func (t *idleTracker) idleFor() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.active > 0 {
		return 0
	}

	return t.now().Sub(t.last)
}

// Serve answers requests on ln until ctx ends or no request came for idle.
// A request that runs longer than idle (a large download) keeps it alive.
func Serve(ctx context.Context, ln net.Listener, h http.Handler, idle time.Duration, log *slog.Logger) error {
	if idle <= 0 {
		idle = DefaultIdle
	}

	t := &idleTracker{now: time.Now}
	t.last = t.now()

	srv := &http.Server{
		Handler:           t.wrap(h),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       idle,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelInfo),
	}

	done := make(chan error, 1)

	go func() { done <- srv.Serve(ln) }()

	tick := time.NewTicker(min(idle/4, 10*time.Second))
	defer tick.Stop()

	for {
		select {
		case err := <-done:
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}

			return fmt.Errorf("files: serve: %w", err)
		case <-ctx.Done():
			return shutdown(ctx, srv, done)
		case <-tick.C:
			if t.idleFor() >= idle {
				log.InfoContext(ctx, "files helper idle, exiting", "idle", idle.String())

				return shutdown(ctx, srv, done)
			}
		}
	}
}

func shutdown(ctx context.Context, srv *http.Server, done <-chan error) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		return fmt.Errorf("files: shutdown: %w", err)
	}

	if err := <-done; err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("files: serve: %w", err)
	}

	return nil
}
