// SPDX-License-Identifier: AGPL-3.0-or-later

package agent

import (
	"context"
	"errors"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/scanunit"
	"github.com/maki-it/hostashi/internal/sites"
)

var (
	errNoScan       = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "malware scans are not available on this server"}
	errEngineNotOn  = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "ClamAV is not installed; switch the malware scan on first"}
	errScanNotFound = &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: "unknown site"}
)

// scanSite looks up the account of a site for its scans.
func scanSite(m *sites.Manager) func(string) (scanunit.Site, error) {
	return func(user string) (scanunit.Site, error) {
		for _, s := range m.List() {
			if s.User != user {
				continue
			}

			home, ok := m.Home(user)
			if !ok {
				break
			}

			return scanunit.Site{Home: home, UID: s.UID, GID: s.GID}, nil
		}

		return scanunit.Site{}, sites.ErrNotFound
	}
}

// registerScan serves scan.engine.* and site.scan.* (ADR-0064). Start and
// the quarantine hold h.mu like site.deploy.start, so a site cannot be
// deleted meanwhile; the scan itself goes on in its unit.
func (h *siteOps) registerScan(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.ScanEngineStatusOp, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.ScanEngineStatusRequest) (agentapi.ScanEngineStatus, error) {
		if h.engine == nil {
			return agentapi.ScanEngineStatus{}, errNoScan
		}

		return h.engine.Status(ctx)
	})

	agentrpc.Handle(s, agentapi.ScanEngineSet, func(ctx context.Context, _ agentrpc.Caller, req agentapi.ScanEngineSetRequest) (agentapi.ScanEngineStatus, error) {
		if h.engine == nil {
			return agentapi.ScanEngineStatus{}, errNoScan
		}

		st, err := h.engine.Set(ctx, req)

		return st, mapScanError(err)
	})

	agentrpc.Handle(s, agentapi.ScanStart, func(ctx context.Context, _ agentrpc.Caller, req agentapi.ScanStartRequest) (agentapi.ScanStatus, error) {
		if h.scan == nil || h.engine == nil {
			return agentapi.ScanStatus{}, errNoScan
		}

		if !h.engine.On() {
			return agentapi.ScanStatus{}, errEngineNotOn
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		st, err := h.scan.Start(ctx, req)

		return st, mapScanError(err)
	})

	agentrpc.Handle(s, agentapi.ScanStatusOp, func(ctx context.Context, _ agentrpc.Caller, req agentapi.ScanStatusRequest) (agentapi.ScanStatus, error) {
		if h.scan == nil {
			return agentapi.ScanStatus{}, errNoScan
		}

		st, err := h.scan.Status(ctx, req)

		return st, mapScanError(err)
	})

	agentrpc.Handle(s, agentapi.ScanQuarantine, func(ctx context.Context, _ agentrpc.Caller, req agentapi.ScanQuarantineRequest) (agentapi.QuarantineItem, error) {
		if h.scan == nil {
			return agentapi.QuarantineItem{}, errNoScan
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		it, err := h.scan.Quarantine(ctx, req)

		return it, mapScanError(err)
	})

	agentrpc.Handle(s, agentapi.ScanRestore, func(ctx context.Context, _ agentrpc.Caller, req agentapi.ScanRestoreRequest) (agentapi.ScanRestored, error) {
		if h.scan == nil {
			return agentapi.ScanRestored{}, errNoScan
		}

		h.mu.Lock()
		defer h.mu.Unlock()

		res, err := h.scan.Restore(ctx, req)

		return res, mapScanError(err)
	})
}

func mapScanError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sites.ErrNotFound):
		return errScanNotFound
	case errors.Is(err, scanunit.ErrUnknownItem):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: err.Error()}
	case errors.Is(err, scanunit.ErrBusy), errors.Is(err, scanunit.ErrEngineBusy):
		return &agentrpc.Error{Code: agentrpc.CodeConflict, Message: err.Error()}
	case errors.Is(err, scanunit.ErrChanged), errors.Is(err, scanunit.ErrExists), errors.Is(err, scanunit.ErrNoParent), errors.Is(err, scanunit.ErrNotRegular):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
	}

	return err
}
