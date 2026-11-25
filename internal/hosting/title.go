// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/maki-it/hostashi/internal/store"
)

// MaxTitleLen is the longest display name of a site in characters
// (ADR-0072); migration 0065 holds the same limit.
const MaxTitleLen = 64

// NormalizeTitle checks a display name of a site and returns it trimmed.
// The title is free text but display text only: no control characters, no
// line breaks and no format characters such as bidirectional overrides,
// which could make one site look like another in a list.
func NormalizeTitle(title string) (string, error) {
	if !utf8.ValidString(title) {
		return "", fmt.Errorf("%w: the display name is not valid UTF-8", ErrInvalid)
	}

	t := strings.TrimSpace(title)
	if utf8.RuneCountInString(t) > MaxTitleLen {
		return "", fmt.Errorf("%w: the display name has more than %d characters", ErrInvalid, MaxTitleLen)
	}

	for _, r := range t {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || r == ' ' || r == ' ' {
			return "", fmt.Errorf("%w: the display name contains a control character or line break", ErrInvalid)
		}
	}

	return t, nil
}

// SetTitle sets or clears (empty) the display name of a site (ADR-0072).
// Only the panel's database changes; the system name and everything on
// the server stay as they are.
func (s *Service) SetTitle(ctx context.Context, actor int64, name, title string) (store.Site, error) {
	t, err := NormalizeTitle(title)
	if err != nil {
		return store.Site{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State == store.SiteDeleting {
		return store.Site{}, ErrState
	}

	if site.Title == t {
		return site, nil
	}

	old, err := s.st.SetSiteTitle(ctx, name, t, s.now())
	if err != nil {
		return store.Site{}, err
	}

	s.auditChange(ctx, actor, "site.title", name, nil,
		map[string]string{"title": old}, map[string]string{"title": t})

	return s.Get(ctx, name)
}
