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

// snippetsJSON are the custom nginx snippets of a site (ADR-0029).
type snippetsJSON struct {
	Server    string     `json:"server"`
	Root      string     `json:"root"`
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
}

func toSnippetsJSON(sn store.SiteSnippets) snippetsJSON {
	out := snippetsJSON{Server: sn.Server, Root: sn.Root}
	if !sn.Empty() {
		at := sn.UpdatedAt
		out.UpdatedAt = &at
	}

	return out
}

// snippetErrorJSON answers rejected snippets with every problem per slot.
type snippetErrorJSON struct {
	Error    apiErrorBody                  `json:"error"`
	Problems agentapi.SnippetCheckResponse `json:"problems"`
}

// maxSnippetBody fits two snippets of the maximum size with JSON escaping.
const maxSnippetBody = 4*agentapi.MaxSnippetBytes + 1024

func registerSnippets(mux *http.ServeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	admin := func(scope string, h func(http.ResponseWriter, *http.Request, auth.Principal)) http.HandlerFunc {
		return a.scoped(scope, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			if p.User.Role != "admin" {
				writeError(w, http.StatusForbidden, "forbidden", "only administrators can manage sites")

				return
			}

			h(w, r, p)
		})
	}

	mux.HandleFunc("GET /api/v1/sites/{name}/snippets", admin("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		sn, err := svc.Snippets(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "read snippets")

			return
		}

		writeJSON(w, http.StatusOK, toSnippetsJSON(sn))
	}))

	mux.HandleFunc("PUT /api/v1/sites/{name}/snippets", admin("sites.snippets:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			Server *string `json:"server"`
			Root   *string `json:"root"`
		}
		if !decodeJSONLimit(w, r, &in, maxSnippetBody) {
			return
		}

		if in.Server == nil || in.Root == nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", `"server" and "root" are required (empty strings clear them)`)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		sn, err := svc.SetSnippets(ctx, p.User.ID, r.PathValue("name"), *in.Server, *in.Root)

		var se *hosting.SnippetError
		if errors.As(err, &se) {
			writeJSON(w, http.StatusUnprocessableEntity, snippetErrorJSON{
				Error:    apiErrorBody{Code: "snippet_rejected", Message: "a snippet contains directives that are not allowed", RequestID: w.Header().Get(RequestIDHeader)},
				Problems: se.Problems,
			})

			return
		}

		if err != nil {
			siteFail(log, w, err, "set snippets")

			return
		}

		writeJSON(w, http.StatusOK, toSnippetsJSON(sn))
	}))
}
