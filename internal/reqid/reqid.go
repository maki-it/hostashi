// SPDX-License-Identifier: AGPL-3.0-or-later

// Package reqid carries one random ID per panel request through the panel
// and the agent, so a log line in either journal can be tied to the request
// that caused it (ADR-0022, option E). The ID is for logs only and never
// drives a decision.
package reqid

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
)

// Len is the length of an ID: 16 random bytes in lowercase hex.
const Len = 32

// LogKey is the attribute name in log lines.
const LogKey = "request_id"

// New returns a fresh random ID.
func New() string {
	var b [Len / 2]byte

	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails

	return hex.EncodeToString(b[:])
}

// Valid reports whether s has the exact form New produces.
func Valid(s string) bool {
	if len(s) != Len {
		return false
	}

	for i := range len(s) {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}

	return true
}

type ctxKey struct{}

// With returns ctx carrying id. An invalid id is dropped.
func With(ctx context.Context, id string) context.Context {
	if !Valid(id) {
		return ctx
	}

	return context.WithValue(ctx, ctxKey{}, id)
}

// From returns the ID in ctx, or "".
func From(ctx context.Context) string {
	id, _ := ctx.Value(ctxKey{}).(string)

	return id
}

// Handler adds the request ID of the record's context to every log line.
func Handler(h slog.Handler) slog.Handler { return handler{h} }

type handler struct{ next slog.Handler }

func (h handler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h handler) Handle(ctx context.Context, r slog.Record) error {
	if id := From(ctx); id != "" {
		r = r.Clone()
		r.AddAttrs(slog.String(LogKey, id))
	}

	return h.next.Handle(ctx, r)
}

func (h handler) WithAttrs(as []slog.Attr) slog.Handler { return handler{h.next.WithAttrs(as)} }
func (h handler) WithGroup(name string) slog.Handler    { return handler{h.next.WithGroup(name)} }
