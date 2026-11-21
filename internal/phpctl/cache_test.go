// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"strings"
	"testing"
)

func TestCacheRefusesOddInput(t *testing.T) {
	var c CacheController

	for _, u := range []string{
		"", "valkey-server.service", "hostashi-valkey@.service", "hostashi-valkey@Blog.service",
		"hostashi-valkey@blog.service --now", "hostashi-valkey@../x.service", "hostashi-php-8.4@blog.service",
		"hostashi-valkey@blog.socket", "--all", "hostashi-valkey@blog.service\n",
	} {
		for name, f := range map[string]func() error{
			"start":   func() error { return c.Start(t.Context(), u) },
			"stop":    func() error { return c.Stop(t.Context(), u) },
			"restart": func() error { return c.Restart(t.Context(), u) },
			"show":    func() error { _, err := c.Show(t.Context(), u, "ActiveState"); return err },
		} {
			if err := f(); err == nil || !strings.Contains(err.Error(), "refusing") {
				t.Errorf("%s %q: %v", name, u, err)
			}
		}
	}

	for _, p := range []string{"", "ExecStart", "ActiveState --all", "Environment"} {
		if _, err := c.Show(t.Context(), "hostashi-valkey@blog.service", p); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("property %q: %v", p, err)
		}
	}
}
