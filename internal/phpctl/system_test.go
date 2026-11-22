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

func TestAppUnitNames(t *testing.T) {
	ctx := t.Context()

	for _, u := range []string{"hostashi-node@.service", "hostashi-node@a.service", "hostashi-php-8.3@blog.service", "hostashi-node@blog.service.d", "hostashi-node@../x.service"} {
		if err := appctl(ctx, u, "start"); err == nil {
			t.Errorf("%q accepted", u)
		}
	}

	if _, err := (AppController{}).Show(ctx, "hostashi-node@blog.service", "ExecStart"); err == nil {
		t.Error("property ExecStart accepted")
	}
}
