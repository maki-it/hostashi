// SPDX-License-Identifier: AGPL-3.0-or-later

package phpctl

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefusesOddInput(t *testing.T) {
	var c Controller

	for _, p := range []string{"", "pool.conf", "/a b/x.conf", "/x/../etc/x.conf", "/x/x.conf;id", "/x/x.ini", "/x/x.conf\n"} {
		if err := c.Test(t.Context(), "8.4", p); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("conf %q: %v", p, err)
		}
	}

	for _, v := range []string{"", "8.0", "../8.4", "8.4 -i"} {
		if err := c.Test(t.Context(), v, "/x/x.conf"); err == nil {
			t.Errorf("version %q accepted", v)
		}
	}

	for _, u := range []string{
		"", "nginx.service", "hostashi-php-8.4@.service", "hostashi-php-8.4@Blog.service",
		"hostashi-php-8.4@blog.service --now", "hostashi-php-84@blog.service", "--all",
		"hostashi-php-8.4@../x.service",
	} {
		for _, f := range []func() error{
			func() error { return c.Start(t.Context(), u) },
			func() error { return c.Reload(t.Context(), u) },
			func() error { return c.Stop(t.Context(), u) },
		} {
			if err := f(); err == nil || !strings.Contains(err.Error(), "refusing") {
				t.Errorf("unit %q: %v", u, err)
			}
		}
	}
}

func TestSliceProperties(t *testing.T) {
	var c Controller

	good := []string{"CPUWeight=100", "CPUQuota=", "MemoryHigh=infinity", "MemoryMax=1073741824", "TasksMax=256"}

	for _, s := range []string{
		"", "hostashi-site.slice", "system.slice", "hostashi-site-Shop.slice", "hostashi-site-my-shop.slice",
		"hostashi-site-../x.slice", "hostashi-site-shop.service", `hostashi-site-shop\x2f.slice`, "--all",
		"user-0.slice", "user-999.slice", "user-01000.slice", "user.slice", "user-.slice", "user-1000-x.slice", "user-12345678901.slice",
	} {
		if err := c.SetSliceProperties(t.Context(), s, good); err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("slice %q: %v", s, err)
		}
	}

	for _, p := range []string{
		"", "CPUWeight", "MemoryMax=-1", "MemoryMax=1G", "TasksMax=10%", "CPUQuota=50", "Delegate=yes",
		"ExecStart=/bin/sh", "MemoryMax=1 TasksMax=2", "--no-block", "CPUWeight=1\nDelegate=yes",
	} {
		err := c.SetSliceProperties(t.Context(), `hostashi-site-my\x2dshop.slice`, append([]string{"TasksMax=64"}, p))
		if err == nil || !strings.Contains(err.Error(), "refusing") {
			t.Errorf("property %q: %v", p, err)
		}
	}

	if err := c.SetSliceProperties(t.Context(), "hostashi-site-shop.slice", nil); err == nil {
		t.Error("empty property list accepted")
	}

	for _, p := range good {
		if !slicePropRe.MatchString(p) {
			t.Errorf("good property %q refused", p)
		}
	}

	for _, s := range []string{"hostashi-site-shop.slice", `hostashi-site-my\x2dshop.slice`, "hostashi-site-a_1.slice", "user-1000.slice", "user-65534.slice"} {
		if !sliceRe.MatchString(s) {
			t.Errorf("good slice %q refused", s)
		}
	}
}

// TestRealPHPFPM runs the installed php-fpm on a minimal config, if there is one.
func TestRealPHPFPM(t *testing.T) {
	versions := Installed()
	if len(versions) == 0 {
		t.Skip("no php-fpm installed")
	}

	dir := t.TempDir()
	conf := filepath.Join(dir, "good.conf")
	bad := filepath.Join(dir, "bad.conf")

	if err := os.WriteFile(conf, []byte("[global]\nerror_log = /dev/null\n[www]\nuser = nobody\nlisten = "+dir+"/s.sock\npm = ondemand\npm.max_children = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(bad, []byte("[global]\nnot a setting\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := (Controller{}).Test(t.Context(), versions[0], conf); err != nil {
		t.Fatal(err)
	}

	if err := (Controller{}).Test(t.Context(), versions[0], bad); err == nil {
		t.Fatal("bad config accepted")
	}
}
