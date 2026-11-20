// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
)

// Firewall is the agent's part of the firewall (ADR-0034).
type Firewall interface {
	Status(ctx context.Context) (agentapi.FirewallStatus, error)
	Apply(ctx context.Context, s agentapi.FirewallState) (agentapi.FirewallStatus, error)
	Confirm(ctx context.Context, id string) (agentapi.FirewallStatus, error)
}

// RPCFirewall talks to the agent.
type RPCFirewall struct{ C *agentrpc.Client }

// Status implements Firewall.
func (f RPCFirewall) Status(ctx context.Context) (agentapi.FirewallStatus, error) {
	return agentapi.FirewallStatusOp.Call(ctx, f.C, agentapi.FirewallStatusRequest{})
}

// Apply implements Firewall.
func (f RPCFirewall) Apply(ctx context.Context, s agentapi.FirewallState) (agentapi.FirewallStatus, error) {
	return agentapi.FirewallApply.Call(ctx, f.C, agentapi.FirewallApplyRequest{State: s})
}

// Confirm implements Firewall.
func (f RPCFirewall) Confirm(ctx context.Context, id string) (agentapi.FirewallStatus, error) {
	return agentapi.FirewallConfirm.Call(ctx, f.C, agentapi.FirewallConfirmRequest{ID: id})
}

// firewallRollbackKey remembers the last rollback written to the audit log.
const firewallRollbackKey = "firewall.rolledBackAudited"

type firewallPendingJSON struct {
	// ID is only in the answer of the PUT that made the change: the
	// browser that sent it confirms with a new request, which proves the
	// admin still reaches the panel through the new rules.
	ID       string    `json:"id,omitempty"`
	Deadline time.Time `json:"deadline"`
}

type firewallJSON struct {
	Enabled    bool                   `json:"enabled"`
	State      agentapi.FirewallState `json:"state"`
	Pending    *firewallPendingJSON   `json:"pending"`
	RolledBack *time.Time             `json:"rolledBack"`
	// ClientIP is the address the panel sees for this request, for the
	// allowlists ("add my address").
	ClientIP string `json:"clientIp"`
	// ConfirmWithin is how long a change waits for its confirmation.
	ConfirmWithin int `json:"confirmWithin"`
}

func toFirewallJSON(st agentapi.FirewallStatus, r *http.Request, withID bool) firewallJSON {
	out := firewallJSON{Enabled: st.Enabled, State: st.State, ClientIP: clientIP(r), ConfirmWithin: agentapi.FirewallConfirmWithin}

	if out.State.Open == nil {
		out.State.Open = []agentapi.FirewallRule{}
	}

	if out.State.SSHAllow == nil {
		out.State.SSHAllow = []string{}
	}

	if out.State.PanelAllow == nil {
		out.State.PanelAllow = []string{}
	}

	if st.Pending != nil {
		out.Pending = &firewallPendingJSON{Deadline: time.Unix(st.Pending.Deadline, 0).UTC()}
		if withID {
			out.Pending.ID = st.Pending.ID
		}
	}

	if st.RolledBack > 0 {
		t := time.Unix(st.RolledBack, 0).UTC()
		out.RolledBack = &t
	}

	return out
}

type firewallPutJSON struct {
	State agentapi.FirewallState `json:"state"`
	// OtherWay says the admin knowingly sets an allowlist without the
	// address of this request: they reach the server another way.
	OtherWay bool `json:"otherWay"`
}

func firewallFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	var ae *agentrpc.Error

	switch {
	case errors.As(err, &ae) && ae.Code == agentrpc.CodePrecondition:
		writeError(w, http.StatusConflict, "firewall_unavailable", ae.Message)
	case errors.As(err, &ae) && ae.Code == agentrpc.CodeNotFound:
		writeError(w, http.StatusNotFound, "not_pending", "this change is no longer pending: it was rolled back or never made")
	case errors.As(err, &ae) && ae.Code == agentrpc.CodeInvalidArgument:
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", ae.Message)
	case errors.As(err, &ae):
		log.Warn(what+": agent refused", "code", ae.Code, "message", ae.Message)
		writeError(w, http.StatusBadGateway, "agent_error", "the firewall could not be changed; the previous rules stay")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "agent_timeout", "the agent did not answer in time")
	default:
		log.Error(what+" failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "the firewall could not be reached, try again")
	}
}

// lockoutGuard refuses an allowlist that leaves out the address of the
// request that sets it (ADR-0034). Loopback always passes.
func lockoutGuard(s agentapi.FirewallState, ip string) (code, msg string) {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return "", ""
	}

	switch {
	case !agentapi.FirewallAllowed(s.PanelAllow, addr):
		return "lockout_panel", "the panel allowlist does not contain your address " + addr.Unmap().String() + "; add it, or confirm that you reach the panel another way"
	case !agentapi.FirewallAllowed(s.SSHAllow, addr):
		return "lockout_ssh", "the SSH allowlist does not contain your address " + addr.Unmap().String() + "; add it, or confirm that you reach SSH another way"
	}

	return "", ""
}

func countDetail(s agentapi.FirewallState) map[string]string {
	n := 0
	for _, r := range s.Open {
		n += len(r.Sources)
	}

	return map[string]string{
		"open": strconv.Itoa(len(s.Open)), "openSources": strconv.Itoa(n),
		"sshAllow": strconv.Itoa(len(s.SSHAllow)), "panelAllow": strconv.Itoa(len(s.PanelAllow)),
	}
}

// registerFirewall adds GET and PUT /api/v1/firewall and POST
// /api/v1/firewall/confirm (ADR-0034). Only administrators call them.
func registerFirewall(mux *http.ServeMux, a authn, fw Firewall, st Settings, log *slog.Logger) {
	admin := func(scope string, h func(http.ResponseWriter, *http.Request, auth.Principal)) http.HandlerFunc {
		return a.scoped(scope, func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
			if p.User.Role != "admin" {
				writeError(w, http.StatusForbidden, "forbidden", "only administrators can change the firewall")

				return
			}

			h(w, r, p)
		})
	}

	audit := func(ctx context.Context, uid *int64, action string, detail map[string]string) {
		if err := st.Audit(ctx, uid, action, "", detail); err != nil {
			log.ErrorContext(ctx, "audit log", "action", action, "err", err)
		}
	}

	// noteRollback writes a rollback the agent made into the audit log,
	// once: the agent rolls back on its own, the panel learns of it here.
	noteRollback := func(ctx context.Context, s agentapi.FirewallStatus) {
		if s.RolledBack == 0 {
			return
		}

		at := strconv.FormatInt(s.RolledBack, 10)

		v, ok, err := st.Setting(ctx, firewallRollbackKey)
		if err != nil {
			return
		}

		var last string
		if ok {
			_ = json.Unmarshal([]byte(v), &last)
		}

		if last == at {
			return
		}

		b, _ := json.Marshal(at)
		if err := st.SetSetting(ctx, firewallRollbackKey, string(b)); err != nil {
			log.ErrorContext(ctx, "firewall rollback setting", "err", err)

			return
		}

		audit(ctx, nil, "firewall.rollback", map[string]string{"at": time.Unix(s.RolledBack, 0).UTC().Format(time.RFC3339)})
	}

	mux.HandleFunc("GET /api/v1/firewall", admin("firewall:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := fw.Status(ctx)
		if err != nil {
			firewallFail(log, w, err, "firewall status")

			return
		}

		noteRollback(ctx, s)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, toFirewallJSON(s, r, false))
	}))

	mux.HandleFunc("PUT /api/v1/firewall", admin("firewall:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in firewallPutJSON
		if !decodeJSON(w, r, &in) {
			return
		}

		if err := in.State.Validate(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())

			return
		}

		if code, msg := lockoutGuard(in.State, clientIP(r)); code != "" && !in.OtherWay {
			writeError(w, http.StatusConflict, code, msg)

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := fw.Apply(ctx, in.State)
		if err != nil {
			firewallFail(log, w, err, "firewall change")

			return
		}

		uid := p.User.ID
		detail := countDetail(s.State)
		detail["ip"] = clientIP(r)
		detail["otherWay"] = strconv.FormatBool(in.OtherWay)

		if s.Pending != nil {
			detail["id"] = s.Pending.ID
		}

		audit(r.Context(), &uid, "firewall.change", detail)
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, toFirewallJSON(s, r, true))
	}))

	mux.HandleFunc("POST /api/v1/firewall/confirm", admin("firewall:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		var in struct {
			ID string `json:"id"`
		}

		if !decodeJSON(w, r, &in) {
			return
		}

		if err := (agentapi.FirewallConfirmRequest{ID: in.ID}).Validate(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := fw.Confirm(ctx, in.ID)
		if err != nil {
			if cur, serr := fw.Status(ctx); serr == nil {
				noteRollback(ctx, cur)
			}

			firewallFail(log, w, err, "firewall confirm")

			return
		}

		uid := p.User.ID
		audit(r.Context(), &uid, "firewall.confirm", map[string]string{"id": in.ID, "ip": clientIP(r)})
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, toFirewallJSON(s, r, false))
	}))
}
