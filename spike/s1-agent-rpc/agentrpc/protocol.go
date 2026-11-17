// Package agentrpc is a throwaway prototype for spike S1 (see docs/06-roadmap.md).
// It is NOT product code. It tests whether a typed RPC over a Unix socket with
// peer credentials stays small enough to maintain by hand.
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
	CodeInvalidArgument  = "invalid_argument"
	CodeUnknownOperation = "unknown_operation"
	CodeBadRequest       = "bad_request"
	CodeInternal         = "internal"
	CodeForbidden        = "forbidden"
)

// Request is one call: an operation name plus its typed parameters.
type Request struct {
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params"`
}

// Response is the reply to exactly one Request.
type Response struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

// Error is a client-visible failure.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

var errFrameTooLarge = errors.New("frame too large")

// writeFrame writes a 4-byte big-endian length followed by the body.
func writeFrame(w io.Writer, body []byte) error {
	if len(body) > MaxFrame {
		return errFrameTooLarge
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(body)))
	buf := append(hdr[:], body...)
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
		return nil, fmt.Errorf("%w: %d bytes", errFrameTooLarge, n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
