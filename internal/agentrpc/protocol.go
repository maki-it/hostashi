// SPDX-License-Identifier: AGPL-3.0-or-later

// Package agentrpc is the typed RPC between the unprivileged web process
// (hostashi serve) and the root agent (hostashi agent).
//
// Wire format: a 4-byte big-endian length prefix followed by a JSON body.
// Every operation is a named pair of Go types (see Op). There is no generic
// exec operation and there never will be one: the agent only offers what is
// registered explicitly.
package agentrpc

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxFrame is the largest accepted frame body. Requests are small typed structs.
const MaxFrame = 64 * 1024

// Error codes returned to clients. They are stable strings, not Go error texts.
const (
	CodeBadRequest       = "bad_request"
	CodeInvalidArgument  = "invalid_argument"
	CodeUnknownOperation = "unknown_operation"
	CodeNotFound         = "not_found"
	CodeConflict         = "conflict"
	CodePrecondition     = "failed_precondition"
	CodeInternal         = "internal"
)

// request is one call: an operation name plus its typed parameters.
type request struct {
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params"`
}

// response is the reply to exactly one request.
type response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is a failure that the caller may see. Message must never contain
// internal details such as file paths or Go error texts.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// ErrFrameTooLarge is returned when a frame exceeds MaxFrame.
var ErrFrameTooLarge = errors.New("agentrpc: frame too large")

// writeFrame writes a 4-byte big-endian length followed by the body.
func writeFrame(w io.Writer, body []byte) error {
	if len(body) > MaxFrame {
		return ErrFrameTooLarge
	}

	buf := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(buf, uint32(len(body))) //nolint:gosec // len(body) <= MaxFrame, checked above
	copy(buf[4:], body)
	_, err := w.Write(buf)

	return err
}

// readFrame reads one frame. The length is checked before any allocation.
func readFrame(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}

	n := binary.BigEndian.Uint32(hdr[:])
	if n > MaxFrame {
		return nil, fmt.Errorf("%w: %d bytes", ErrFrameTooLarge, n)
	}

	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}

	return body, nil
}
