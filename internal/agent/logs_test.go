// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/logrotate"
	"github.com/maki-it/hostashi/internal/sitelogs"
)

func TestLogsWithoutReader(t *testing.T) {
	c, _ := startSiteAgent(t)
	ctx := context.Background()

	_, err := agentapi.SiteLogsRead.Call(ctx, c, agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogAccess, Lines: 10})
	wantCode(t, err, agentrpc.CodePrecondition)

	_, err = agentapi.LogsRetentionSet.Call(ctx, c, agentapi.LogsRetentionSetRequest{Days: 7})
	wantCode(t, err, agentrpc.CodePrecondition)
}

func TestLogsOps(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	dir := t.TempDir()
	r := &sitelogs.Reader{SiteLogDir: filepath.Join(dir, "nginx"), PHPLogDir: filepath.Join(dir, "php")}
	rot := filepath.Join(dir, "logrotate")
	c, _ := startSiteAgentCfg(t, Config{Logs: r, LogRotate: rot})
	ctx := context.Background()

	req := agentapi.SiteLogsReadRequest{Site: "blog", Kind: agentapi.LogAccess, Lines: 10}

	_, err := agentapi.SiteLogsRead.Call(ctx, c, req)
	wantCode(t, err, agentrpc.CodeNotFound)

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	resp, err := agentapi.SiteLogsRead.Call(ctx, c, req)
	if err != nil || len(resp.Lines) != 0 {
		t.Fatalf("no log yet: %+v %v", resp, err)
	}

	if err := os.MkdirAll(filepath.Join(r.SiteLogDir, "blog"), 0o750); err != nil {
		t.Fatal(err)
	}

	p := filepath.Join(r.SiteLogDir, "blog", "access.log")
	if err := os.WriteFile(p, []byte("GET / 200\nGET /x 404\n"), 0o640); err != nil {
		t.Fatal(err)
	}

	resp, err = agentapi.SiteLogsRead.Call(ctx, c, req)
	if err != nil || strings.Join(resp.Lines, "|") != "GET / 200|GET /x 404" {
		t.Fatalf("tail: %+v %v", resp, err)
	}

	// A file the site could have put there is refused.
	if err := os.Chown(p, 1000, 1000); err != nil {
		t.Fatal(err)
	}

	_, err = agentapi.SiteLogsRead.Call(ctx, c, req)
	wantCode(t, err, agentrpc.CodePrecondition)

	for _, bad := range []agentapi.SiteLogsReadRequest{
		{Site: "blog", Kind: "../../etc/shadow", Lines: 10},
		{Site: "blog", Kind: agentapi.LogAccess, Lines: 5000},
	} {
		_, err = agentapi.SiteLogsRead.Call(ctx, c, bad)
		wantCode(t, err, agentrpc.CodeInvalidArgument)
	}

	// Retention.
	_, err = agentapi.LogsRetentionSet.Call(ctx, c, agentapi.LogsRetentionSetRequest{Days: 0})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	if _, err := agentapi.LogsRetentionSet.Call(ctx, c, agentapi.LogsRetentionSetRequest{Days: 30}); err != nil {
		t.Fatal(err)
	}

	b, err := os.ReadFile(rot)
	if d, ok := logrotate.Days(b); err != nil || !ok || d != 30 {
		t.Fatalf("logrotate file: %v %d %v\n%s", err, d, ok, b)
	}
}
