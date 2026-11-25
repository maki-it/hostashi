// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import "testing"

func TestWPUnitNames(t *testing.T) {
	ctx := t.Context()
	c := WPController{}

	for _, u := range []string{"hostashi-wp@.service", "hostashi-wp@a.service", "hostashi-wp@blog.service.d", "hostashi-wp@../x.service", "hostashi-deploy@blog.service", "hostashi-wpcli-install.service"} {
		if err := c.StartNoBlock(ctx, u); err == nil {
			t.Errorf("%q accepted", u)
		}

		if _, err := c.Show(ctx, u, "ActiveState"); err == nil {
			t.Errorf("show %q accepted", u)
		}
	}

	if _, err := c.Show(ctx, "hostashi-wp@blog.service", "ExecStart"); err == nil {
		t.Error("property ExecStart accepted")
	}

	if !wpUnitRe.MatchString("hostashi-wp@blog.service") {
		t.Error("unit refused")
	}
}
