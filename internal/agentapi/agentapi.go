// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agentapi is the complete list of operations the agent offers.
// The web process and the CLI import it to call the agent, the agent imports
// it to register handlers. Adding an operation means adding it here, with a
// request type that validates its own input.
package agentapi

import "github.com/maki-it/hostashi/internal/agentrpc"

// DefaultSocket is where the agent listens on a production server.
// The directory is created by systemd (RuntimeDirectory=hostashi, mode 0750).
const DefaultSocket = "/run/hostashi/agent.sock"

// PingRequest has no parameters.
type PingRequest struct{}

// Validate accepts the empty request.
func (PingRequest) Validate() error { return nil }

// PingResponse reports what the agent knows about the caller and itself.
type PingResponse struct {
	CallerUID uint32 `json:"callerUid"` // as reported by the kernel
	Version   string `json:"version"`
}

// Ping checks that the agent is reachable and that the caller is accepted.
var Ping = agentrpc.Op[PingRequest, PingResponse]{Name: "ping"}
