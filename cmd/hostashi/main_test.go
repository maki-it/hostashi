// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.sock")
	cases := []struct {
		name    string
		args    []string
		euid    int
		code    int
		wantErr string
	}{
		{"no args", nil, 1000, 2, "usage"},
		{"unknown command", []string{"exec"}, 1000, 2, "unknown command"},
		{"version", []string{"version"}, 1000, 0, ""},
		{"bad flag", []string{"serve", "-nope"}, 1000, 2, ""},
		{"extra args", []string{"agent", "sh"}, 0, 2, ""},
		{"serve as root", []string{"serve"}, 0, 1, "must not run as root"},
		{"serve public", []string{"serve", "-listen", "0.0.0.0:8080"}, 1000, 1, "only loopback"},
		{"agent as user", []string{"agent"}, 1000, 1, "must run as root"},
		{"agent root as serve user", []string{"agent", "-serve-user", "root"}, 0, 1, "dedicated unprivileged user"},
		{"ping without agent", []string{"ping", "-socket", missing}, 1000, 1, "unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(context.Background(), tc.args, tc.euid, &out, &errOut)
			if code != tc.code {
				t.Fatalf("exit %d, want %d\nstdout: %s\nstderr: %s", code, tc.code, &out, &errOut)
			}
			if tc.wantErr != "" && !strings.Contains(errOut.String(), tc.wantErr) {
				t.Fatalf("stderr %q does not contain %q", &errOut, tc.wantErr)
			}
		})
	}
}
