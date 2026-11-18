// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestValidatePHPVersion(t *testing.T) {
	for _, v := range PHPVersions {
		if err := ValidatePHPVersion(v); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}

	for _, v := range LegacyPHPVersions {
		if err := ValidatePHPVersion(v); err != nil {
			t.Errorf("legacy %s not in the list: %v", v, err)
		}
	}

	for _, v := range []string{"", "8", "8.0", "5.6", "8.4.1", " 8.4", "8.4\n", "../8.4", "84"} {
		if err := ValidatePHPVersion(v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
}

// The installer writes a unit template for every version the agent accepts.
func TestInstallerKnowsTheSamePHPVersions(t *testing.T) {
	b, err := os.ReadFile("../../packaging/install.sh")
	if err != nil {
		t.Fatal(err)
	}

	m := regexp.MustCompile(`(?m)^PHP_VERSIONS=\(([^)]*)\)$`).FindSubmatch(b)
	if m == nil {
		t.Fatal("PHP_VERSIONS not found in install.sh")
	}

	if got := strings.Fields(string(m[1])); !slices.Equal(got, PHPVersions) {
		t.Fatalf("install.sh has %v, agentapi has %v", got, PHPVersions)
	}
}
