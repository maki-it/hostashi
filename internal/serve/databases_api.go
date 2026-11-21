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
}

func toDatabaseJSON(d store.Database) databaseJSON {
	u := d.Users
	if u == nil {
		u = []string{}
	}

	return databaseJSON{Name: d.Name, Site: d.Site, Server: d.Server, Users: u, CreatedAt: d.CreatedAt}
}

// dbFail maps the database errors and hands the rest to siteFail.
func dbFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrDatabaseNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such database or database user")
	case errors.Is(err, hosting.ErrJobNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such job for this database")
	case errors.Is(err, hosting.ErrNoDBTool):
		writeError(w, http.StatusConflict, "dbtool_unavailable", "the database tool is not available on this server")
	case errors.Is(err, hosting.ErrDatabaseExists):
		writeError(w, http.StatusConflict, "database_exists", "a database or database user with this name exists")
	case errors.Is(err, hosting.ErrDBServerLocalOnly):
		writeError(w, http.StatusConflict, "db_server_local_only", "this works only for databases on the local server so far")
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

		out := make([]databaseJSON, 0, len(dbs))
		for _, d := range dbs {
			if sees == nil || sees(d.Site) {
				out = append(out, toDatabaseJSON(d))
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

		writeJSON(w, http.StatusOK, toDatabaseJSON(d))
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
