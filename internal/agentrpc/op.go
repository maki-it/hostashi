// SPDX-License-Identifier: AGPL-3.0-or-later

package agentrpc

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
)

// Validator is implemented by every request type. Validation is positive:
// anything not explicitly allowed is rejected.
type Validator interface{ Validate() error }

// Op ties an operation name to its request and response types. Server and
// client both use the same Op value, so a mismatch between name and types
// cannot compile (spike S1, finding 2).
type Op[Req Validator, Resp any] struct {
	Name string
}

var opNameRe = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z][a-z0-9]*)*$`)

// Handle registers the handler for op on s. It panics on an invalid or
// duplicate name, because that is a programming error found at startup.
func Handle[Req Validator, Resp any](s *Server, op Op[Req, Resp], fn func(ctx context.Context, c Caller, req Req) (Resp, error)) {
	if !opNameRe.MatchString(op.Name) {
		panic("agentrpc: invalid operation name " + op.Name)
	}

	if _, dup := s.ops[op.Name]; dup {
		panic("agentrpc: duplicate operation " + op.Name)
	}

	s.ops[op.Name] = func(ctx context.Context, c Caller, raw json.RawMessage) (any, error) {
		var req Req

		if len(raw) == 0 {
			raw = []byte("{}")
		}

		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()

		if err := dec.Decode(&req); err != nil {
			return nil, &Error{CodeBadRequest, "params do not match the operation"}
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

// Call sends req to the agent behind c and decodes the typed response.
// The request is validated locally first, so obviously bad input never
// leaves the web process. The agent validates again and is authoritative.
func (op Op[Req, Resp]) Call(ctx context.Context, c *Client, req Req) (Resp, error) {
	var zero Resp
	if err := req.Validate(); err != nil {
		return zero, &Error{CodeInvalidArgument, err.Error()}
	}

	raw, err := c.call(ctx, op.Name, req)
	if err != nil {
		return zero, err
	}

	var out Resp
	if err := json.Unmarshal(raw, &out); err != nil {
		return zero, err
	}

	return out, nil
}
