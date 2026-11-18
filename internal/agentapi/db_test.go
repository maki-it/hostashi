// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"strings"
	"testing"
)

func TestValidateDBName(t *testing.T) {
	good := []string{"shop", "shop_wp", "wp1", "a", strings.Repeat("a", 64), "wordpress_db_2"}
	bad := []string{
		"", "1shop", "_shop", "Shop", "shop-wp", "shop.wp", "shop wp", "shop`", "shop'", "shop%",
		"shop;", "../x", "shöp", strings.Repeat("a", 65), "mysql", "information_schema",
		"performance_schema", "sys", "test", "hostashi", "hostashi_panel", "a\x00b", "a\nb",
	}

	for _, n := range good {
		if err := ValidateDBName(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}

	for _, n := range bad {
		if err := ValidateDBName(n); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestValidateDBUser(t *testing.T) {
	good := []string{"shop", "shop_wp", "u1", strings.Repeat("a", 32)}
	bad := []string{
		"", "1u", "U1", "u-1", "u.1", "u@x", "u'1", strings.Repeat("a", 33), "root", "mysql",
		"mariadb", "mariadb_sys", "debian_sys_maint", "healthcheck", "hostashi", "hostashi_x",
	}

	for _, n := range good {
		if err := ValidateDBUser(n); err != nil {
			t.Errorf("%q: %v", n, err)
		}
	}

	for _, n := range bad {
		if err := ValidateDBUser(n); err == nil {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestValidateDBPassword(t *testing.T) {
	good := []string{
		"abcdefghijkl", strings.Repeat("x", 128), "A1!#$%&()*+,-./:;<=>?@[]^_{|}~",
		"Kq7-x9!mZ2@pL4",
	}
	bad := []string{
		"", "short", "abcdefghijk", strings.Repeat("x", 129), "with space 12345",
		"single'quote1234", `double"quote1234`, `back\slash123456`, "back`tick1234567",
		"newline\n12345678", "tab\t1234567890", "umlaut-ä-12345678", "nul\x00-1234567890",
	}

	for _, p := range good {
		if err := ValidateDBPassword(p); err != nil {
			t.Errorf("%q: %v", p, err)
		}
	}

	for _, p := range bad {
		if err := ValidateDBPassword(p); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}

func FuzzValidateDBPassword(f *testing.F) {
	f.Add("Secret-pass_12345")
	f.Add("x' OR '1'='1")

	f.Fuzz(func(t *testing.T, pw string) {
		if ValidateDBPassword(pw) != nil {
			return
		}

		if strings.ContainsAny(pw, "'\"\\` \t\r\n\x00") {
			t.Fatalf("accepted %q", pw)
		}
	})
}

func TestGenerateDBPassword(t *testing.T) {
	seen := map[string]bool{}

	for range 100 {
		pw := GenerateDBPassword()
		if len(pw) != GeneratedDBPasswordLen {
			t.Fatalf("length %d", len(pw))
		}

		if err := ValidateDBPassword(pw); err != nil {
			t.Fatalf("%q: %v", pw, err)
		}

		if strings.Trim(pw, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789") != "" {
			t.Fatalf("not alphanumeric: %q", pw)
		}

		if seen[pw] {
			t.Fatal("repeated password")
		}

		seen[pw] = true
	}
}
