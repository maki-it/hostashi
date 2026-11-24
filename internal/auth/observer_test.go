// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"bytes"
	"context"
	"log/slog"
	"regexp"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/reqid"
)

type countObs struct{ fail, throttled map[string]int }

func (c *countObs) Failure(k string)   { c.fail[k]++ }
func (c *countObs) Throttled(k string) { c.throttled[k]++ }

func TestObserverSeesKindsOnly(t *testing.T) {
	e := newEnv(t)
	obs := &countObs{fail: map[string]int{}, throttled: map[string]int{}}
	e.svc.obs = obs
	e.setup(t)

	ctx := context.Background()

	_, _, _ = e.svc.Login(ctx, "kim", "wrong password!!", "", "6.6.6.6")
	_, _, _ = e.svc.Login(ctx, "nobody", "wrong password!!", "", "6.6.6.6")

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "7.7.7.7"); err != nil {
		t.Fatal(err)
	}

	_, _ = e.svc.AuthenticateToken(ctx, "hst_nothing", "8.8.8.8")
	_, _ = e.svc.AuthenticateToken(ctx, "no prefix", "8.8.8.8")

	if obs.fail[KindPassword] != 2 || obs.fail[KindToken] != 2 || len(obs.throttled) != 0 {
		t.Fatalf("%+v", obs)
	}

	for range 10 {
		_, _, _ = e.svc.Login(ctx, "kim", "wrong password!!", "", "9.9.9.9")
	}

	_, _, _ = e.svc.Login(ctx, "kim", testPassword, "", "9.9.9.9")

	if obs.throttled[KindPassword] != 1 {
		t.Fatalf("%+v", obs)
	}
}

// failLine is the contract with fail2ban's hostashi-panel filter (ADR-0021).
// Change it only together with packaging's filter.
var failLine = regexp.MustCompile(`^\{"time":"[^"]+","level":"WARN","msg":"auth\.failed","kind":"(password|totp|passkey|token|webhook)","ip":"([0-9a-f.:]+)"(,"request_id":"[0-9a-f]+")?\}$`)

func TestFailedLoginLogLine(t *testing.T) {
	e := newEnv(t)
	e.setup(t)

	var buf bytes.Buffer

	e.svc.flog = slog.New(slog.NewJSONHandler(&buf, nil))

	ctx := context.Background()

	_, _, _ = e.svc.Login(ctx, "kim", "wrong password!!", "", "203.0.113.7")
	// A user name that tries to look like another address field.
	_, _, _ = e.svc.Login(ctx, `x","ip":"198.51.100.1"}`, "wrong password!!", "", "203.0.113.8")
	_, _, _ = e.svc.Login(ctx, "kim", "wrong password!!", "", "::ffff:192.0.2.9")
	_, _, _ = e.svc.Login(ctx, "kim", "wrong password!!", "", "fe80::1%eth0")
	// No parseable address: counted, but no line fail2ban could act on.
	_, _, _ = e.svc.Login(ctx, "kim", "wrong password!!", "", "not an ip")
	_, _, _ = e.svc.Login(ctx, "kim", "wrong password!!", "", "")
	_, _ = e.svc.AuthenticateToken(ctx, "hst_nothing", "2001:db8::1")

	// Inside a request the logger adds the request ID at the end.
	e.svc.flog = slog.New(reqid.Handler(slog.NewJSONHandler(&buf, nil)))
	_, _ = e.svc.AuthenticateToken(reqid.With(ctx, reqid.New()), "hst_nothing", "203.0.113.9")
	e.svc.WebhookFailed(reqid.With(ctx, reqid.New()), "198.51.100.20")

	if _, _, err := e.svc.Login(ctx, "kim", testPassword, "", "192.0.2.50"); err != nil {
		t.Fatal(err)
	}

	want := [][2]string{
		{"password", "203.0.113.7"},
		{"password", "203.0.113.8"},
		{"password", "192.0.2.9"},
		{"password", "fe80::1"},
		{"token", "2001:db8::1"},
		{"token", "203.0.113.9"},
		{"webhook", "198.51.100.20"},
	}

	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(lines), len(want), buf.String())
	}

	for i, l := range lines {
		m := failLine.FindStringSubmatch(l)
		if m == nil || m[1] != want[i][0] || m[2] != want[i][1] {
			t.Errorf("line %d %q, want kind %s ip %s", i, l, want[i][0], want[i][1])
		}

		if strings.Contains(l, "kim") || strings.Contains(l, "198.51.100.1") || strings.Contains(l, "hst_") {
			t.Errorf("line %d leaks a user name or token: %q", i, l)
		}
	}
}
