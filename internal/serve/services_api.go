// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/openapi"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/servermon"
	"github.com/maki-it/hostashi/internal/store"
)

// ServerMonitor is the panel's part of service management and the
// dashboard (ADR-0052, internal/servermon).
type ServerMonitor interface {
	Services(ctx context.Context, site string) ([]servermon.Service, error)
	Action(ctx context.Context, userID int64, req agentapi.ServiceActionRequest) (agentapi.ServiceState, error)
	Events(ctx context.Context, f store.ServiceEventFilter) ([]store.ServiceEvent, error)
	Series(ctx context.Context, rng string) ([]store.HostSample, int, error)
}

// RPCServerAgent is servermon.Agent over the agent socket.
type RPCServerAgent struct{ C *agentrpc.Client }

// HostStats implements servermon.Agent.
func (a RPCServerAgent) HostStats(ctx context.Context) (agentapi.HostStats, error) {
	return agentapi.HostStatsOp.Call(ctx, a.C, agentapi.HostStatsRequest{})
}

// Services implements servermon.Agent.
func (a RPCServerAgent) Services(ctx context.Context, req agentapi.ServicesStatusRequest) ([]agentapi.ServiceState, error) {
	r, err := agentapi.ServicesStatus.Call(ctx, a.C, req)

	return r.Services, err
}

// Action implements servermon.Agent.
func (a RPCServerAgent) Action(ctx context.Context, req agentapi.ServiceActionRequest) (agentapi.ServiceState, error) {
	return agentapi.ServiceAction.Call(ctx, a.C, req)
}

// serviceActionTimeout covers a slow start of MariaDB [E].
const serviceActionTimeout = 2 * time.Minute

type serviceEventJSON struct {
	At       time.Time `json:"at"`
	Service  string    `json:"service"`
	Site     string    `json:"site,omitempty"`
	Kind     string    `json:"kind"`
	Detail   string    `json:"detail,omitempty"`
	Restarts int       `json:"restarts,omitempty"`
	User     string    `json:"user,omitempty"`
}

func toServiceEvents(in []store.ServiceEvent) []serviceEventJSON {
	out := make([]serviceEventJSON, 0, len(in))
	for _, e := range in {
		out = append(out, serviceEventJSON{At: e.At, Service: e.Service, Site: e.Site, Kind: e.Kind, Detail: e.Detail, Restarts: e.Restarts, User: e.UserName})
	}

	return out
}

// serviceActionJSON is the body of an action. Confirm must be "<action>
// <service>" (for example "stop mariadb") where the catalog asks for it.
type serviceActionJSON struct {
	Confirm string `json:"confirm"`
}

func writeServiceAgentError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	var ae *agentrpc.Error

	switch {
	case errors.As(err, &ae) && ae.Code == agentrpc.CodeInvalidArgument:
		writeError(w, http.StatusBadRequest, "invalid_argument", ae.Message)
	case errors.As(err, &ae) && ae.Code == agentrpc.CodeNotFound:
		writeError(w, http.StatusNotFound, "not_found", ae.Message)
	case errors.As(err, &ae) && (ae.Code == agentrpc.CodePrecondition || ae.Code == agentrpc.CodeConflict):
		writeError(w, http.StatusConflict, "failed_precondition", ae.Message)
	case errors.As(err, &ae) && ae.Code == agentrpc.CodeInternal && ae.Message != "internal error":
		// The agent's own words (for example errAction), not a raw error.
		log.WarnContext(r.Context(), "services", "err", err)
		writeError(w, http.StatusBadGateway, "agent_error", ae.Message)
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "agent_timeout", "the agent did not answer in time")
	default:
		log.WarnContext(r.Context(), "services", "err", err)
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "the agent could not do this, try again")
	}
}

type serviceListJSON struct {
	Services []servermon.Service `json:"services"`
}

type serviceEventsJSON struct {
	Events []serviceEventJSON `json:"events"`
}

// dashboardJSON is the server's load over a range; step is in seconds.
type dashboardJSON struct {
	Range  string             `json:"range"`
	Step   int                `json:"step"`
	Points []store.HostSample `json:"points"`
}

// registerServices adds the service and dashboard routes (ADR-0052). The
// states name units and the server's load, so admins only.
func registerServices(mux *routeMux, a authn, m ServerMonitor, st Settings, log *slog.Logger) {
	list := func(w http.ResponseWriter, r *http.Request, site string) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		rows, err := m.Services(ctx, site)
		if err != nil {
			writeServiceAgentError(w, r, log, err)

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, serviceListJSON{Services: rows})
	}

	mux.Op("GET /api/v1/services", openapi.Operation{
		ID: "listServices", Tag: "services", Summary: "The managed services with state and restarts", Out: serviceListJSON{},
	}, a.allow("services:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		list(w, r, "")
	}))

	mux.Op("GET /api/v1/sites/{name}/services", openapi.Operation{
		ID: "listSiteServices", Tag: "services", Summary: "The services of one site", Out: serviceListJSON{},
	}, a.allow("services:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		site := r.PathValue("name")
		if agentapi.ValidateSiteUser(site) != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such site")

			return
		}

		list(w, r, site)
	}))

	mux.Op("GET /api/v1/services/events", openapi.Operation{
		ID: "listServiceEvents", Tag: "services", Summary: "Recent service events: failures, restarts, actions", Out: serviceEventsJSON{}, Query: []openapi.Param{{Name: "service"}, {Name: "site"}, {Name: "limit", Type: "integer"}},
	}, a.allow("services:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		q := r.URL.Query()
		f := store.ServiceEventFilter{Service: q.Get("service"), Site: q.Get("site"), Since: time.Now().Add(-store.ServiceEventsKeep)}

		if f.Service != "" {
			if err := agentapi.ValidateServiceRef(f.Service, f.Site); err != nil {
				writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())

				return
			}
		} else if f.Site != "" {
			writeError(w, http.StatusBadRequest, "invalid_argument", "site needs a service")

			return
		}

		if v := q.Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 1000 {
				writeError(w, http.StatusBadRequest, "invalid_argument", "limit must be 1 to 1000")

				return
			}

			f.Limit = n
		}

		ev, err := m.Events(r.Context(), f)
		if err != nil {
			log.ErrorContext(r.Context(), "service events", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "the events could not be read")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, serviceEventsJSON{Events: toServiceEvents(ev)})
	}))

	act := func(w http.ResponseWriter, r *http.Request, p auth.Principal, site string) {
		req := agentapi.ServiceActionRequest{ID: r.PathValue("id"), Site: site, Action: r.PathValue("action")}
		if err := req.Validate(); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())

			return
		}

		var in serviceActionJSON
		if r.ContentLength != 0 && !decodeJSON(w, r, &in) {
			return
		}

		svc, _ := agentapi.LookupService(req.ID)
		if slices.Contains(svc.Confirm, req.Action) && in.Confirm != req.Action+" "+req.ID {
			writeError(w, http.StatusBadRequest, "confirm_required", "type \""+req.Action+" "+req.ID+"\" to confirm")

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), serviceActionTimeout)
		defer cancel()

		uid := p.User.ID
		detail := map[string]string{"service": req.ID, "action": req.Action}

		if site != "" {
			detail["site"] = site
		}

		res, err := m.Action(ctx, uid, req)
		if err != nil {
			detail["result"] = "error"
			if aerr := st.Audit(r.Context(), &uid, "service.action", req.ID, detail); aerr != nil {
				log.ErrorContext(r.Context(), "audit log", "action", "service.action", "err", aerr)
			}

			writeServiceAgentError(w, r, log, err)

			return
		}

		detail["result"] = "ok"
		if aerr := st.Audit(r.Context(), &uid, "service.action", req.ID, detail); aerr != nil {
			log.ErrorContext(r.Context(), "audit log", "action", "service.action", "err", aerr)
		}

		writeJSON(w, http.StatusOK, res)
	}

	mux.Op("POST /api/v1/services/{id}/{action}", openapi.Operation{
		ID: "serviceAction", Tag: "services", Summary: "Start, stop, restart or reload a service", In: serviceActionJSON{}, Out: agentapi.ServiceState{},
	}, a.allow("services:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		act(w, r, p, "")
	}))

	mux.Op("POST /api/v1/sites/{name}/services/{id}/{action}", openapi.Operation{
		ID: "siteServiceAction", Tag: "services", Summary: "Start, stop, restart or reload a service of a site", In: serviceActionJSON{}, Out: agentapi.ServiceState{},
	}, a.allow("services:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		site := r.PathValue("name")
		if agentapi.ValidateSiteUser(site) != nil {
			writeError(w, http.StatusNotFound, "not_found", "no such site")

			return
		}

		act(w, r, p, site)
	}))

	mux.Op("GET /api/v1/dashboard", openapi.Operation{
		ID: "getDashboard", Tag: "services", Summary: "CPU, memory, disk and load of the server over a range", Out: dashboardJSON{}, Query: []openapi.Param{{Name: "range", Description: "e.g. 1h, 24h, 7d"}},
	}, a.allow("services:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		rng := r.URL.Query().Get("range")
		if rng == "" {
			rng = servermon.Range1h
		}

		pts, step, err := m.Series(r.Context(), rng)
		if errors.Is(err, servermon.ErrRange) {
			writeError(w, http.StatusBadRequest, "invalid_argument", "range must be 1h, 24h, 7d or 30d")

			return
		}

		if err != nil {
			log.ErrorContext(r.Context(), "dashboard", "err", err)
			writeError(w, http.StatusInternalServerError, "internal", "the samples could not be read")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, dashboardJSON{Range: rng, Step: step, Points: pts})
	}))
}
