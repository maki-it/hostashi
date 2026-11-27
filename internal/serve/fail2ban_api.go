// SPDX-License-Identifier: AGPL-3.0-or-later

package serve

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/agentrpc"
	"github.com/maki-it/hostashi/internal/auth"
	"github.com/maki-it/hostashi/internal/openapi"
)

// Fail2ban is the agent's view of fail2ban's Hostashi jails (ADR-0078).
type Fail2ban interface {
	Status(ctx context.Context, req agentapi.Fail2banStatusRequest) (agentapi.Fail2banStatus, error)
	Unban(ctx context.Context, address string) (agentapi.Fail2banUnbanResponse, error)
}

// RPCFail2ban talks to the agent.
type RPCFail2ban struct{ C *agentrpc.Client }

// Status implements Fail2ban.
func (f RPCFail2ban) Status(ctx context.Context, req agentapi.Fail2banStatusRequest) (agentapi.Fail2banStatus, error) {
	return agentapi.Fail2banStatusOp.Call(ctx, f.C, req)
}

// Unban implements Fail2ban.
func (f RPCFail2ban) Unban(ctx context.Context, address string) (agentapi.Fail2banUnbanResponse, error) {
	return agentapi.Fail2banUnban.Call(ctx, f.C, agentapi.Fail2banUnbanRequest{Address: address})
}

// fail2banBlockedJSON is one blocked address with every jail that blocks
// it; unblocking lifts all of them.
type fail2banBlockedJSON struct {
	Address string    `json:"address"`
	Jails   []string  `json:"jails"`
	Since   time.Time `json:"since"`
	// Until is null when one of the bans has no end.
	Until *time.Time `json:"until"`
}

type fail2banJailJSON struct {
	Name            string `json:"name"`
	Running         bool   `json:"running"`
	CurrentlyFailed int    `json:"currentlyFailed"`
	TotalFailed     int    `json:"totalFailed"`
	CurrentlyBanned int    `json:"currentlyBanned"`
	TotalBanned     int    `json:"totalBanned"`
}

type fail2banJSON struct {
	Jails []fail2banJailJSON `json:"jails"`
	// Addresses is one page of the matching addresses, newest first.
	Addresses []fail2banBlockedJSON `json:"addresses"`
	// Total counts every matching address.
	Total   int `json:"total"`
	Page    int `json:"page"`
	PerPage int `json:"perPage"`
	// Truncated is true when fail2ban blocks more addresses than the
	// panel reads; some are then missing from every page.
	Truncated bool `json:"truncated"`
}

type fail2banUnbanJSON struct {
	Address string `json:"address"`
	// Jails had banned the address and no longer do.
	Jails []string `json:"jails"`
}

func toFail2banJSON(st agentapi.Fail2banStatus, page, perPage int) fail2banJSON {
	out := fail2banJSON{
		Jails: make([]fail2banJailJSON, 0, len(st.Jails)), Addresses: make([]fail2banBlockedJSON, 0, len(st.Blocked)),
		Total: st.Total, Page: page, PerPage: perPage, Truncated: st.Truncated,
	}

	for _, j := range st.Jails {
		out.Jails = append(out.Jails, fail2banJailJSON{
			Name: j.Name, Running: j.Running, CurrentlyFailed: j.CurrentlyFailed, TotalFailed: j.TotalFailed,
			CurrentlyBanned: j.CurrentlyBanned, TotalBanned: j.TotalBanned,
		})
	}

	for _, b := range st.Blocked {
		bj := fail2banBlockedJSON{Address: b.Address, Jails: b.Jails, Since: time.Unix(b.Since, 0).UTC()}
		if bj.Jails == nil {
			bj.Jails = []string{}
		}

		if b.Until != 0 {
			u := time.Unix(b.Until, 0).UTC()
			bj.Until = &u
		}

		out.Addresses = append(out.Addresses, bj)
	}

	return out
}

var fail2banQuery = []openapi.Param{
	{Name: "jail", Description: "only addresses this jail blocks: sshd, hostashi-panel, hostashi-site-auth or recidive"},
	{Name: "address", Description: "only addresses that contain this text, equal this address, or lie in this range such as 203.0.113.0/24"},
	{Name: "page", Type: "integer", Description: "page number, from 1"},
	{Name: "perPage", Type: "integer", Description: "addresses per page, 1 to 100, default 50"},
}

// fail2banRequest reads the filter and the page of GET /firewall/bans.
func fail2banRequest(r *http.Request) (agentapi.Fail2banStatusRequest, int, int, error) {
	q := r.URL.Query()
	page, perPage := 1, agentapi.Fail2banPageDefault

	for name, dst := range map[string]*int{"page": &page, "perPage": &perPage} {
		v := q.Get(name)
		if v == "" {
			continue
		}

		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			return agentapi.Fail2banStatusRequest{}, 0, 0, fmt.Errorf("%s must be a whole number from 1", name)
		}

		*dst = n
	}

	if perPage > agentapi.Fail2banPageMax {
		return agentapi.Fail2banStatusRequest{}, 0, 0, fmt.Errorf("perPage must be at most %d", agentapi.Fail2banPageMax)
	}

	if page > 1_000_000/perPage {
		return agentapi.Fail2banStatusRequest{}, 0, 0, errors.New("page is out of range")
	}

	req := agentapi.Fail2banStatusRequest{
		Jail: q.Get("jail"), Address: strings.TrimSpace(q.Get("address")), Offset: (page - 1) * perPage, Limit: perPage,
	}

	if err := req.Validate(); err != nil {
		return agentapi.Fail2banStatusRequest{}, 0, 0, err
	}

	return req, page, perPage, nil
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
		ID: "getFirewallBans", Tag: "firewall", Summary: "fail2ban's Hostashi jails and one page of the addresses they block, newest first",
		Out: fail2banJSON{}, Query: fail2banQuery,
	}, a.allow("firewall:read", func(w http.ResponseWriter, r *http.Request, _ auth.Principal) {
		req, page, perPage, err := fail2banRequest(r)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, "invalid_argument", err.Error())

			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), agentTimeout)
		defer cancel()

		s, err := f2b.Status(ctx, req)
		if err != nil {
			fail2banFail(log, w, err, "fail2ban status")

			return
		}

		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, toFail2banJSON(s, page, perPage))
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
