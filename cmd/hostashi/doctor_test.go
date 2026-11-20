// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
)

func doctorAgent(t *testing.T, r agentapi.DoctorReport) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "doc")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	sock := filepath.Join(dir, "a.sock")
	s := agentrpc.NewServer(agentrpc.AllowUIDs(uint32(os.Getuid())), agentrpc.Options{})
	agentrpc.Handle(s, agentapi.Doctor, func(context.Context, agentrpc.Caller, agentapi.DoctorRequest) (agentapi.DoctorReport, error) {
		return r, nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := s.Listen(ctx, sock, -1); err != nil {
		t.Fatal(err)
	}

	go func() { _ = s.Serve(ctx) }()

	return sock
}

var sample = agentapi.DoctorReport{Checks: []agentapi.DoctorCheck{
	{ID: "system.os", Status: agentapi.DoctorOK, Summary: "Debian GNU/Linux 13 (trixie) is supported"},
	{ID: "system.quota", Status: agentapi.DoctorWarn, Summary: "/home: no project quota", Hint: "Run the installer again."},
	{ID: "site.disk", Site: "shop", Status: agentapi.DoctorOK, Summary: "disk space: 1.0 GiB of 10.0 GiB (10 %)"},
	{ID: "site.limits", Site: "shop", Status: agentapi.DoctorWarn, Summary: "no CPU, memory and process limits"},
}}

func TestDoctorText(t *testing.T) {
	var out, errOut bytes.Buffer

	code := doctorCmd(context.Background(), agentrpc.NewClient(doctorAgent(t, sample)), false, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit %d with warnings only, stderr %q", code, errOut.String())
	}

	got := out.String()
	for _, want := range []string{
		"system\n  ok    system.os        Debian GNU/Linux 13 (trixie) is supported\n",
		"  WARN  system.quota     /home: no project quota\n                         Run the installer again.\n",
		"\nsite shop\n  ok    site.disk        disk space",
		"\n2 ok, 2 warnings, 0 failed\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q:\n%s", want, got)
		}
	}
}

func TestDoctorJSONAndFailure(t *testing.T) {
	r := sample
	r.Checks = append(append([]agentapi.DoctorCheck{}, r.Checks...), agentapi.DoctorCheck{ID: "service.nginx", Status: agentapi.DoctorFail, Summary: "nginx does not answer"})

	var out, errOut bytes.Buffer
	if code := doctorCmd(context.Background(), agentrpc.NewClient(doctorAgent(t, r)), true, &out, &errOut); code != exitDoctorFailed {
		t.Fatalf("exit %d, want %d", code, exitDoctorFailed)
	}

	var got agentapi.DoctorReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil || len(got.Checks) != 5 || got.Checks[4].Status != agentapi.DoctorFail {
		t.Fatalf("json = %s, %v", out.String(), err)
	}
}

func TestDoctorNoAgent(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := doctorCmd(context.Background(), agentrpc.NewClient(filepath.Join(t.TempDir(), "none")), false, &out, &errOut); code != 1 {
		t.Fatalf("exit %d", code)
	}

	if !strings.Contains(errOut.String(), "agent does not answer") {
		t.Errorf("stderr %q", errOut.String())
	}
}
