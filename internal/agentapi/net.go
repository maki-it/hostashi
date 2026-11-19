// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import "github.com/maki-it/hostashi/internal/agentrpc"

// ServerAddrsRequest has no parameters.
type ServerAddrsRequest struct{}

// Validate accepts the empty request.
func (ServerAddrsRequest) Validate() error { return nil }

// ServerAddrsResponse lists the server's public addresses.
type ServerAddrsResponse struct {
	// Addrs are the global unicast addresses of the server's interfaces
	// that are not private (RFC 1918, RFC 4193), sorted.
	Addrs []string `json:"addrs"`
}

// ServerAddrs returns the addresses a domain must point to so that it
// reaches this server (ADR-0031). The agent reads them; the web process
// has no netlink socket.
var ServerAddrs = agentrpc.Op[ServerAddrsRequest, ServerAddrsResponse]{Name: "net.addrs"}
