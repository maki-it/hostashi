package agentrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

// Client is a synchronous, single-connection client (not safe for concurrent use).
type Client struct{ conn net.Conn }

func Dial(path string) (*Client, error) {
	c, err := net.DialTimeout("unix", path, 5*time.Second)
	if err != nil {
		return nil, err
	}
	return &Client{conn: c}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

// Call is the typed client side: request and response types are checked at
// compile time at the call site.
func Call[Req, Resp any](c *Client, op string, req Req) (Resp, error) {
	var zero Resp
	params, err := json.Marshal(req)
	if err != nil {
		return zero, err
	}
	body, err := json.Marshal(Request{Op: op, Params: params})
	if err != nil {
		return zero, err
	}
	_ = c.conn.SetDeadline(time.Now().Add(15 * time.Second))
	if err := writeFrame(c.conn, body); err != nil {
		return zero, err
	}
	rb, err := readFrame(c.conn)
	if err != nil {
		return zero, err
	}
	var resp Response
	if err := json.Unmarshal(rb, &resp); err != nil {
		return zero, fmt.Errorf("bad response: %w", err)
	}
	if !resp.OK {
		if resp.Error == nil {
			return zero, errors.New("agent returned failure without error")
		}
		return zero, resp.Error
	}
	var out Resp
	if err := json.Unmarshal(resp.Result, &out); err != nil {
		return zero, fmt.Errorf("bad result: %w", err)
	}
	return out, nil
}
