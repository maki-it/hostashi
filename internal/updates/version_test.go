// SPDX-License-Identifier: AGPL-3.0-or-later

package updates

import "testing"

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"0.1.0", "0.1.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"0.10.0", "0.9.0", 1},
		{"1.0.0", "1.0.0.1", -1},
		{"1.0.0~rc1", "1.0.0", -1},
		{"1.0.0~rc1", "1.0.0~rc2", -1},
		{"1.0.0~~", "1.0.0~", -1},
		{"1.0.0+b1", "1.0.0", 1},
		{"1.0.0a", "1.0.0+", -1},
		{"1:0.1", "2.0", 1},
		{"0:1.0", "1.0", 0},
		{"1.0-2", "1.0-10", -1},
		{"1.0-1", "1.0", 1},
		{"1.2-3-4", "1.2-3-5", -1},
		{"007", "7", 0},
		{"1.0.0.4", "1.0.0.3", 1},
	} {
		got := Compare(c.a, c.b)
		if sign(got) != c.want {
			t.Errorf("Compare(%q, %q) = %d, want sign %d", c.a, c.b, got, c.want)
		}

		if sign(Compare(c.b, c.a)) != -c.want {
			t.Errorf("Compare(%q, %q) not antisymmetric", c.b, c.a)
		}
	}
}

func sign(n int) int {
	switch {
	case n < 0:
		return -1
	case n > 0:
		return 1
	default:
		return 0
	}
}
