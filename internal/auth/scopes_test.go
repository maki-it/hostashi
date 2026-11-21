// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import "testing"

// A token must be able to carry every scope of the catalog.
func TestScopeCatalogFitsOneToken(t *testing.T) {
	if len(scopeCatalog) > maxScopesPerToken {
		t.Fatalf("%d scopes, but a token holds at most %d", len(scopeCatalog), maxScopesPerToken)
	}
}
