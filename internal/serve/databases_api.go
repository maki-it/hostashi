// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/dbserver"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/store"
)

type databaseJSON struct {
	Name      string    `json:"name"`
	Site      string    `json:"site"`
	Server    string    `json:"server"`
	Users     []string  `json:"users"`
	CreatedAt time.Time `json:"createdAt"`
	// SizeBytes is the size of a local database (ADR-0015), absent for
	// external ones and when the measurement failed.
	SizeBytes *int64 `json:"sizeBytes,omitempty"`
}

func toDatabaseJSON(d store.Database) databaseJSON {
	u := d.Users
	if u == nil {
		u = []string{}
	}

	return databaseJSON{Name: d.Name, Site: d.Site, Server: d.Server, Users: u, CreatedAt: d.CreatedAt}
}

// withSize adds the size of a local database from sizes.
func (j databaseJSON) withSize(sizes hosting.DBSizes) databaseJSON {
	if j.Server != store.LocalDBServer {
		return j
	}

	if b, ok := sizes.Bytes[j.Name]; ok {
		j.SizeBytes = &b
	}

	return j
}

// dbSizes measures the local databases for a response. A failure only
// leaves the sizes out.
func dbSizes(r *http.Request, svc *hosting.Service, log *slog.Logger) hosting.DBSizes {
	z, err := svc.DatabaseSizes(r.Context())
	if err != nil && !errors.Is(err, hosting.ErrNoDBSizes) {
		log.WarnContext(r.Context(), "measure database sizes", "err", err)
	}

	return z
}

// dbFail maps the database errors and hands the rest to siteFail.
func dbFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrDatabaseNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such database or database user")
	case errors.Is(err, hosting.ErrJobNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such job for this database")
	case errors.Is(err, hosting.ErrToolSessionNotFound):
		writeError(w, http.StatusNotFound, "not_found", "the session has ended")
	case errors.Is(err, hosting.ErrToolSessionLimit):
		writeError(w, http.StatusConflict, "dbtool_session_limit", "the session already ends as late as it may")
	case errors.Is(err, hosting.ErrNoDBTool):
		writeError(w, http.StatusConflict, "dbtool_unavailable", "the database tool is not available on this server")
	case errors.Is(err, hosting.ErrDatabaseExists):
		writeError(w, http.StatusConflict, "database_exists", "a database or database user with this name exists")
	case errors.Is(err, hosting.ErrPostgreSQLNotYet):
		writeError(w, http.StatusConflict, "postgresql_not_yet", "this does not work for PostgreSQL databases yet")
	case errors.Is(err, hosting.ErrDBServerLocalOnly):
		writeError(w, http.StatusConflict, "db_server_local_only", "this works only for databases on the local server so far")
	case errors.Is(err, hosting.ErrDBServerResolve):
		writeError(w, http.StatusBadGateway, "db_server_unresolved", "the database server's name gives no address the job may connect to")
	case errors.Is(err, hosting.ErrDBServersOff):
		writeError(w, http.StatusConflict, "db_server_local_only", "external database servers are not available")
	case errors.Is(err, hosting.ErrDBServerNotFound):
		writeError(w, http.StatusUnprocessableEntity, "unknown_db_server", "no such database server")
	case errors.Is(err, dbserver.ErrUnavailable):
		writeError(w, http.StatusBadGateway, "db_server_unavailable", "the database server is not reachable")
	case errors.Is(err, dbserver.ErrAccess):
		writeError(w, http.StatusBadGateway, "db_server_denied", "the database server refused the operation")
	default:
		siteFail(log, w, err, what)
	}
}

// registerDatabases adds the database routes (ADR-0024). A password is in a
// response only when the panel generated it; the panel stores none.
func registerDatabases(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	// dbSite names the site of the {db} path value, so a user reaches only
	// the databases of granted sites (ADR-0035).
	dbSite := func(r *http.Request) (string, bool) {
		d, err := svc.Database(r.Context(), r.PathValue("db"))
		if err != nil {
			return "", false
		}

		return d.Site, true
	}

	list := func(w http.ResponseWriter, r *http.Request, p auth.Principal, site string) {
		dbs, err := svc.Databases(r.Context(), site)
		if err != nil {
			dbFail(log, w, err, "list databases")

			return
		}

		sees, err := a.siteFilter(r, p)
		if err != nil {
			a.fail(w, err, "list databases")

			return
		}

		sizes := dbSizes(r, svc, log)

		out := make([]databaseJSON, 0, len(dbs))
		for _, d := range dbs {
			if sees == nil || sees(d.Site) {
				out = append(out, toDatabaseJSON(d).withSize(sizes))
			}
		}

		writeJSON(w, http.StatusOK, map[string][]databaseJSON{"databases": out})
	}

	mux.HandleFunc("GET /api/v1/databases", a.allow("databases:list", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		list(w, r, p, "")
	}))

	mux.HandleFunc("GET /api/v1/sites/{name}/databases", a.allow("databases:read", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		list(w, r, p, r.PathValue("name"))
	}))

	mux.HandleFunc("GET /api/v1/databases/{db}", a.allowSite("databases:read", dbSite, func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		d, err := svc.Database(r.Context(), r.PathValue("db"))
		if err != nil {
			dbFail(log, w, err, "get database")

			return
		}

		writeJSON(w, http.StatusOK, toDatabaseJSON(d).withSize(dbSizes(r, svc, log)))
	}))

	mux.HandleFunc("POST /api/v1/sites/{name}/databases", a.allow("databases:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Name     string `json:"name"`
			Server   string `json:"server"`   // optional, the default server if empty
			User     string `json:"user"`     // optional first user
			Password string `json:"password"` // optional, generated if empty
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		d, pw, err := svc.CreateDatabase(ctx, p.User.ID, hosting.NewDatabase{
			Site: r.PathValue("name"), Name: in.Name, Server: in.Server, User: in.User, Password: in.Password,
		})
		if err != nil {
			dbFail(log, w, err, "create database")

			return
		}

		writeJSON(w, http.StatusCreated, struct {
			databaseJSON
			Password string `json:"password,omitempty"`
		}{toDatabaseJSON(d), pw})
	}))

	mux.HandleFunc("DELETE /api/v1/databases/{db}", a.allowSite("databases:delete", dbSite, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.DeleteDatabase(ctx, p.User.ID, r.PathValue("db")); err != nil {
			dbFail(log, w, err, "delete database")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("POST /api/v1/databases/{db}/users", a.allowSite("databases:write", dbSite, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			User     string `json:"user"`
			Password string `json:"password"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		pw, err := svc.CreateDatabaseUser(ctx, p.User.ID, r.PathValue("db"), in.User, in.Password)
		if err != nil {
			dbFail(log, w, err, "create database user")

			return
		}

		writeJSON(w, http.StatusCreated, map[string]string{"user": in.User, "database": r.PathValue("db"), "password": pw})
	}))

	mux.HandleFunc("PUT /api/v1/databases/{db}/users/{user}/password", a.allowSite("databases:write", dbSite, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Password string `json:"password"` // empty: generate one
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		pw, err := svc.SetDatabaseUserPassword(ctx, p.User.ID, r.PathValue("db"), r.PathValue("user"), in.Password)
		if err != nil {
			dbFail(log, w, err, "set database password")

			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"user": r.PathValue("user"), "password": pw})
	}))

	mux.HandleFunc("DELETE /api/v1/databases/{db}/users/{user}", a.allowSite("databases:write", dbSite, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.DeleteDatabaseUser(ctx, p.User.ID, r.PathValue("db"), r.PathValue("user")); err != nil {
			dbFail(log, w, err, "delete database user")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	// Import and export run as agent jobs (ADR-0025): the answer is 202 with
	// the job, its state comes from the jobs route. The file is relative to
	// the site's home; an export never overwrites a file.
	dump := func(name string, start func(context.Context, int64, string, string) (agentapi.DBJob, error)) func(http.ResponseWriter, *http.Request, auth.Principal) {
		return func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			var in struct {
				Path string `json:"path"`
			}
			if !decodeJSON(w, r, &in) {
				return
			}

			ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
			defer cancel()

			j, err := start(ctx, p.User.ID, r.PathValue("db"), in.Path)
			if err != nil {
				dbFail(log, w, err, name+" database")

				return
			}

			writeJSON(w, http.StatusAccepted, j)
		}
	}

	mux.HandleFunc("POST /api/v1/databases/{db}/import", a.allowSite("databases:write", dbSite, dump("import", svc.ImportDatabase)))
	mux.HandleFunc("POST /api/v1/databases/{db}/export", a.allowSite("databases:write", dbSite, dump("export", svc.ExportDatabase)))

	// The web database tool (ADR-0038): the answer is an address with a
	// one-time ticket for a new tab. It is never cached.
	mux.HandleFunc("GET /api/v1/dbtool", a.allow("databases:list", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		st, err := svc.DBTool(r.Context())
		if err != nil {
			dbFail(log, w, err, "database tool status")

			return
		}

		writeJSON(w, http.StatusOK, st)
	}))

	mux.HandleFunc("POST /api/v1/databases/{db}/tool", a.allowSite("databases:open", dbSite, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Minutes int `json:"minutes"`
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.OpenDatabaseTool(ctx, p.User.ID, r.PathValue("db"), in.Minutes)
		if err != nil {
			dbFail(log, w, err, "open database tool")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusCreated, s)
	}))

	// Open sessions of the tool (ADR-0038 addendum 1): whoever may open a
	// database in the tool may see, extend and end its sessions.
	mux.HandleFunc("GET /api/v1/databases/{db}/tool/sessions", a.allowSite("databases:open", dbSite, func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		list, err := svc.DBToolSessionsOf(ctx, r.PathValue("db"))
		if err != nil {
			dbFail(log, w, err, "list database tool sessions")

			return
		}

		writeJSON(w, http.StatusOK, map[string]any{"sessions": list})
	}))

	mux.HandleFunc("POST /api/v1/databases/{db}/tool/sessions/{id}/extend", a.allowSite("databases:open", dbSite, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		v, err := svc.ExtendDBToolSession(ctx, p.User.ID, r.PathValue("db"), r.PathValue("id"))
		if err != nil {
			dbFail(log, w, err, "extend database tool session")

			return
		}

		writeJSON(w, http.StatusOK, v)
	}))

	mux.HandleFunc("DELETE /api/v1/databases/{db}/tool/sessions/{id}", a.allowSite("databases:open", dbSite, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.EndDBToolSession(ctx, p.User.ID, r.PathValue("db"), r.PathValue("id")); err != nil {
			dbFail(log, w, err, "end database tool session")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("GET /api/v1/databases/{db}/jobs/{id}", a.allowSite("databases:read", dbSite, func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		j, err := svc.DatabaseJob(ctx, r.PathValue("db"), r.PathValue("id"))
		if err != nil {
			dbFail(log, w, err, "database job")

			return
		}

		writeJSON(w, http.StatusOK, j)
	}))
}
