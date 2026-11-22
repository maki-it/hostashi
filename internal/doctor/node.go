// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"fmt"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// NodeSoonDays is how many days before a Node.js line's end of life its
// sites get a warning, as on the panel's Node.js versions page.
const NodeSoonDays = 180

// Node checks the installed Node.js lines (ADR-0054): each runs the
// release Hostashi pins, which the agent installs by itself after a
// Hostashi update, and a line that apps run on still gets fixes. users
// lists the sites per line. Without an installed line there is nothing to
// check.
func Node(installed []agentapi.NodeInstalled, users map[string][]string, now time.Time) []Check {
	const id = "system.node"

	var out []Check

	for _, i := range installed {
		rel, err := agentapi.NodeReleaseOf(i.Major)
		if err != nil {
			continue
		}

		if i.Version != rel.Version {
			out = append(out, warn(id,
				fmt.Sprintf("Node.js %s runs %s, Hostashi brings %s: the automatic update did not finish", i.Major, i.Version, rel.Version),
				"Install the update under Node.js versions in the panel; its log shows what failed."))
		}

		sites := users[i.Major]
		if len(sites) == 0 {
			continue
		}

		end, err := time.Parse(time.DateOnly, rel.End)
		if err != nil {
			continue
		}

		left := end.Add(24 * time.Hour).Sub(now)
		days := int(left.Hours() / 24)

		switch {
		case left <= 0:
			out = append(out, warn(id,
				"Node.js "+i.Major+" gets no fixes since "+rel.End+" ("+strings.Join(sites, ", ")+")",
				"Move the sites to a newer Node.js version on their Web tab."))
		case days <= NodeSoonDays:
			out = append(out, warn(id,
				fmt.Sprintf("Node.js %s gets fixes only until %s (%d days; %s)", i.Major, rel.End, days, strings.Join(sites, ", ")),
				"Plan the move of the sites to a newer Node.js version."))
		}
	}

	if len(out) == 0 && len(installed) > 0 {
		out = append(out, ok(id, "every installed Node.js line runs the current release"))
	}

	return out
}
