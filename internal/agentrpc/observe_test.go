// SPDX-License-Identifier: AGPL-3.0-or-later

package agentrpc

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestResult(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, "ok"},
		{&Error{Code: CodeNotFound}, CodeNotFound},
		{fmt.Errorf("wrapped: %w", &Error{Code: CodeConflict}), CodeConflict},
		{&Error{Code: "made_up_by_a_newer_agent"}, "error"},
		{fmt.Errorf("%w: dial", ErrUnavailable), "unavailable"},
		{errors.New("decode"), "error"},
	}

	for _, c := range cases {
		if got := Result(c.err); got != c.want {
			t.Errorf("%v: %q, want %q", c.err, got, c.want)
		}
	}
}

type observed struct {
	op, result string
	d          time.Duration
}

func TestClientObserve(t *testing.T) {
	_, c := start(t, allowSelf(), Options{})

	var got []observed

	c.Observe(func(op, result string, d time.Duration) { got = append(got, observed{op, result, d}) })

	ctx := context.Background()
	_, _ = opEcho.Call(ctx, c, echoReq{Text: "hi"})
	_, _ = opExec.Call(ctx, c, emptyReq{})
	// Stopped by client-side validation: never sent, so not observed.
	_, _ = opEcho.Call(ctx, c, echoReq{Text: ""})

	if len(got) != 2 || got[0].op != opEcho.Name || got[0].result != "ok" || got[1].op != opExec.Name || got[1].result != CodeUnknownOperation || got[0].d <= 0 {
		t.Fatalf("%+v", got)
	}

	down := NewClient(filepath.Join(t.TempDir(), "missing.sock"))
	down.Observe(func(op, result string, d time.Duration) { got = append(got, observed{op, result, d}) })
	_, _ = opEcho.Call(ctx, down, echoReq{Text: "hi"})

	if got[2].result != "unavailable" {
		t.Fatalf("%+v", got[2])
	}
}
