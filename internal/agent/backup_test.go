// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/backup"
)

type countingUnits struct{ started int }

func (u *countingUnits) StartRun(context.Context) error             { u.started++; return nil }
func (u *countingUnits) Reload(context.Context) error               { return nil }
func (u *countingUnits) EnableTimer(context.Context) error          { return nil }
func (u *countingUnits) StartRestore(context.Context, string) error { return nil }
func (u *countingUnits) DisableTimer(context.Context) error         { return nil }

func code(err error) string {
	var e *agentrpc.Error
	if errors.As(err, &e) {
		return e.Code
	}

	return ""
}

func TestBackupOperationsThroughServer(t *testing.T) {
	ctx := context.Background()

	// Without a backup side every operation is a precondition failure.
	c0, _ := startSiteAgentCfg(t, Config{})
	if _, err := agentapi.BackupGet.Call(ctx, c0, agentapi.BackupGetRequest{}); code(err) != agentrpc.CodePrecondition {
		t.Fatalf("no backup side: %v", err)
	}

	dir := t.TempDir()
	status := filepath.Join(dir, "status")
	_ = os.Mkdir(status, 0o700)
	units := &countingUnits{}
	b := &backup.Agent{ConfigDir: filepath.Join(dir, "config"), StatusDir: status, DropIn: filepath.Join(dir, "window.conf"), Units: units}
	c, _ := startSiteAgentCfg(t, Config{Backup: b})

	if _, err := agentapi.BackupRun.Call(ctx, c, agentapi.BackupRunRequest{}); code(err) != agentrpc.CodePrecondition {
		t.Fatalf("run before setup: %v", err)
	}

	s := agentapi.DefaultBackupSettings()
	s.Target = &agentapi.BackupTarget{User: "u1", Host: "u1.your-storagebox.de", Port: 23, Path: "hostashi"}

	info, err := agentapi.BackupSet.Call(ctx, c, agentapi.BackupSetRequest{Settings: s})
	if err != nil || !strings.HasPrefix(info.PublicKey, "ssh-ed25519 ") || info.Ready {
		t.Fatalf("set: %+v, %v", info, err)
	}

	// The request type refuses bad settings before the agent sees them.
	bad := s
	bad.Retention.Daily = 0

	if _, err := agentapi.BackupSet.Call(ctx, c, agentapi.BackupSetRequest{Settings: bad}); code(err) != agentrpc.CodeInvalidArgument {
		t.Errorf("bad settings: %v", err)
	}

	if _, err := agentapi.BackupHostKey.Call(ctx, c, agentapi.BackupHostKeyRequest{Key: "ssh-dss AAAA"}); code(err) != agentrpc.CodeInvalidArgument {
		t.Errorf("bad host key: %v", err)
	}

	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	k, _ := ssh.NewPublicKey(pub)

	info, err = agentapi.BackupHostKey.Call(ctx, c, agentapi.BackupHostKeyRequest{Key: string(ssh.MarshalAuthorizedKey(k))})
	if err != nil || !info.Ready || info.HostKey != ssh.FingerprintSHA256(k) {
		t.Fatalf("host key: %+v, %v", info, err)
	}

	if _, err := agentapi.BackupRun.Call(ctx, c, agentapi.BackupRunRequest{}); err != nil || units.started != 1 {
		t.Fatalf("run: %v, started %d", err, units.started)
	}

	st, err := agentapi.BackupStatusGet.Call(ctx, c, agentapi.BackupStatusRequest{})
	if err != nil || st.Running || st.Items == nil {
		t.Fatalf("status: %+v, %v", st, err)
	}

	if _, err := agentapi.BackupSnapshots.Call(ctx, c, agentapi.BackupSnapshotsRequest{Site: "../x"}); code(err) != agentrpc.CodeInvalidArgument {
		t.Errorf("bad site name: %v", err)
	}

	snaps, err := agentapi.BackupSnapshots.Call(ctx, c, agentapi.BackupSnapshotsRequest{Site: "blog"})
	if err != nil || snaps.Snapshots == nil {
		t.Errorf("snapshots: %+v, %v", snaps, err)
	}
}
