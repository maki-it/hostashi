// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/doctor"
	"github.com/maki-it/hostashi/internal/store"
)

// enginePostgreSQL is the engine the server check records for PostgreSQL
// (ADR-0051).
const enginePostgreSQL = dbserver.EnginePostgreSQL

// pgClientCheck reports, for doctor, whether the installed PostgreSQL
// client can dump every external PostgreSQL server: pg_dump refuses a
// server of a newer major (ADR-0051).
func pgClientCheck(servers func(context.Context) ([]store.DBServer, error), pg PGClient) PanelChecks {
	return func(ctx context.Context, _ time.Time) ([]agentapi.DoctorCheck, error) {
		list, err := servers(ctx)
		if err != nil {
			return nil, err
		}

		if !hasPGServer(list) {
			return nil, nil
		}

		ctx, cancel := context.WithTimeout(ctx, agentTimeout)
		defer cancel()

		st, err := pg.Status(ctx)

		return pgClientChecks(list, st, err), nil
	}
}

func hasPGServer(list []store.DBServer) bool {
	for _, s := range list {
		if !s.Local() && s.Status.Engine == enginePostgreSQL {
			return true
		}
	}

	return false
}

// majorOf is the number before the first dot of a version, or 0.
func majorOf(version string) int {
	head, _, _ := strings.Cut(version, ".")

	n, err := strconv.Atoi(head)
	if err != nil || n < 0 {
		return 0
	}

	return n
}

func pgClientChecks(list []store.DBServer, st agentapi.PGClientStatus, stErr error) []agentapi.DoctorCheck {
	const id = "dbserver.pg_client"

	if stErr != nil {
		return []agentapi.DoctorCheck{doctor.New(agentapi.DoctorWarn, id, "pgclient.error", "pgclient.error.hint", nil)}
	}

	client := 0
	for _, m := range st.Installed {
		client = max(client, majorOf(m))
	}

	newest := agentapi.PGClientNewest()

	var out []agentapi.DoctorCheck

	for _, s := range list {
		if s.Local() || s.Status.Engine != enginePostgreSQL {
			continue
		}

		server := majorOf(s.Status.Version)

		switch {
		case client == 0:
			out = append(out, doctor.New(agentapi.DoctorFail, id, "pgclient.none", "pgclient.none.hint", doctor.P{"name": s.Name}))
		case server == 0:
			// Not checked yet; dbserver.reachable says so.
		case server > client:
			params := doctor.P{"client": strconv.Itoa(client), "name": s.Name, "version": s.Status.Version, "server": strconv.Itoa(server), "newest": newest}

			hint := "pgclient.update.hint"
			if majorOf(newest) >= server {
				hint = "pgclient.install.hint"
			}

			out = append(out, doctor.New(agentapi.DoctorFail, id, "pgclient.old", hint, params))
		default:
			out = append(out, doctor.New(agentapi.DoctorOK, id, "pgclient.ok", "", doctor.P{"client": strconv.Itoa(client), "name": s.Name, "version": s.Status.Version}))
		}
	}

	return out
}
