// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import "testing"

func TestScanUnitNames(t *testing.T) {
	ctx := t.Context()
	c := ScanController{}

	for _, u := range []string{"hostashi-scan@.service", "hostashi-scan@a.service", "hostashi-scan@blog.service.d", "hostashi-scan@../x.service", "hostashi-deploy@blog.service", "clamav-daemon.service"} {
		if err := c.StartNoBlock(ctx, u); err == nil {
			t.Errorf("%q accepted", u)
		}

		if _, err := c.Show(ctx, u, "ActiveState"); err == nil {
			t.Errorf("show %q accepted", u)
		}
	}

	if _, err := c.Show(ctx, "hostashi-scan@blog.service", "ExecStart"); err == nil {
		t.Error("property ExecStart accepted")
	}

	if !scanUnitRe.MatchString("hostashi-scan@blog.service") {
		t.Error("scan unit refused")
	}

	for _, inst := range []string{"", "purge", "install ", "install.service", "../remove"} {
		if _, err := clamavUnit(inst); err == nil {
			t.Errorf("instance %q accepted", inst)
		}
	}

	for _, inst := range []string{"install", "remove"} {
		if _, err := clamavUnit(inst); err != nil {
			t.Errorf("instance %q refused: %v", inst, err)
		}
	}
}
