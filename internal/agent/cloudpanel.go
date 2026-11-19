// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/cpsource"
)

var errNoCloudPanel = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the CloudPanel import is not available on this server"}

// registerCloudPanel serves cloudpanel.source.* (ADR-0026). The private key
// stays in the agent; answers carry only the public key and fingerprints.
func registerCloudPanel(s *agentrpc.Server, src *cpsource.Source) {
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
