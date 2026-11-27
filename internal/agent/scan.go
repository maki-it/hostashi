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
	errRealtimeOff  = &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: "the real-time mode of the malware scan is off"}
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

// clamdServes reports whether scans go through the real-time mode's clamd:
// the mode is on and clamd runs or its socket starts it.
func clamdServes(e *scanunit.Engine) func(context.Context) bool {
	return func(ctx context.Context) bool {
		if !e.RealtimeOn() || e.Realtime == nil {
			return false
		}

		st, err := e.Realtime.State(ctx, scanunit.ClamdUnit)

		return err == nil && st != "failed"
	}
}

// memTotal is the server's RAM in bytes for the page's hint, 0 when it
// cannot be read.
func (h *siteOps) memTotal() uint64 {
	if h.host == nil {
		return 0
	}

	st, err := h.host.Read(nil)
	if err != nil {
		return 0
	}

	return st.Memory.TotalBytes
}

// registerScan serves scan.engine.* and site.scan.* (ADR-0064). Start and
// the quarantine hold h.mu like site.deploy.start, so a site cannot be
// deleted meanwhile; the scan itself goes on in its unit.
func (h *siteOps) registerScan(s *agentrpc.Server) {
	agentrpc.Handle(s, agentapi.ScanEngineStatusOp, func(ctx context.Context, _ agentrpc.Caller, _ agentapi.ScanEngineStatusRequest) (agentapi.ScanEngineStatus, error) {
		if h.engine == nil {
			return agentapi.ScanEngineStatus{}, errNoScan
		}

		st, err := h.engine.Status(ctx)
		if err == nil {
			st.MemTotal = h.memTotal()
		}

		return st, err
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

	agentrpc.Handle(s, agentapi.ScanRealtimePoll, func(ctx context.Context, _ agentrpc.Caller, req agentapi.ScanRealtimePollRequest) (agentapi.ScanRealtimeFindings, error) {
		if h.realtime == nil || h.engine == nil {
			return agentapi.ScanRealtimeFindings{}, errNoScan
		}

		if !h.engine.RealtimeOn() {
			return agentapi.ScanRealtimeFindings{}, errRealtimeOff
		}

		// The homes of the sites the panel names; unknown ones are skipped.
		lookup := scanSite(h.m)
		homes := make(map[string]string, len(req.Sites))
		list := make([]string, 0, len(req.Sites))

		for _, site := range req.Sites {
			if acct, err := lookup(site); err == nil {
				homes[site] = acct.Home
				list = append(list, acct.Home)
			}
		}

		active, err := h.realtime.Sync(ctx, list)
		if err != nil {
			return agentapi.ScanRealtimeFindings{}, err
		}

		out, err := h.realtime.Poll(homes, req.Cursor)
		out.Active = active

		return out, err
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
	case errors.Is(err, scanunit.ErrUnknownItem), errors.Is(err, scanunit.ErrTarget):
		return &agentrpc.Error{Code: agentrpc.CodeNotFound, Message: err.Error()}
	case errors.Is(err, scanunit.ErrEngineOff):
		return errEngineNotOn
	case errors.Is(err, scanunit.ErrBusy), errors.Is(err, scanunit.ErrEngineBusy):
		return &agentrpc.Error{Code: agentrpc.CodeConflict, Message: err.Error()}
	case errors.Is(err, scanunit.ErrChanged), errors.Is(err, scanunit.ErrExists), errors.Is(err, scanunit.ErrNoParent), errors.Is(err, scanunit.ErrNotRegular):
		return &agentrpc.Error{Code: agentrpc.CodePrecondition, Message: err.Error()}
	}

	return err
}
