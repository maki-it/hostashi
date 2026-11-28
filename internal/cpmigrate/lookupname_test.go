// SPDX-License-Identifier: AGPL-3.0-or-later

package cpmigrate

import (
	"regexp"
	"testing"
)

func TestLookupName(t *testing.T) {
	if got := lookupName("www.example.com"); got != "www.example.com" {
		t.Fatalf("exact %q", got)
	}

	re := regexp.MustCompile(`^hostashi-check-[a-z2-7]{10}\.example\.com$`)

	a, b := lookupName("*.example.com"), lookupName("*.example.com")
	if !re.MatchString(a) || a == b {
		t.Fatalf("wildcard %q %q", a, b)
	}
}
