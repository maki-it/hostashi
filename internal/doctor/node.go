// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"strconv"
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
			out = append(out, warnT(id, "node.behind", "node.behind.hint", P{"line": i.Major, "version": i.Version, "release": rel.Version}))
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
			out = append(out, warnT(id, "node.ended", "node.ended.hint", P{"line": i.Major, "end": rel.End, "sites": strings.Join(sites, ", ")}))
		case days <= NodeSoonDays:
			out = append(out, warnT(id, "node.soon", "node.soon.hint", P{"line": i.Major, "end": rel.End, "days": strconv.Itoa(days), "sites": strings.Join(sites, ", ")}))
		}
	}

	if len(out) == 0 && len(installed) > 0 {
		out = append(out, okT(id, "node.ok", nil))
	}

	return out
}
