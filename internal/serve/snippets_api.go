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
	"github.com/maki-it/hostashi/internal/cloudpanel/snippetdraft"
	"github.com/maki-it/hostashi/internal/cpmigrate"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
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
	Problems agentapi.SnippetCheckResponse `json:"problems,omitzero"`
}

type snippetsRequest struct {
	Server *string `json:"server"`
	Root   *string `json:"root"`
}

// maxSnippetBody fits two snippets of the maximum size with JSON escaping.
const maxSnippetBody = 4*agentapi.MaxSnippetBytes + 1024

func registerSnippets(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/sites/{name}/snippets", openapi.Operation{
		ID: "getSiteSnippets", Tag: "sites", Summary: "Get the site's own nginx snippets", Out: snippetsJSON{},
	}, a.allow("sites:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		sn, err := svc.Snippets(r.Context(), r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "read snippets")

			return
		}

		writeJSON(w, http.StatusOK, toSnippetsJSON(sn))
	}))

	mux.Op("PUT /api/v1/sites/{name}/snippets", openapi.Operation{
		ID: "setSiteSnippets", Tag: "sites", Summary: "Set the site's own nginx snippets", In: snippetsRequest{}, Out: snippetsJSON{}, Errors: map[int]any{http.StatusUnprocessableEntity: snippetErrorJSON{}},
	}, a.allow("sites.snippets:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in snippetsRequest
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
			headersFail(log, w, err, "set snippets")

			return
		}

		writeJSON(w, http.StatusOK, toSnippetsJSON(sn))
	}))
}

// registerSnippetDraft adds the draft of a migrated site's snippets
// (ADR-0017 Part 2, ADR-0029): the custom directives of the stored scan,
// pointed at the site of the same name on this server and checked, with
// what happened to each entry. Nothing is saved; the person reviews the
// draft and saves it with PUT /api/v1/sites/{name}/snippets. The draft
// holds snippet text, which may carry credentials, so it is only built for
// a site that exists here.
func registerSnippetDraft(mux *routeMux, a authn, mig *cpmigrate.Service, svc *hosting.Service, log *slog.Logger) {
	mux.Op("GET /api/v1/migrations/cloudpanel/reports/{id}/sites/{name}/snippets-draft", openapi.Operation{
		ID: "getCloudPanelSnippetsDraft", Tag: "migrations", Summary: "Draft of the site's own nginx snippets from the report", Out: snippetdraft.Result{},
	}, a.allow("migrations:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		site, err := mig.ReportSite(r.Context(), r.PathValue("id"), r.PathValue("name"))
		if err != nil {
			importFail(log, w, err, "snippet draft")

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		target, err := svc.SnippetTarget(ctx, site.User)
		if err != nil {
			if errors.Is(err, hosting.ErrNotFound) {
				writeError(w, http.StatusConflict, "site_missing", "create the site on this server first")

				return
			}

			siteFail(log, w, err, "snippet draft")

			return
		}

		writeJSON(w, http.StatusOK, snippetdraft.Draft(site, target))
	}))
}
