// SPDX-License-Identifier: AGPL-3.0-or-later

package doctor

import (
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
)

// Fail2banJails checks that every Hostashi jail runs (ADR-0078). A jail
// that runs and counts nothing is normal on a quiet server, so only a
// missing jail warns.
func Fail2banJails(st agentapi.Fail2banStatus, err error) Check {
	const id = "service.fail2ban.jails"

	hint := "Check the jails: fail2ban-client status, and a local file under /etc/fail2ban/jail.d/ that may break them."
	if err != nil {
		return warn(id, "the jails cannot be read: "+describe(err), hint)
	}

	var missing []string

	for _, j := range st.Jails {
		if !j.Running {
			missing = append(missing, j.Name)
		}
	}

	if len(missing) > 0 {
		return warn(id, "not running: "+strings.Join(missing, ", "), hint)
	}

	return ok(id, "all jails are running")
}
