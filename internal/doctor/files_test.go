// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func TestFiles(t *testing.T) {
	const btime = 1_795_000_000

	now := time.Unix(btime, 0).Add(48 * time.Hour)
	stat := func(startSec int) string {
		// The name holds a space and a ")" on purpose.
		return "42 (hostashi (x) y) S 1 42 42 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 3 0 " + strconv.Itoa(startSec*userHZ) + " 1000 10\n"
	}
	merge := func(ms ...map[string]string) map[string]string {
		out := map[string]string{}

		for _, m := range ms {
			for k, v := range m {
				out[k] = v
			}
		}

		return out
	}
	units := map[string]string{FilesService: "[Service]\n", FilesSocket: "[Socket]\n", "/proc/stat": "cpu 1 2 3\nbtime 1795000000\n"}
	with := func(extra map[string]string) Hardening {
		return Hardening{Root: tree(t, merge(units, extra)), Now: func() time.Time { return now }}
	}

	helper := func(pid, site, uid string, startSec int) map[string]string {
		return map[string]string{
			"/proc/" + pid + "/cgroup": "0::/hostashi.slice/hostashi-site-" + site + ".slice/hostashi-files@" + site + ".service\n",
			"/proc/" + pid + "/status": "Name:\thostashi\nUid:\t" + uid + "\t" + uid + "\t" + uid + "\t" + uid + "\n",
			"/proc/" + pid + "/stat":   stat(startSec),
		}
	}

	recent := 47 * 3600 // an hour ago
	day := 3600         // 47 hours ago

	for _, tc := range []struct {
		name   string
		h      Hardening
		status string
		want   string
	}{
		{"missing units", Hardening{Root: tree(t, map[string]string{FilesService: "x"})}, agentapi.DoctorFail, "units are missing"},
		{"none running", with(nil), agentapi.DoctorOK, "run as the site users"},
		{"fresh helper", with(helper("10", "blog", "1001", recent)), agentapi.DoctorOK, "run as the site users"},
		{"old helper", with(merge(helper("10", "blog", "1001", recent), helper("11", "shop", "1002", day))), agentapi.DoctorWarn, "of shop has run for more than a day"},
		{"as root", with(merge(helper("11", "shop", "1002", day), helper("12", "wiki", "0", recent))), agentapi.DoctorFail, "of wiki runs as root"},
		{"other units", with(map[string]string{"/proc/13/cgroup": "0::/system.slice/hostashi-php-8.4@blog.service\n", "/proc/13/status": "Uid:\t0\t0\t0\t0\n"}), agentapi.DoctorOK, ""},
	} {
		c := tc.h.Files()
		if c.ID != "system.files" || c.Status != tc.status || !strings.Contains(c.Summary, tc.want) {
			t.Errorf("%s: %+v", tc.name, c)
		}
	}
}
