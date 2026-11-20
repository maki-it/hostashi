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
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/store"
)

type databaseJSON struct {
	Name      string    `json:"name"`
	Site      string    `json:"site"`
	Users     []string  `json:"users"`
	CreatedAt time.Time `json:"createdAt"`
}

func toDatabaseJSON(d store.Database) databaseJSON {
	u := d.Users
	if u == nil {
		u = []string{}
	}

	return databaseJSON{Name: d.Name, Site: d.Site, Users: u, CreatedAt: d.CreatedAt}
}

// dbFail maps the database errors and hands the rest to siteFail.
func dbFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, hosting.ErrDatabaseNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such database or database user")
	case errors.Is(err, hosting.ErrJobNotFound):
		writeError(w, http.StatusNotFound, "not_found", "no such job for this database")
	case errors.Is(err, hosting.ErrDatabaseExists):
		writeError(w, http.StatusConflict, "database_exists", "a database or database user with this name exists")
	default:
		siteFail(log, w, err, what)
	}
}

// registerDatabases adds the database routes (ADR-0024). A password is in a
// response only when the panel generated it; the panel stores none.
func registerDatabases(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	list := func(w http.ResponseWriter, r *http.Request, site string) {
		dbs, err := svc.Databases(r.Context(), site)
		if err != nil {
			dbFail(log, w, err, "list databases")

			return
		}

		out := make([]databaseJSON, 0, len(dbs))
		for _, d := range dbs {
			out = append(out, toDatabaseJSON(d))
		}

		writeJSON(w, http.StatusOK, map[string][]databaseJSON{"databases": out})
	}

	mux.HandleFunc("GET /api/v1/databases", a.allow("databases:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		list(w, r, "")
	}))

	mux.HandleFunc("GET /api/v1/sites/{name}/databases", a.allow("databases:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		list(w, r, r.PathValue("name"))
	}))

	mux.HandleFunc("GET /api/v1/databases/{db}", a.allow("databases:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
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
			User     string `json:"user"`     // optional first user
			Password string `json:"password"` // optional, generated if empty
		}
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		d, pw, err := svc.CreateDatabase(ctx, p.User.ID, hosting.NewDatabase{
			Site: r.PathValue("name"), Name: in.Name, User: in.User, Password: in.Password,
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

	mux.HandleFunc("DELETE /api/v1/databases/{db}", a.allow("databases:delete", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		if err := svc.DeleteDatabase(ctx, p.User.ID, r.PathValue("db")); err != nil {
			dbFail(log, w, err, "delete database")

			return
		}

		w.WriteHeader(http.StatusNoContent)
	}))

	mux.HandleFunc("POST /api/v1/databases/{db}/users", a.allow("databases:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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

	mux.HandleFunc("PUT /api/v1/databases/{db}/users/{user}/password", a.allow("databases:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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

	mux.HandleFunc("DELETE /api/v1/databases/{db}/users/{user}", a.allow("databases:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
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

	mux.HandleFunc("POST /api/v1/databases/{db}/import", a.allow("databases:write", dump("import", svc.ImportDatabase)))
	mux.HandleFunc("POST /api/v1/databases/{db}/export", a.allow("databases:write", dump("export", svc.ExportDatabase)))

	mux.HandleFunc("GET /api/v1/databases/{db}/jobs/{id}", a.allow("databases:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
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
