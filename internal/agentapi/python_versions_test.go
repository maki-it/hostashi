// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"regexp"
	"testing"
	"time"
)

func TestPythonReleases(t *testing.T) {
	sum := regexp.MustCompile(`^[0-9a-f]{64}$`)
	line := regexp.MustCompile(`^3\.[0-9]{1,2}$`)
	tag := regexp.MustCompile(`^[0-9]{8}$`)
	prev := ""

	for _, r := range PythonReleases {
		if !line.MatchString(r.Line) || (prev != "" && !lineAfter(r.Line, prev)) {
			t.Errorf("%s: lines must be 3.x, oldest first", r.Line)
		}

		prev = r.Line

		if !regexp.MustCompile(`^` + regexp.QuoteMeta(r.Line) + `\.[0-9]+$`).MatchString(r.Version) {
			t.Errorf("%s: version %q", r.Line, r.Version)
		}

		if !tag.MatchString(r.Tag) {
			t.Errorf("%s: tag %q", r.Line, r.Tag)
		}

		if _, err := time.Parse(time.DateOnly, r.End); err != nil {
			t.Errorf("%s: end %q", r.Line, r.End)
		}

		for _, a := range []string{"x86_64", "aarch64"} {
			if !sum.MatchString(r.SHA256[a]) {
				t.Errorf("%s %s: SHA-256 %q", r.Line, a, r.SHA256[a])
			}
		}

		if len(r.SHA256) != 2 {
			t.Errorf("%s: unexpected architectures %v", r.Line, r.SHA256)
		}
	}

	for _, bad := range []string{"", "3", "3.10", "3.13.1", "../3.13", "3.13 ", "2.7"} {
		if ValidatePythonLine(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// lineAfter reports whether 3.a comes after 3.b.
func lineAfter(a, b string) bool {
	return len(a) > len(b) || (len(a) == len(b) && a > b)
}
