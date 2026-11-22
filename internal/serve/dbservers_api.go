// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

// dbServerTimeout bounds a connection check: connect, TLS, a few
// statements, and for a new server the privilege test.
const dbServerTimeout = 30 * time.Second

// dbServerJSON is a database server in the API (ADR-0041). The password
// and the CA never leave the panel. Who may not manage servers sees only
// what the "New database" dialog needs.
type dbServerJSON struct {
	Name      string     `json:"name"`
	Local     bool       `json:"local"`
	Default   bool       `json:"default"`
	Engine    string     `json:"engine,omitempty"`
	Version   string     `json:"version,omitempty"`
	State     string     `json:"state"` // "ok", "error" or "unchecked"
	Databases int        `json:"databases"`
	TLSMode   string     `json:"tlsMode"`
	TLSActive bool       `json:"tlsActive"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
	// CertExpires is when the server's TLS certificate ends.
	CertExpires *time.Time `json:"certExpires,omitempty"`
	// Host and port go into the site's configuration (DB_HOST), so
	// everyone who sees the server sees them.
	Host string `json:"host,omitempty"`
	Port int    `json:"port,omitempty"`

	// Admins only.
	AdminUser  string `json:"adminUser,omitempty"`
	ClientHost string `json:"clientHost,omitempty"`
	LastError  string `json:"lastError,omitempty"`

	// Warnings come only with a new server: what works but is not
	// recommended, for example a PostgreSQL superuser.
	Warnings []string `json:"warnings,omitempty"`
}

type dbServerListJSON struct {
	Servers []dbServerJSON `json:"servers"`
}

type dbServerAddRequest struct {
	// Engine is "mariadb" (default, also for MySQL) or
	// "postgresql" (ADR-0051).
	Engine        string `json:"engine"`
	Name          string `json:"name"`
	Host          string `json:"host"`
	Port          int    `json:"port"` // 0: 3306 or 5432
	AdminUser     string `json:"adminUser"`
	AdminPassword string `json:"adminPassword"`
	TLSMode       string `json:"tlsMode"` // "verify" (default), "verify-ca" or "none"
	CACertificate string `json:"caCertificate"`
}

func toDBServerJSON(d store.DBServer, details bool) dbServerJSON {
	out := dbServerJSON{
		Name: d.Name, Local: d.Local(), Default: d.IsDefault, Engine: d.Status.Engine, Version: d.Status.Version,
		Databases: d.Databases, TLSMode: d.TLSMode, TLSActive: d.Status.TLSActive,
	}

	switch {
	case d.Local():
		// The agent reaches it over the socket; doctor checks it.
		out.State = "ok"
	case d.Status.LastError != "":
		out.State = "error"
	case d.Status.CheckedAt.IsZero():
		out.State = "unchecked"
	default:
		out.State = "ok"
	}

	if !d.Status.CheckedAt.IsZero() {
		t := d.Status.CheckedAt
		out.CheckedAt = &t
	}

	if !d.Status.CertExpires.IsZero() {
		t := d.Status.CertExpires
		out.CertExpires = &t
	}

	if !d.Local() {
		out.Host, out.Port = d.Host, d.Port
	}

	if details && !d.Local() {
		out.AdminUser = d.AdminUser
		out.ClientHost, out.LastError = d.Status.ClientHost, d.Status.LastError
	}

	return out
}

// dbServerFail maps the server errors and hands the rest to dbFail.
func dbServerFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrDBServerNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such database server")
	case errors.Is(err, hosting.ErrDBServerExists):
		writeError(w, http.StatusConflict, "db_server_exists", "a database server with this name exists")
	case errors.Is(err, hosting.ErrDBServerInUse):
		writeError(w, http.StatusConflict, "db_server_in_use", "the server has databases or is the local server")
	case errors.Is(err, hosting.ErrDBServersOff):
		writeError(w, http.StatusConflict, "db_servers_unavailable", "external database servers are not available on this panel")
	case errors.Is(err, hosting.ErrPGClientTooOld):
		writeError(w, http.StatusUnprocessableEntity, "pg_client_too_old", "the PostgreSQL server is newer than the client tools this Hostashi release can install; update Hostashi first")
	case errors.Is(err, hosting.ErrDBServerCheck):
		// The text names the failed step and the server error number,
		// never a statement or password (internal/dbserver).
		writeError(w, http.StatusUnprocessableEntity, "db_server_check_failed", checkMessage(err))
	default:
		dbFail(log, w, err, what)
	}
}

// checkMessage says in one line why a new server failed the check.
func checkMessage(err error) string {
	switch {
	case errors.Is(err, dbserver.ErrAccess):
		return "the administrator lacks a privilege or the password is wrong: " + err.Error()
	case errors.Is(err, dbserver.ErrUnavailable):
		return "the server is not reachable or its TLS certificate is not trusted: " + err.Error()
	case errors.Is(err, dbserver.ErrUnsupported):
		return "the server version is not supported (MariaDB 10.5+, MySQL 8.0 to 9.x or PostgreSQL 14+): " + err.Error()
	default:
		return err.Error()
	}
}

// registerDBServers adds the database server routes (ADR-0041). Everyone
// who may create databases reads the list to pick a server; managing
// servers is for admins.
func registerDBServers(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/db-servers", openapi.Operation{
		ID: "listDBServers", Tag: "db-servers", Summary: "List the database servers", Out: dbServerListJSON{},
	}, a.allow("db_servers:list", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		list, err := svc.DBServerList(r.Context())
		if err != nil {
			dbServerFail(log, w, err, "list database servers")

			return
		}

		details := authz.Allowed(authz.Role(p.User.Role), "db_servers:write")
		out := make([]dbServerJSON, 0, len(list))

		for _, d := range list {
			out = append(out, toDBServerJSON(d, details))
		}

		writeJSON(w, http.StatusOK, dbServerListJSON{Servers: out})
	}))

	mux.Op("POST /api/v1/db-servers", openapi.Operation{
		ID: "addDBServer", Tag: "db-servers", Summary: "Add an external database server", In: dbServerAddRequest{}, Out: dbServerJSON{}, Status: http.StatusCreated,
	}, a.allow("db_servers:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in dbServerAddRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		var engine string

		switch in.Engine {
		case "", "mariadb":
		case "postgresql":
			engine = dbserver.EnginePostgreSQL
		default:
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `engine must be "mariadb" or "postgresql"`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), dbServerTimeout)
		defer cancel()

		d, warnings, err := svc.AddDBServer(ctx, p.User.ID, hosting.NewDBServer{
			Engine: engine, Name: in.Name, Host: in.Host, Port: in.Port, AdminUser: in.AdminUser, AdminPassword: in.AdminPassword,
			TLS: dbserver.TLSMode(in.TLSMode), CAPEM: in.CACertificate,
		})
		if err != nil {
			dbServerFail(log, w, err, "add database server")

			return
		}

		out := toDBServerJSON(d, true)
		out.Warnings = warnings
		writeJSON(w, http.StatusCreated, out)
	}))

	mux.Op("POST /api/v1/db-servers/{server}/check", openapi.Operation{
		ID: "checkDBServer", Tag: "db-servers", Summary: "Check the connection to a database server", Out: dbServerJSON{},
	}, a.allow("db_servers:write", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), dbServerTimeout)
		defer cancel()

		d, err := svc.CheckDBServer(ctx, r.PathValue("server"))
		if err != nil {
			dbServerFail(log, w, err, "check database server")

			return
		}

		writeJSON(w, http.StatusOK, toDBServerJSON(d, true))
	}))

	mux.Op("PUT /api/v1/db-servers/{server}/default", openapi.Operation{
		ID: "setDefaultDBServer", Tag: "db-servers", Summary: "Make a database server the default",
	}, a.allow("db_servers:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if err := svc.SetDefaultDBServer(r.Context(), p.User.ID, r.PathValue("server")); err != nil {
			dbServerFail(log, w, err, "set default database server")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	mux.Op("DELETE /api/v1/db-servers/{server}", openapi.Operation{
		ID: "deleteDBServer", Tag: "db-servers", Summary: "Remove a database server without databases",
	}, a.allow("db_servers:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		if err := svc.DeleteDBServer(r.Context(), p.User.ID, r.PathValue("server")); err != nil {
			dbServerFail(log, w, err, "delete database server")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))
}
