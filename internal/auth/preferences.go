// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/maki-it/hostashi/internal/store"
)

// ErrInvalidPreference is returned for a color scheme, theme or language
// outside the allowlist (ADR-0037, ADR-0071).
var ErrInvalidPreference = errors.New("color scheme must be system, light or dark, theme must be default, and language must be auto, en or de")

// Allowed values. A new theme is a new token file, a contrast check and an
// entry here and in the store's schema.
var (
	colorSchemes = []string{"system", "light", "dark"}
	themes       = []string{"default"}
	languages    = []string{"auto", "en", "de"}
)

// Preferences returns how the panel looks for the account and its language.
func (s *Service) Preferences(ctx context.Context, userID int64) (store.Preferences, error) {
	p, err := s.st.UserPreferences(ctx, userID)
	if err != nil {
		return store.Preferences{}, fmt.Errorf("auth: %w", err)
	}

	return p, nil
}

// SetPreferences changes the color scheme, theme and language of the
// account. An empty value keeps the stored one.
func (s *Service) SetPreferences(ctx context.Context, userID int64, colorScheme, theme, language string) (store.Preferences, error) {
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

	if language != "" {
		p.Language = language
	}

	if !slices.Contains(colorSchemes, p.ColorScheme) || !slices.Contains(themes, p.Theme) || !slices.Contains(languages, p.Language) {
		return store.Preferences{}, ErrInvalidPreference
	}

	if err := s.st.SetUserPreferences(ctx, userID, p); err != nil {
		return store.Preferences{}, fmt.Errorf("auth: %w", err)
	}

	return p, nil
}
