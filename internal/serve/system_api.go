// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/openapi"
	"github.com/maki-it/hostashi/internal/store"
)

// SystemAgent is the agent's part of the server settings (ADR-0060).
type SystemAgent interface {
	Info(ctx context.Context) (agentapi.SystemInfo, error)
	Timezones(ctx context.Context) (agentapi.SystemTimezones, error)
	SetTimezone(ctx context.Context, zone string) (agentapi.SystemInfo, error)
	SetHostname(ctx context.Context, name string) (agentapi.SystemInfo, error)
	Reboot(ctx context.Context) (agentapi.SystemRebootResponse, error)
	SetAutoUpdate(ctx context.Context, on bool) (agentapi.SystemInfo, error)
}

// RPCSystemAgent is SystemAgent over the agent socket.
type RPCSystemAgent struct{ C *agentrpc.Client }

// Info implements SystemAgent.
func (a RPCSystemAgent) Info(ctx context.Context) (agentapi.SystemInfo, error) {
	return agentapi.SystemInfoOp.Call(ctx, a.C, agentapi.SystemInfoRequest{})
}

// Timezones implements SystemAgent.
func (a RPCSystemAgent) Timezones(ctx context.Context) (agentapi.SystemTimezones, error) {
	return agentapi.SystemTimezonesOp.Call(ctx, a.C, agentapi.SystemTimezonesRequest{})
}

// SetTimezone implements SystemAgent.
func (a RPCSystemAgent) SetTimezone(ctx context.Context, zone string) (agentapi.SystemInfo, error) {
	return agentapi.SystemTimezoneSet.Call(ctx, a.C, agentapi.SystemTimezoneRequest{Zone: zone})
}

// SetHostname implements SystemAgent.
func (a RPCSystemAgent) SetHostname(ctx context.Context, name string) (agentapi.SystemInfo, error) {
	return agentapi.SystemHostnameSet.Call(ctx, a.C, agentapi.SystemHostnameRequest{Name: name})
}

// Reboot implements SystemAgent.
func (a RPCSystemAgent) Reboot(ctx context.Context) (agentapi.SystemRebootResponse, error) {
	return agentapi.SystemReboot.Call(ctx, a.C, agentapi.SystemRebootRequest{})
}

// SetAutoUpdate implements SystemAgent.
func (a RPCSystemAgent) SetAutoUpdate(ctx context.Context, on bool) (agentapi.SystemInfo, error) {
	return agentapi.SystemAutoUpdateSet.Call(ctx, a.C, agentapi.SystemAutoUpdateRequest{On: on})
}

type systemAutoUpdateJSON struct {
	On bool `json:"on"`
}

type systemTimezoneJSON struct {
	Zone string `json:"zone"`
}

type systemHostnameJSON struct {
	Hostname string `json:"hostname"`
}

// systemRebootJSON carries the confirmation: the server's hostname, typed
// by the admin.
type systemRebootJSON struct {
	Confirm string `json:"confirm"`
}

// registerSystem adds the server settings routes (ADR-0060): admins only.
func registerSystem(mux *routeMux, a authn, sys SystemAgent, st Settings, log *slog.Logger) {
	audit := func(r *http.Request, uid int64, action, target string, detail map[string]string) {
		if err := st.Audit(r.Context(), &uid, action, target, detail); err != nil {
			log.ErrorContext(r.Context(), "audit log", "action", action, "err", err)
		}
	}

	mux.Op("GET /api/v1/system", openapi.Operation{
		ID: "getSystem", Tag: "system", Summary: "The server's hostname, time zone, clock, uptime and pending reboot",
		Out: agentapi.SystemInfo{},
	}, a.allow("system:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		info, err := sys.Info(ctx)
		if err != nil {
			writeServiceAgentError(w, r, log, err)

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, info)
	}))

	mux.Op("GET /api/v1/system/timezones", openapi.Operation{
		ID: "listTimezones", Tag: "system", Summary: "The time zones the server knows",
		Out: agentapi.SystemTimezones{},
	}, a.allow("system:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		zones, err := sys.Timezones(ctx)
		if err != nil {
			writeServiceAgentError(w, r, log, err)

			return
		}

		writeJSON(w, http.StatusOK, zones)
	}))

	// change runs one setter and writes the audit entry with the old and
	// the new value of field.
	change := func(w http.ResponseWriter, r *http.Request, p auth.Principal, action, field, value string, old func(agentapi.SystemInfo) string, set func(context.Context) (agentapi.SystemInfo, error)) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		before := map[string]string{}
		if info, err := sys.Info(ctx); err == nil {
			before[field] = old(info)
		}

		diff := store.DiffOf(before, map[string]string{field: value})
		uid := p.User.ID

		info, err := set(ctx)
		if err != nil {
			if aerr := st.AuditChange(r.Context(), &uid, action, value, map[string]string{"result": "error"}, diff); aerr != nil {
				log.ErrorContext(r.Context(), "audit log", "action", action, "err", aerr)
			}

			writeServiceAgentError(w, r, log, err)

			return
		}

		if aerr := st.AuditChange(r.Context(), &uid, action, value, map[string]string{"result": "ok"}, diff); aerr != nil {
			log.ErrorContext(r.Context(), "audit log", "action", action, "err", aerr)
		}

		writeJSON(w, http.StatusOK, info)
	}

	mux.Op("PUT /api/v1/system/timezone", openapi.Operation{
		ID: "setTimezone", Tag: "system", Summary: "Set the server's time zone (site cron jobs follow it at once)",
		In: systemTimezoneJSON{}, Out: agentapi.SystemInfo{},
	}, a.allow("system:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in systemTimezoneJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		if err := agentapi.ValidateTimezone(in.Zone); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())

			return
		}

		change(w, r, p, "system.timezone", "timezone", in.Zone, func(i agentapi.SystemInfo) string { return i.Timezone },
			func(ctx context.Context) (agentapi.SystemInfo, error) { return sys.SetTimezone(ctx, in.Zone) })
	}))

	mux.Op("PUT /api/v1/system/hostname", openapi.Operation{
		ID: "setHostname", Tag: "system", Summary: "Set the server's hostname",
		In: systemHostnameJSON{}, Out: agentapi.SystemInfo{},
	}, a.allow("system:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in systemHostnameJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		if err := agentapi.ValidateHostname(in.Hostname); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_argument", err.Error())

			return
		}

		change(w, r, p, "system.hostname", "hostname", in.Hostname, func(i agentapi.SystemInfo) string { return i.Hostname },
			func(ctx context.Context) (agentapi.SystemInfo, error) { return sys.SetHostname(ctx, in.Hostname) })
	}))

	mux.Op("PUT /api/v1/system/auto-update", openapi.Operation{
		ID: "setAutoUpdate", Tag: "system", Summary: "Turn the automatic installation of new Hostashi versions on or off",
		In: systemAutoUpdateJSON{}, Out: agentapi.SystemInfo{},
	}, a.allow("system:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in systemAutoUpdateJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		onOff := func(on bool) string {
			if on {
				return "on"
			}

			return "off"
		}

		change(w, r, p, "system.auto_update", "auto_update", onOff(in.On), func(i agentapi.SystemInfo) string { return onOff(i.Updates != nil && i.Updates.Auto) },
			func(ctx context.Context) (agentapi.SystemInfo, error) { return sys.SetAutoUpdate(ctx, in.On) })
	}))

	mux.Op("POST /api/v1/system/reboot", openapi.Operation{
		ID: "rebootServer", Tag: "system", Summary: "Reboot the server; confirm with its hostname",
		In: systemRebootJSON{}, Out: agentapi.SystemRebootResponse{}, Status: http.StatusAccepted,
	}, a.allow("system:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in systemRebootJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		info, err := sys.Info(ctx)
		if err != nil {
			writeServiceAgentError(w, r, log, err)

			return
		}

		if in.Confirm != info.Hostname {
			writeError(w, http.StatusBadRequest, "confirm_required", "type the server's hostname \""+info.Hostname+"\" to confirm")

			return
		}

		detail := map[string]string{"hostname": info.Hostname, "reboot_required": "no"}
		if info.RebootRequired {
			detail["reboot_required"] = "yes"
			if len(info.RebootPackages) > 0 {
				detail["packages"] = strings.Join(info.RebootPackages, " ")
			}
		}

		// The entry is written before the call: after it the panel may be
		// gone before it could write anything.
		detail["result"] = "requested"
		audit(r, p.User.ID, "system.reboot", info.Hostname, detail)

		res, err := sys.Reboot(ctx)
		if err != nil {
			detail["result"] = "error"
			audit(r, p.User.ID, "system.reboot", info.Hostname, detail)
			writeServiceAgentError(w, r, log, err)

			return
		}

		writeJSON(w, http.StatusAccepted, res)
	}))
}
