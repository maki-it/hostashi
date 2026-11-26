// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/openapi"
)

// Fail2ban is the agent's view of fail2ban's Hostashi jails (ADR-0078).
type Fail2ban interface {
	Status(ctx context.Context) (agentapi.Fail2banStatus, error)
	Unban(ctx context.Context, address string) (agentapi.Fail2banUnbanResponse, error)
}

// RPCFail2ban talks to the agent.
type RPCFail2ban struct{ C *agentrpc.Client }

// Status implements Fail2ban.
func (f RPCFail2ban) Status(ctx context.Context) (agentapi.Fail2banStatus, error) {
	return agentapi.Fail2banStatusOp.Call(ctx, f.C, agentapi.Fail2banStatusRequest{})
}

// Unban implements Fail2ban.
func (f RPCFail2ban) Unban(ctx context.Context, address string) (agentapi.Fail2banUnbanResponse, error) {
	return agentapi.Fail2banUnban.Call(ctx, f.C, agentapi.Fail2banUnbanRequest{Address: address})
}

type fail2banBanJSON struct {
	Address string    `json:"address"`
	Since   time.Time `json:"since"`
	// Until is null for a ban without end.
	Until *time.Time `json:"until"`
}

type fail2banJailJSON struct {
	Name            string            `json:"name"`
	Running         bool              `json:"running"`
	CurrentlyFailed int               `json:"currentlyFailed"`
	TotalFailed     int               `json:"totalFailed"`
	CurrentlyBanned int               `json:"currentlyBanned"`
	TotalBanned     int               `json:"totalBanned"`
	Bans            []fail2banBanJSON `json:"bans"`
}

type fail2banJSON struct {
	Jails []fail2banJailJSON `json:"jails"`
}

type fail2banUnbanJSON struct {
	Address string `json:"address"`
	// Jails had banned the address and no longer do.
	Jails []string `json:"jails"`
}

func toFail2banJSON(st agentapi.Fail2banStatus) fail2banJSON {
	out := fail2banJSON{Jails: make([]fail2banJailJSON, 0, len(st.Jails))}

	for _, j := range st.Jails {
		jj := fail2banJailJSON{
			Name: j.Name, Running: j.Running, CurrentlyFailed: j.CurrentlyFailed, TotalFailed: j.TotalFailed,
			CurrentlyBanned: j.CurrentlyBanned, TotalBanned: j.TotalBanned, Bans: make([]fail2banBanJSON, 0, len(j.Bans)),
		}

		for _, b := range j.Bans {
			bj := fail2banBanJSON{Address: b.Address, Since: time.Unix(b.Since, 0).UTC()}
			if b.Until != 0 {
				u := time.Unix(b.Until, 0).UTC()
				bj.Until = &u
			}

			jj.Bans = append(jj.Bans, bj)
		}

		out.Jails = append(out.Jails, jj)
	}

	return out
}

func fail2banFail(log *slog.Logger, w http.ResponseWriter, err error, what string) {
	var ae *agentrpc.Error

	switch {
	case errors.As(err, &ae) && ae.Code == agentrpc.CodePrecondition:
		writeError(w, http.StatusConflict, "fail2ban_unavailable", ae.Message)
	case errors.As(err, &ae) && ae.Code == agentrpc.CodeInvalidArgument:
		writeError(w, http.StatusUnprocessableEntity, "invalid_argument", ae.Message)
	case errors.As(err, &ae):
		log.Warn(what+": agent refused", "code", ae.Code, "message", ae.Message)
		writeError(w, http.StatusBadGateway, "agent_error", "fail2ban could not be read or changed")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusGatewayTimeout, "agent_timeout", "the agent did not answer in time")
	default:
		log.Error(what+" failed", "err", err)
		writeError(w, http.StatusServiceUnavailable, "agent_unavailable", "the agent could not be reached, try again")
	}
}

// registerFail2ban adds GET /api/v1/firewall/bans and DELETE
// /api/v1/firewall/bans/{address} (ADR-0078). Only administrators call them,
// like the rest of the firewall.
func registerFail2ban(mux *routeMux, a authn, f2b Fail2ban, st Settings, log *slog.Logger) {
	mux.Op("GET /api/v1/firewall/bans", openapi.Operation{
		ID: "getFirewallBans", Tag: "firewall", Summary: "fail2ban's Hostashi jails and the addresses they block", Out: fail2banJSON{},
	}, a.allow("firewall:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := f2b.Status(ctx)
		if err != nil {
			fail2banFail(log, w, err, "fail2ban status")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, toFail2banJSON(s))
	}))

	// One address is unblocked in every jail at once (ADR-0078), so a
	// recidive ban does not stay behind.
	mux.Op("DELETE /api/v1/firewall/bans/{address}", openapi.Operation{
		ID: "deleteFirewallBan", Tag: "firewall", Summary: "Unblock an address in every Hostashi jail", Out: fail2banUnbanJSON{},
	}, a.allow("firewall:write", func(w http.ResponseWriter, r *http.Request, p auth.Principal) {
		addr := r.PathValue("address")
		if err := (agentapi.Fail2banUnbanRequest{Address: addr}).Validate(); err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		resp, err := f2b.Unban(ctx, addr)
		if err != nil {
			fail2banFail(log, w, err, "fail2ban unban")

			return
		}

		if len(resp.Jails) == 0 {
			writeError(w, http.StatusNotFound, "not_banned", "no Hostashi jail blocks this address")

			return
		}

		uid := p.User.ID
		if err := st.Audit(r.Context(), &uid, "firewall.unban", "", map[string]string{
			"address": addr, "jails": strings.Join(resp.Jails, ","), "ip": clientIP(r),
		}); err != nil {
			log.ErrorContext(r.Context(), "audit log", "action", "firewall.unban", "err", err)
		}

		writeJSON(w, http.StatusOK, fail2banUnbanJSON{Address: addr, Jails: resp.Jails})
	}))
}
