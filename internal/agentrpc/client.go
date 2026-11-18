// SPDX-License-Identifier: AGPL-3.0-or-later

package agentrpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// DefaultTimeout bounds a call whose context has no deadline.
const DefaultTimeout = 15 * time.Second

// Client talks to the agent. It opens one connection per call, which keeps
// it safe for concurrent use and lets it survive agent restarts. The call
// rate is a handful per minute, so the cost does not matter.
type Client struct {
	path string
}

// NewClient returns a client for the socket at path. It does not connect.
func NewClient(path string) *Client { return &Client{path: path} }

// ErrUnavailable wraps failures to reach the agent at all.
var ErrUnavailable = errors.New("agentrpc: agent unavailable")

func (c *Client) roundTrip(ctx context.Context, op string, req any) (json.RawMessage, error) {
	params, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(request{Op: op, Params: params})
	if err != nil {
		return nil, err
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.path)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Now()) })
	defer stop()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if err := writeFrame(conn, body); err != nil {
		if errors.Is(err, ErrFrameTooLarge) {
			return nil, err
		}
		// The agent may close before reading, for example when the
		// connection limit is reached or the peer is not authorized.
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	rb, err := readFrame(conn)
	if err != nil {
		// The agent closes without a reply for unauthorized peers.
		return nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	var resp response
	if err := json.Unmarshal(rb, &resp); err != nil {
		return nil, fmt.Errorf("agentrpc: bad response: %w", err)
	}
	if !resp.OK {
		if resp.Error == nil {
			return nil, errors.New("agentrpc: failure without error")
		}
		return nil, resp.Error
	}
	return resp.Result, nil
}
