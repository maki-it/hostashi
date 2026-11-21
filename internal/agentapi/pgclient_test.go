// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestValidatePGClientMajor(t *testing.T) {
	for _, m := range PGClientMajors {
		if err := ValidatePGClientMajor(m); err != nil {
			t.Errorf("%s: %v", m, err)
		}
	}

	for _, m := range []string{"", "9", "13", "19.0", " 19", "19\n", "../19", "1 9", "99"} {
		if err := ValidatePGClientMajor(m); err == nil {
			t.Errorf("%q accepted", m)
		}
	}

	if err := (PGClientInstallRequest{}).Validate(); err != nil {
		t.Errorf("empty major (newest) refused: %v", err)
	}

	if err := (PGClientInstallRequest{Major: "13"}).Validate(); err == nil {
		t.Error("major 13 accepted")
	}

	if PGClientNewest() != PGClientMajors[len(PGClientMajors)-1] {
		t.Error("newest is not the last major")
	}
}

// The script behind the install unit (ADR-0051) knows the same majors.
func TestPGClientInstallKnowsTheSameMajors(t *testing.T) {
	b, err := os.ReadFile("../../packaging/postgresql/pg-client-install")
	if err != nil {
		t.Fatal(err)
	}

	m := regexp.MustCompile(`(?m)^PG_MAJORS=\(([^)]*)\)$`).FindSubmatch(b)
	if m == nil {
		t.Fatal("PG_MAJORS not found in pg-client-install")
	}

	if got := strings.Fields(string(m[1])); !slices.Equal(got, PGClientMajors) {
		t.Fatalf("pg-client-install has %v, agentapi has %v", got, PGClientMajors)
	}
}
