// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import "testing"

func TestPGClientUnitName(t *testing.T) {
	got, err := pgClientUnit("19")
	if err != nil || got != "hostashi-pg-client-install@19.service" {
		t.Fatalf("19: %q %v", got, err)
	}

	for _, m := range []string{"", "13", "19.service", "19 ", "../19", "x"} {
		if _, err := pgClientUnit(m); err == nil {
			t.Errorf("%q accepted", m)
		}
	}
}
