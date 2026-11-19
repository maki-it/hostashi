// SPDX-License-Identifier: AGPL-3.0-or-later

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/cloudpanel/fixture"
	"github.com/maki-it/hostashi/internal/cloudpanel/scan"
)

func cloudpanelTree(t *testing.T) (string, string) {
	t.Helper()

	dir := filepath.Join(t.TempDir(), "srv")
	if _, err := fixture.Build(dir, time.Now()); err != nil {
		t.Fatal(err)
	}

	// The fixture is root-owned only when the test runs as root.
	return dir, strconv.FormatBool(os.Geteuid() == 0)
}

func scanJSON(t *testing.T, args ...string) *scan.Report {
	t.Helper()

	var out, errOut bytes.Buffer
	if code := run(context.Background(), args, 1000, &out, &errOut); code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, &errOut)
	}

	var rep scan.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatal(err)
	}

	return &rep
}

func customAndCron(rep *scan.Report) []string {
	var out []string

	for _, s := range rep.Sites {
		for _, c := range s.Custom {
			out = append(out, c.Raw)
		}

		for _, c := range s.Cron {
			out = append(out, c.Command)
		}
	}

	return out
}

func TestCloudPanelScanRedactsByDefault(t *testing.T) {
	dir, strict := cloudpanelTree(t)

	rep := scanJSON(t, "cloudpanel", "scan", "-root", dir, "-strict="+strict, "-json")
	if rep.Format != scan.Format || rep.Summary.Sites == 0 {
		t.Fatalf("format %q, %d sites", rep.Format, rep.Summary.Sites)
	}

	texts := customAndCron(rep)
	if len(texts) == 0 {
		t.Fatal("fixture has no custom snippets or cron jobs")
	}

	for _, s := range texts {
		if !strings.HasSuffix(s, "bytes hidden]") {
			t.Fatalf("not redacted: %q", s)
		}
	}

	for _, s := range customAndCron(scanJSON(t, "cloudpanel", "scan", "-root", dir, "-strict="+strict, "-json", "-full")) {
		if strings.HasSuffix(s, "bytes hidden]") {
			t.Fatalf("-full still redacts: %q", s)
		}
	}
}

func TestCloudPanelScanTable(t *testing.T) {
	dir, strict := cloudpanelTree(t)

	var out, errOut bytes.Buffer
	if code := run(context.Background(), []string{"cloudpanel", "scan", "-root", dir, "-strict=" + strict}, 1000, &out, &errOut); code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, &errOut)
	}

	if !strings.Contains(out.String(), "adoptable") || !strings.Contains(out.String(), "blocked") {
		t.Fatalf("unexpected table:\n%s", &out)
	}
}

func TestCloudPanelScanErrors(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "none")

	for _, tc := range []struct {
		args []string
		code int
	}{
		{[]string{"cloudpanel"}, 2},
		{[]string{"cloudpanel", "import"}, 2},
		{[]string{"cloudpanel", "scan", "extra"}, 2},
		{[]string{"cloudpanel", "scan", "-nope"}, 2},
		{[]string{"cloudpanel", "scan", "-root", missing}, 1},
		// A tree without /etc/nginx/sites-enabled is not a CloudPanel server.
		{[]string{"cloudpanel", "scan", "-root", t.TempDir()}, 1},
	} {
		var out, errOut bytes.Buffer
		if code := run(context.Background(), tc.args, 1000, &out, &errOut); code != tc.code {
			t.Errorf("%v: exit %d, want %d\nstderr: %s", tc.args, code, tc.code, &errOut)
		}
	}
}

func TestHomeSize(t *testing.T) {
	for want, u := range map[string]*scan.Usage{
		"-":       nil,
		"0 MiB":   {Bytes: 4096},
		"12 MiB":  {Bytes: 12 << 20},
		"2.5 GiB": {Bytes: 5 << 29},
	} {
		if got := homeSize(u); got != want {
			t.Errorf("homeSize(%+v) = %q, want %q", u, got, want)
		}
	}
}
