// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/filesunit"
)

var errNoFiles = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the file manager is not available on this server"}

// registerFiles serves files.open (ADR-0040). It holds h.mu like
// site.delete, so a deleted site's socket cannot be started again.
func (h *siteOps) registerFiles(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.FilesOpen, func(ctx context.Context, _ agentrpc.Caller, req agentapi.FilesOpenRequest) (agentapi.FilesOpenResponse, error) {
		if h.files == nil {
			return agentapi.FilesOpenResponse{}, errNoFiles
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		home, ok := h.m.Home(req.Site)
		if !ok {
			return agentapi.FilesOpenResponse{}, &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site " + req.Site}
		}

		sock, err := h.files.Open(ctx, filesunit.Site{Name: req.Site, Home: home, Slice: siteSlice(req.Site)})
		if errors.Is(err, filesunit.ErrInvalid) {
			return agentapi.FilesOpenResponse{}, &agentrpc.Error{Code: agentrpc.CodeInvalidArgument, Message: err.Error()}
		}

		if err != nil {
			return agentapi.FilesOpenResponse{}, err
		}

		return agentapi.FilesOpenResponse{Socket: sock}, nil
	})
}
