// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestRun(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none.sock")
	home := t.TempDir()

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
		{"schema version", []string{"version", "-schema"}, 1000, 0, ""},
		{"version extra args", []string{"version", "x"}, 1000, 2, ""},
		{"bad flag", []string{"serve", "-nope"}, 1000, 2, ""},
		{"extra args", []string{"agent", "sh"}, 0, 2, ""},
		{"serve as root", []string{"serve"}, 0, 1, "must not run as root"},
		{"serve public", []string{"serve", "-listen", "0.0.0.0:8080"}, 1000, 1, "only loopback"},
		{"serve public metrics", []string{"serve", "-metrics-listen", "0.0.0.0:9471"}, 1000, 1, "only a loopback IP"},
		{"serve metrics by name", []string{"serve", "-metrics-listen", "localhost:9471"}, 1000, 1, "only a loopback IP"},
		{"agent as user", []string{"agent"}, 1000, 1, "must run as root"},
		{"agent root as serve user", []string{"agent", "-serve-user", "root"}, 0, 1, "dedicated unprivileged user"},
		{"files helper without home", []string{"files-helper"}, 1000, 2, ""},
		{"files helper as root", []string{"files-helper", "-home", "/"}, 0, 1, "must not run as root"},
		{"files helper without socket", []string{"files-helper", "-home", home}, 1000, 1, "hostashi-files@<site>.socket"},
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

func TestAdminUsage(t *testing.T) {
	for _, args := range [][]string{
		{"site"},
		{"site", "bogus"},
		{"site", "delete"},
		{"site", "files"},
		{"site", "files", "a", "b"},
		{"web"},
		{"web", "apply", "extra"},
		{"db"},
		{"db", "create", "shop"},
		{"db", "user"},
		{"db", "user", "create", "shop"},
		{"db", "list", "x"},
		{"pool"},
		{"pool", "add", "vol1"},
		{"pool", "add", "vol1", "/srv/vol1", "x"},
		{"pool", "remove"},
		{"pool", "list", "x"},
		{"service"},
		{"service", "nginx"},
		{"service", "list", "-yaml", "x"},
		{"service", "php", "blog", "reload", "now"},
		{"system"},
		{"system", "reboot"},
		{"system", "hostname"},
		{"system", "timezone", "UTC", "x"},
		{"system", "info", "x"},
	} {
		var out, errOut bytes.Buffer
		if code := run(t.Context(), args, 0, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
	}
}

func TestWebApplyRejectsUnknownFields(t *testing.T) {
	stdin = strings.NewReader(`{"sites":[{"user":"blog","domains":["a.example.com"],"root":"/etc"}]}`)

	t.Cleanup(func() { stdin = os.Stdin })

	var out, errOut bytes.Buffer
	if code := run(t.Context(), []string{"web", "-socket", "/nonexistent", "apply"}, 0, &out, &errOut); code != 1 {
		t.Fatalf("exit %d, want 1", code)
	}

	if !strings.Contains(errOut.String(), "unknown field") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestReadPassword(t *testing.T) {
	t.Cleanup(func() { stdin = os.Stdin })

	stdin = strings.NewReader("Secret-pass_12345\nignored\n")

	pw, gen, err := readPassword()
	if err != nil || gen || pw != "Secret-pass_12345" {
		t.Fatalf("got %q %v %v", pw, gen, err)
	}

	stdin = strings.NewReader("\n")

	pw, gen, err = readPassword()
	if err != nil || !gen || len(pw) != 32 {
		t.Fatalf("generated %q %v %v", pw, gen, err)
	}

	stdin = strings.NewReader("")

	if _, gen, err := readPassword(); err != nil || !gen {
		t.Fatalf("empty stdin: %v %v", gen, err)
	}
}

func TestPrintServices(t *testing.T) {
	var b bytes.Buffer

	printServices(&b, []agentapi.ServiceState{
		{ID: "nginx", Installed: true, Active: "active", Sub: "running", NRestarts: 2},
		{ID: "adminer"},
		{ID: "php", Site: "blog", Installed: true, Active: "failed"},
	})

	got := b.String()
	for _, want := range []string{"SERVICE", "nginx", "active (running)", "adminer", "not installed", "blog", "failed"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
}
