// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// nginx reaches the panel through a Unix socket that systemd creates
// (hostashi-panel.socket) and hands over at start (ADR-0023). Only nginx's
// workers and the panel may connect to it, so its X-Real-IP header is the
// client's address. The TCP listener never trusts a forwarded header.

// listenFDsStart is the first file descriptor systemd passes (sd_listen_fds).
const listenFDsStart = 3

// systemdListener returns the socket systemd passed to this process, or nil
// when there is none.
func systemdListener() (net.Listener, error) {
	pid, fds := os.Getenv("LISTEN_PID"), os.Getenv("LISTEN_FDS")

	for _, k := range []string{"LISTEN_PID", "LISTEN_FDS", "LISTEN_FDNAMES"} {
		_ = os.Unsetenv(k)
	}

	if pid != strconv.Itoa(os.Getpid()) {
		return nil, nil //nolint:nilnil // no socket activation
	}

	n, err := strconv.Atoi(fds)
	if err != nil || n < 1 {
		return nil, nil //nolint:nilnil // no socket activation
	}

	if n != 1 {
		return nil, fmt.Errorf("systemd passed %d sockets, want 1 (hostashi-panel.socket)", n)
	}

	f := os.NewFile(listenFDsStart, "hostashi-panel.sock")
	defer func() { _ = f.Close() }()

	ln, err := net.FileListener(f)
	if err != nil {
		return nil, fmt.Errorf("socket from systemd: %w", err)
	}

	if ln.Addr().Network() != "unix" {
		_ = ln.Close()

		return nil, errors.New("socket from systemd is not a Unix socket")
	}

	return ln, nil
}

type clientIPKey struct{}

// fromProxy takes the client's address from nginx's X-Real-IP. A request
// without a valid one did not come through nginx's panel server.
func fromProxy(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := net.ParseIP(r.Header.Get("X-Real-IP"))
		if ip == nil {
			http.Error(w, "missing client address", http.StatusBadRequest)

			return
		}

		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), clientIPKey{}, ip.String())))
	})
}

// PanelDomain returns the host name of publicURL that nginx serves the
// panel at, or "" for none: no URL, localhost or an IP address.
func PanelDomain(publicURL string) string {
	if publicURL == "" {
		return ""
	}

	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme != "https" {
		return ""
	}

	host := strings.ToLower(u.Hostname())
	if host == "localhost" || net.ParseIP(host) != nil || agentapi.ValidateDomain(host) != nil {
		return ""
	}

	return host
}
