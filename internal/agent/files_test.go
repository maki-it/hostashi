// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/filesunit"
)

type filesCtl struct{ cronCtl }

func (c *filesCtl) Start(_ context.Context, u string) error   { return c.add("start " + u) }
func (c *filesCtl) Restart(_ context.Context, u string) error { return c.add("try-restart " + u) }
func (c *filesCtl) Stop(_ context.Context, u string) error    { return c.add("stop " + u) }

func TestFilesWithoutManager(t *testing.T) {
	c, _ := startSiteAgent(t)

	_, err := agentapi.FilesOpen.Call(context.Background(), c, agentapi.FilesOpenRequest{Site: "blog"})
	wantCode(t, err, agentrpc.CodePrecondition)
}

func TestFilesOpen(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}

	units := t.TempDir()
	ctl := &filesCtl{}
	c, _ := startSiteAgentCfg(t, Config{Files: &filesunit.Manager{UnitDir: units, Ctl: ctl}})
	ctx := context.Background()

	_, err := agentapi.FilesOpen.Call(ctx, c, agentapi.FilesOpenRequest{Site: "blog"})
	wantCode(t, err, agentrpc.CodeNotFound)

	_, err = agentapi.FilesOpen.Call(ctx, c, agentapi.FilesOpenRequest{Site: "../x"})
	wantCode(t, err, agentrpc.CodeInvalidArgument)

	if _, err := agentapi.SiteCreate.Call(ctx, c, agentapi.SiteCreateRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	r, err := agentapi.FilesOpen.Call(ctx, c, agentapi.FilesOpenRequest{Site: "blog"})
	if err != nil || r.Socket != "/run/hostashi-files/blog.sock" || !ctl.has("start hostashi-files@blog.socket") {
		t.Fatalf("open: %+v %v %q", r, err, ctl.calls)
	}

	dropIn := filepath.Join(units, "hostashi-files@blog.service.d", filesunit.DropInName)
	if _, err := os.Stat(dropIn); err != nil {
		t.Fatal(err)
	}

	// Deleting the site stops the socket and removes the drop-in.
	if _, err := agentapi.SiteDelete.Call(ctx, c, agentapi.SiteDeleteRequest{User: "blog"}); err != nil {
		t.Fatal(err)
	}

	if !ctl.has("stop hostashi-files@blog.socket") {
		t.Errorf("calls %q", ctl.calls)
	}

	if _, err := os.Stat(dropIn); !os.IsNotExist(err) {
		t.Errorf("drop-in left after site delete: %v", err)
	}
}
