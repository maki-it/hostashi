// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/maki-it/hostashi/internal/store"
)

// ErrInvalidPreference is returned for a color scheme or theme outside
// the allowlist (ADR-0037).
var ErrInvalidPreference = errors.New("color scheme must be system, light or dark, and theme must be default")

// Allowed values. A new theme is a new token file, a contrast check and an
// entry here and in the store's schema.
var (
	colorSchemes = []string{"system", "light", "dark"}
	themes       = []string{"default"}
)

// Preferences returns how the panel looks for the account.
func (s *Service) Preferences(ctx context.Context, userID int64) (store.Preferences, error) {
	p, err := s.st.UserPreferences(ctx, userID)
	if err != nil {
		return store.Preferences{}, fmt.Errorf("auth: %w", err)
	}

	return p, nil
}

// SetPreferences changes the color scheme and theme of the account. An
// empty value keeps the stored one.
func (s *Service) SetPreferences(ctx context.Context, userID int64, colorScheme, theme string) (store.Preferences, error) {
	p, err := s.Preferences(ctx, userID)
	if err != nil {
		return store.Preferences{}, err
	}

	if colorScheme != "" {
		p.ColorScheme = colorScheme
	}

	if theme != "" {
		p.Theme = theme
	}

	if !slices.Contains(colorSchemes, p.ColorScheme) || !slices.Contains(themes, p.Theme) {
		return store.Preferences{}, ErrInvalidPreference
	}

	if err := s.st.SetUserPreferences(ctx, userID, p); err != nil {
		return store.Preferences{}, fmt.Errorf("auth: %w", err)
	}

	return p, nil
}
