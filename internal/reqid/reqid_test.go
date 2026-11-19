// SPDX-License-Identifier: AGPL-3.0-or-later

package reqid

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestNewIsValidAndUnique(t *testing.T) {
	seen := map[string]bool{}

	for range 100 {
		id := New()
		if !Valid(id) || seen[id] {
			t.Fatalf("%q", id)
		}

		seen[id] = true
	}
}

func TestValid(t *testing.T) {
	bad := []string{"", "abc", strings.Repeat("A", Len), strings.Repeat("g", Len), strings.Repeat("0", Len-1), strings.Repeat("0", Len+1), strings.Repeat("0", Len-1) + "\n", strings.Repeat("0", Len-2) + "\"}"}
	for _, s := range bad {
		if Valid(s) {
			t.Errorf("%q accepted", s)
		}
	}

	if !Valid(strings.Repeat("0a", Len/2)) {
		t.Fatal("valid id rejected")
	}
}

func TestContext(t *testing.T) {
	ctx := context.Background()
	if From(ctx) != "" || From(With(ctx, "forged\nline")) != "" {
		t.Fatal("invalid id kept")
	}

	id := New()
	if From(With(ctx, id)) != id {
		t.Fatal("id lost")
	}
}

func TestHandler(t *testing.T) {
	var buf bytes.Buffer

	log := slog.New(Handler(slog.NewJSONHandler(&buf, nil))).With("component", "test")
	id := New()

	log.InfoContext(With(context.Background(), id), "with id", "k", "v")
	log.InfoContext(context.Background(), "without id")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatal(buf.String())
	}

	var first, second map[string]any
	if json.Unmarshal([]byte(lines[0]), &first) != nil || json.Unmarshal([]byte(lines[1]), &second) != nil {
		t.Fatal(buf.String())
	}

	if first[LogKey] != id || first["component"] != "test" || first["k"] != "v" {
		t.Fatalf("%v", first)
	}

	if _, ok := second[LogKey]; ok {
		t.Fatalf("%v", second)
	}
}
