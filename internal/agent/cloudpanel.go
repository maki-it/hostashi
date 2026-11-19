// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/cpimport"
	"github.com/maki-it/hostashi/internal/cpsource"
)

var errNoCloudPanel = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the CloudPanel import is not available on this server"}

// registerCloudPanel serves cloudpanel.source.* (ADR-0026). The private key
// stays in the agent; answers carry only the public key and fingerprints.
func registerCloudPanel(s *agentrpc.Server, src *cpsource.Source, imp *cpimport.Agent) {
	agentrpc.Handle(s, agentapi.CloudPanelSourceGet, func(_ context.Context, _ agentrpc.Caller, _ agentapi.CloudPanelSourceGetRequest) (agentapi.CloudPanelSourceInfo, error) {
		if src == nil {
			return agentapi.CloudPanelSourceInfo{}, errNoCloudPanel
		}

		info, err := src.Get()

		return info, mapCloudPanelError(err)
	})

	agentrpc.Handle(s, agentapi.CloudPanelSourceSet, func(_ context.Context, _ agentrpc.Caller, req agentapi.CloudPanelSourceSetRequest) (agentapi.CloudPanelSourceInfo, error) {
		if src == nil {
			return agentapi.CloudPanelSourceInfo{}, errNoCloudPanel
		}

		info, err := src.Set(req.Source)

		return info, mapCloudPanelError(err)
	})

	agentrpc.Handle(s, agentapi.CloudPanelHostKey, func(_ context.Context, _ agentrpc.Caller, req agentapi.CloudPanelHostKeyRequest) (agentapi.CloudPanelSourceInfo, error) {
		if src == nil {
			return agentapi.CloudPanelSourceInfo{}, errNoCloudPanel
		}

		info, err := src.SetHostKey(req.Key)

		return info, mapCloudPanelError(err)
	})

	agentrpc.Handle(s, agentapi.CloudPanelSourceDelete, func(_ context.Context, _ agentrpc.Caller, _ agentapi.CloudPanelSourceDeleteRequest) (agentapi.CloudPanelSourceInfo, error) {
		if src == nil {
			return agentapi.CloudPanelSourceInfo{}, errNoCloudPanel
		}

		info, err := src.Delete()

		return info, mapCloudPanelError(err)
	})

	agentrpc.Handle(s, agentapi.CloudPanelSourceDB, func(_ context.Context, _ agentrpc.Caller, _ agentapi.CloudPanelDBRequest) (agentapi.CloudPanelDBCredentials, error) {
		if src == nil {
			return agentapi.CloudPanelDBCredentials{}, errNoCloudPanel
		}

		c, err := src.GenerateDB()

		return c, mapCloudPanelError(err)
	})

	agentrpc.Handle(s, agentapi.CloudPanelImportStart, func(ctx context.Context, _ agentrpc.Caller, req agentapi.CloudPanelImportRequest) (agentapi.CloudPanelImport, error) {
		if imp == nil {
			return agentapi.CloudPanelImport{}, errNoCloudPanel
		}

		r, err := imp.Start(ctx, req)

		return r, mapImportError(err)
	})

	agentrpc.Handle(s, agentapi.CloudPanelImportGet, func(_ context.Context, _ agentrpc.Caller, req agentapi.CloudPanelImportGetRequest) (agentapi.CloudPanelImport, error) {
		if imp == nil {
			return agentapi.CloudPanelImport{}, errNoCloudPanel
		}

		r, err := imp.Get(req.ID)

		return r, mapImportError(err)
	})
}

func mapImportError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, cpimport.ErrInvalid):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: err.Error()}
	case errors.Is(err, cpimport.ErrNotReady):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "set up the old server and confirm its host key first"}
	case errors.Is(err, cpimport.ErrNoDBUser):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
	case errors.Is(err, cpimport.ErrNoSite), errors.Is(err, cpimport.ErrNotFound), errors.Is(err, cpimport.ErrNoDatabase):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: err.Error()}
	case errors.Is(err, cpimport.ErrBusy):
		return &agentrpc.Error{Code: agentrpc.CodeConflict, Message: "an import is in progress"}
	default:
		return mapCloudPanelError(err)
	}
}

func mapCloudPanelError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, agentapi.ErrSourceInvalid):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: err.Error()}
	case errors.Is(err, cpsource.ErrBadHostKey):
		return &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: "host key must be one Ed25519, ECDSA or RSA (3072 bits or more) public key"}
	case errors.Is(err, cpsource.ErrNotSet), errors.Is(err, cpsource.ErrNotReady):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "set the old server's address first"}
	default:
		return err
	}
}
