// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/dbserver"
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
		return []agentapi.DoctorCheck{{
			ID: id, Status: agentapi.DoctorWarn, Summary: "cannot read which PostgreSQL client is installed",
			Hint: "The agent did not answer; check hostashi-agent.service.",
		}}
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
			out = append(out, agentapi.DoctorCheck{
				ID: id, Status: agentapi.DoctorFail,
				Summary: "no PostgreSQL client is installed for database server " + s.Name + ": its backups, imports and exports fail",
				Hint:    "Install it with POST /api/v1/db-servers/postgresql-client; adding a PostgreSQL server starts it too.",
			})
		case server == 0:
			// Not checked yet; dbserver.reachable says so.
		case server > client:
			hint := "Update Hostashi: a newer release brings the client for PostgreSQL " + strconv.Itoa(server) + "."
			if majorOf(newest) >= server {
				hint = "Install the PostgreSQL " + newest + " client with POST /api/v1/db-servers/postgresql-client."
			}

			out = append(out, agentapi.DoctorCheck{
				ID: id, Status: agentapi.DoctorFail,
				Summary: "the PostgreSQL client " + strconv.Itoa(client) + " is older than database server " + s.Name +
					" (PostgreSQL " + s.Status.Version + "): its backups, imports and exports fail",
				Hint: hint,
			})
		default:
			out = append(out, agentapi.DoctorCheck{
				ID: id, Status: agentapi.DoctorOK,
				Summary: "the PostgreSQL client " + strconv.Itoa(client) + " can dump database server " + s.Name + " (PostgreSQL " + s.Status.Version + ")",
			})
		}
	}

	return out
}
