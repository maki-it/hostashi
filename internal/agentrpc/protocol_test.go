// SPDX-License-Identifier: AGPL-3.0-or-later

package agentrpc

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, MaxFrame} {
		var buf bytes.Buffer

		body := bytes.Repeat([]byte("a"), n)
		if err := writeFrame(&buf, body); err != nil {
			t.Fatal(err)
		}

		got, err := readFrame(&buf)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("n=%d: %v", n, err)
		}
	}
}

func TestFrameLimits(t *testing.T) {
	if err := writeFrame(io.Discard, make([]byte, MaxFrame+1)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("write: %v", err)
	}

	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 0xFFFFFFFF)

	if _, err := readFrame(bytes.NewReader(hdr[:])); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("read: %v", err)
	}
}

func TestFrameTruncated(t *testing.T) {
	for _, in := range [][]byte{{}, {0, 0}, {0, 0, 0, 5, 'a', 'b'}} {
		if _, err := readFrame(bytes.NewReader(in)); err == nil {
			t.Fatalf("%v: want error", in)
		}
	}
}

func FuzzReadFrame(f *testing.F) {
	f.Add([]byte{0, 0, 0, 2, '{', '}'})
	f.Add([]byte{0xFF, 0xFF, 0xFF, 0xFF})
	f.Fuzz(func(t *testing.T, in []byte) {
		body, err := readFrame(bytes.NewReader(in))
		if err == nil && len(body) > MaxFrame {
			t.Fatalf("accepted %d bytes", len(body))
		}
	})
}

// FuzzDispatch feeds arbitrary request bodies to a server with the test
// operations. It must never panic and must always produce a response that
// is either OK or carries a known error code.
func FuzzDispatch(f *testing.F) {
	for _, s := range []string{
		`{"op":"who","params":{}}`,
		`{"op":"echo","params":{"text":"hi"}}`,
		`{"op":"echo","params":{"text":"hi","cmd":"id"}}`,
		`{"op":"boom"}`,
		`{"op":"exec","params":{"argv":["sh","-c","id"]}}`,
		`[]`, `null`, `{"op":null}`,
	} {
		f.Add([]byte(s))
	}

	s := NewServer(nil, Options{})
	Handle(s, opWho, func(_ context.Context, c Caller, _ emptyReq) (whoResp, error) { return whoResp{UID: c.UID}, nil })
	Handle(s, opEcho, func(_ context.Context, _ Caller, r echoReq) (echoResp, error) { return echoResp(r), nil })
	Handle(s, opBoom, func(context.Context, Caller, emptyReq) (whoResp, error) {
		return whoResp{}, errors.New("internal detail")
	})

	known := map[string]bool{CodeBadRequest: true, CodeInvalidArgument: true, CodeUnknownOperation: true, CodeInternal: true}

	f.Fuzz(func(t *testing.T, body []byte) {
		resp := s.dispatch(context.Background(), Caller{}, body)
		if resp.OK {
			return
		}

		if resp.Error == nil || !known[resp.Error.Code] {
			t.Fatalf("bad failure %+v", resp)
		}

		if bytes.Contains([]byte(resp.Error.Message), []byte("internal detail")) {
			t.Fatal("internal error text leaked")
		}
	})
}
