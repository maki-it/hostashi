// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/backup"
	"github.com/maki-it/hostashi/internal/resticctl"
)

// openBackup builds the backup side with the fixed paths of a server.
func openBackup() *backup.Agent {
	h, _ := os.Hostname()

	return &backup.Agent{Units: resticctl.Units{}, Host: strings.ToLower(h)}
}

var errNoBackup = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "backups are not available on this server"}

func registerBackup(s *agentrpc.Server, b *backup.Agent) {
	agentrpc.Handle(s, agentapi.BackupGet, func(_ context.Context, _ agentrpc.Caller, _ agentapi.BackupGetRequest) (agentapi.BackupInfo, error) {
		if b == nil {
			return agentapi.BackupInfo{}, errNoBackup
		}

		info, err := b.Get()

		return info, mapBackupError(err)
	})

	agentrpc.Handle(s, agentapi.BackupSet, func(ctx context.Context, _ agentrpc.Caller, req agentapi.BackupSetRequest) (agentapi.BackupInfo, error) {
		if b == nil {
			return agentapi.BackupInfo{}, errNoBackup
		}

		info, err := b.Set(ctx, req.Settings)

		return info, mapBackupError(err)
	})

	agentrpc.Handle(s, agentapi.BackupHostKey, func(ctx context.Context, _ agentrpc.Caller, req agentapi.BackupHostKeyRequest) (agentapi.BackupInfo, error) {
		if b == nil {
			return agentapi.BackupInfo{}, errNoBackup
		}

		info, err := b.SetHostKey(ctx, req.Key)

		return info, mapBackupError(err)
	})

	agentrpc.Handle(s, agentapi.BackupRun, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.BackupRunRequest) (agentapi.BackupRunResponse, error) {
		if b == nil {
			return agentapi.BackupRunResponse{}, errNoBackup
		}

		return agentapi.BackupRunResponse{}, mapBackupError(b.StartRun(ctx))
	})

	agentrpc.Handle(s, agentapi.BackupStatusGet, func(_ context.Context, _ agentrpc.Caller, _ agentapi.BackupStatusRequest) (agentapi.BackupStatus, error) {
		if b == nil {
			return agentapi.BackupStatus{}, errNoBackup
		}

		st, err := b.Status()

		return st, mapBackupError(err)
	})

	agentrpc.Handle(s, agentapi.BackupSnapshots, func(_ context.Context, _ agentrpc.Caller, req agentapi.BackupSnapshotsRequest) (agentapi.BackupSnapshotsResponse, error) {
		if b == nil {
			return agentapi.BackupSnapshotsResponse{}, errNoBackup
		}

		snaps, err := b.Snapshots(req.Site)

		return agentapi.BackupSnapshotsResponse{Snapshots: snaps}, mapBackupError(err)
	})
}

// mapBackupError gives the caller fixed messages; details stay in the log.
func mapBackupError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, backup.ErrInvalid):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: err.Error()}
	case errors.Is(err, backup.ErrBadHostKey):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "host key must be one Ed25519, ECDSA or RSA (3072 bits or more) public key"}
	case errors.Is(err, backup.ErrNotReady):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "set a backup target and confirm its host key first"}
	case errors.Is(err, backup.ErrRunning):
		return &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "a backup run is in progress"}
	default:
		return err
	}
}
