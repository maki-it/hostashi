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

	const hint = "jails.hint"

	if err != nil {
		return warnT(id, "jails.error", hint, P{"error": describe(err)})
	}

	var missing []string

	for _, j := range st.Jails {
		if !j.Running {
			missing = append(missing, j.Name)
		}
	}

	if len(missing) > 0 {
		return warnT(id, "jails.down", hint, P{"jails": strings.Join(missing, ", ")})
	}

	return okT(id, "jails.ok", nil)
}
