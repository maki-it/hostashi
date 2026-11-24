// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// ErrNoPermissionsReset means the panel runs without an agent that can
// reset permissions.
var ErrNoPermissionsReset = errors.New("hosting: permission reset unavailable")

// PermissionsAgent resets the file permissions of a site (ADR-0067).
type PermissionsAgent interface {
	SitePermissionsReset(ctx context.Context, user string) (agentapi.SitePermissionsResetResponse, error)
}

// SitePermissionsReset calls site.permissions.reset.
func (a RPCAgent) SitePermissionsReset(ctx context.Context, user string) (agentapi.SitePermissionsResetResponse, error) {
	return agentapi.SitePermissionsReset.Call(ctx, a.C, agentapi.SitePermissionsResetRequest{Site: user})
}

// ResetPermissions gives every file of a site back to its Linux user and
// sets Hostashi's default modes (ADR-0067). The audit entry records the
// counts, also when the agent fails part way.
func (s *Service) ResetPermissions(ctx context.Context, actor int64, name string) (agentapi.SitePermissionsResetResponse, error) {
	pa, ok := s.ag.(PermissionsAgent)
	if !ok {
		return agentapi.SitePermissionsResetResponse{}, ErrNoPermissionsReset
	}

	site, err := s.Get(ctx, name)
	if err != nil {
		return agentapi.SitePermissionsResetResponse{}, err
	}

	if err := needAccount(site); err != nil {
		return agentapi.SitePermissionsResetResponse{}, err
	}

	if site.State != store.SiteActive {
		return agentapi.SitePermissionsResetResponse{}, ErrState
	}

	res, err := pa.SitePermissionsReset(ctx, site.Name)

	detail := map[string]string{
		"checked": strconv.FormatInt(res.Checked, 10),
		"changed": strconv.FormatInt(res.Changed, 10),
		"skipped": strconv.FormatInt(res.Skipped, 10),
	}

	if err != nil {
		detail["error"] = "failed"
		s.audit(ctx, actor, "site.permissions.reset", name, detail)

		return agentapi.SitePermissionsResetResponse{}, err
	}

	s.audit(ctx, actor, "site.permissions.reset", name, detail)

	return res, nil
}
