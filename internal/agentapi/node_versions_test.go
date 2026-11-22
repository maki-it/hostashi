// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"regexp"
	"strconv"
	"testing"
	"time"
)

func TestNodeReleases(t *testing.T) {
	sum := regexp.MustCompile(`^[0-9a-f]{64}$`)
	prev := 0

	for _, r := range NodeReleases {
		m, err := strconv.Atoi(r.Major)
		if err != nil || m%2 != 0 || m <= prev {
			t.Errorf("%s: majors must be even LTS lines, oldest first", r.Major)
		}

		prev = m

		if !regexp.MustCompile(`^` + r.Major + `\.[0-9]+\.[0-9]+$`).MatchString(r.Version) {
			t.Errorf("%s: version %q", r.Major, r.Version)
		}

		if _, err := time.Parse(time.DateOnly, r.End); err != nil || r.Codename == "" {
			t.Errorf("%s: end %q codename %q", r.Major, r.End, r.Codename)
		}

		for _, a := range []string{"x64", "arm64"} {
			if !sum.MatchString(r.SHA256[a]) {
				t.Errorf("%s %s: SHA-256 %q", r.Major, a, r.SHA256[a])
			}
		}

		if len(r.SHA256) != 2 {
			t.Errorf("%s: unexpected architectures %v", r.Major, r.SHA256)
		}
	}

	for _, bad := range []string{"", "25", "24.1", "../24", "24 "} {
		if ValidateNodeMajor(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
