// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/maki-it/hostashi/internal/store"
)

// MetricsScope is the scope of the Prometheus endpoint (ADR-0022).
const MetricsScope = "metrics:read"

// ExpiryWarning is how long before its end a monitoring token is flagged
// (ADR-0022 [E]). A scrape that breaks silently is easy to miss, so tokens
// with MetricsScope get this warning.
const ExpiryWarning = 30 * 24 * time.Hour

// WarnExpiresSoon is the warning code of a token that ends within ExpiryWarning.
const WarnExpiresSoon = "expires_soon"

// TokenWarnings returns the warning codes for a token as it stands now.
func (s *Service) TokenWarnings(t store.APIToken) []string {
	if t.RevokedAt != nil || t.ExpiresAt == nil || !slices.Contains(t.Scopes, MetricsScope) {
		return nil
	}

	now := s.now()
	if t.ExpiresAt.After(now) && !t.ExpiresAt.After(now.Add(ExpiryWarning)) {
		return []string{WarnExpiresSoon}
	}

	return nil
}

// MetricsTokenExpiry returns the earliest end of an active token with
// MetricsScope. ok is false when there is none or none of them expires.
func (s *Service) MetricsTokenExpiry(ctx context.Context) (end time.Time, ok bool, err error) {
	ts, err := s.st.ActiveAPITokens(ctx, s.now())
	if err != nil {
		return time.Time{}, false, fmt.Errorf("auth: %w", err)
	}

	for _, t := range ts {
		if t.ExpiresAt == nil || !slices.Contains(t.Scopes, MetricsScope) {
			continue
		}

		if !ok || t.ExpiresAt.Before(end) {
			end, ok = *t.ExpiresAt, true
		}
	}

	return end, ok, nil
}
