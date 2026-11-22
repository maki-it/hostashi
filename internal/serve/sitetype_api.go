// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/hosting"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

// siteTypeRequest is a new type for a site with the settings of that type
// (ADR-0054 addendum 3), the same fields as on create: php for php,
// redirect for redirect, proxy for proxy, node for node, app for app,
// python for python; static takes none.
type siteTypeRequest struct {
	Type     string          `json:"type"`
	PHP      string          `json:"php"`
	Redirect *redirectJSON   `json:"redirect"`
	Proxy    *proxyJSON      `json:"proxy"`
	Node     *nodeAppJSON    `json:"node"`
	App      *programAppJSON `json:"app"`
	Python   *pythonAppJSON  `json:"python"`
}

func (in siteTypeRequest) request() (hosting.TypeRequest, bool) {
	out := hosting.TypeRequest{Type: in.Type, PHP: in.PHP}

	if in.Redirect != nil {
		out.Redirect = &store.SiteRedirect{Target: in.Redirect.Target, Status: in.Redirect.Status, KeepPath: in.Redirect.KeepPath}
	}

	if in.Proxy != nil {
		out.Proxy = &store.SiteProxy{Target: in.Proxy.Target, TargetHost: in.Proxy.TargetHost, Insecure: in.Proxy.Insecure}
	}

	n := 0

	for _, set := range []bool{in.Node != nil, in.App != nil, in.Python != nil} {
		if set {
			n++
		}
	}

	switch {
	case n > 1:
		return out, false
	case in.Node != nil:
		if in.Type != store.SiteTypeNode || in.Node.Version == "" {
			return out, false
		}

		a := in.Node.app()
		out.App = &a
	case in.App != nil:
		if in.Type != store.SiteTypeApp {
			return out, false
		}

		a := in.App.app()
		out.App = &a
	case in.Python != nil:
		if in.Type != store.SiteTypePython || in.Python.Version == "" {
			return out, false
		}

		a := in.Python.app()
		out.App = &a
	}

	return out, true
}

// registerSiteType adds the routes that change the type of a site and
// remove the account a redirect or proxy site kept.
func registerSiteType(mux *routeMux, a authn, svc *hosting.Service, log *slog.Logger) {
	mux.Op("POST /api/v1/sites/{name}/type", openapi.Operation{
		ID: "setSiteType", Tag: "sites", Summary: "Change the type of a site; the account and its files stay",
		In: siteTypeRequest{}, Out: siteJSON{},
	}, a.allow("sites.type:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in siteTypeRequest
		if !decodeJSON(w, r, &in) {
			return
		}

		req, ok := in.request()
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", "node needs type node and node.version, app needs type app, python needs type python and python.version")

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.SetType(ctx, p.User.ID, r.PathValue("name"), req)
		if err != nil {
			siteFail(log, w, err, "set site type")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))

	mux.Op("DELETE /api/v1/sites/{name}/account", openapi.Operation{
		ID: "removeSiteAccount", Tag: "sites", Summary: "Remove the Linux account, files and databases a redirect or reverse proxy site kept after a type change",
		Out: siteJSON{},
	}, a.allow("sites.account:delete", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := svc.RemoveAccount(ctx, p.User.ID, r.PathValue("name"))
		if err != nil {
			siteFail(log, w, err, "remove site account")

			return
		}

		writeJSON(w, http.StatusOK, toSiteJSON(svc, s))
	}))
}
