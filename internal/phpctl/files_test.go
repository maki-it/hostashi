// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"context"
	"testing"
)

func TestFilesRefusesOddUnits(t *testing.T) {
	ctx := context.Background()
	c := FilesController{}

	for _, u := range []string{
		"", "hostashi-files@.socket", "hostashi-files@Blog.socket", "hostashi-files@blog.timer",
		"hostashi-files@blog.socket;reboot", "hostashi-files@../x.socket", "nginx.service",
		"hostashi-files@blog.socket --now", "hostashi-files@b.socket",
	} {
		if err := c.Start(ctx, u); err == nil {
			t.Errorf("Start(%q) accepted", u)
		}

		if err := c.Stop(ctx, u); err == nil {
			t.Errorf("Stop(%q) accepted", u)
		}
	}

	// Start only takes the socket, Restart only the service.
	if err := c.Start(ctx, "hostashi-files@blog.service"); err == nil {
		t.Error("Start accepted the service")
	}

	if err := c.Restart(ctx, "hostashi-files@blog.socket"); err == nil {
		t.Error("Restart accepted the socket")
	}
}
