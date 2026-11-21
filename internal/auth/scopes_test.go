// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import "testing"

// A token may carry every scope at once, so the cap must grow with the
// catalog.
func TestScopeCatalogFitsOneToken(t *testing.T) {
	all := make([]string, 0, len(scopeCatalog))
	for name := range scopeCatalog {
		all = append(all, name)
	}

	if _, err := normalizeScopes(all); err != nil {
		t.Fatalf("every scope at once: %v (raise maxScopesPerToken)", err)
	}
}
