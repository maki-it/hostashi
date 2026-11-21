// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// ErrNoDBTool means the web database tool is not set up: no port, no
// panel address or no panel certificate yet.
var ErrNoDBTool = errors.New("hosting: the database tool is not available")

// DBToolAgent is the part of the agent the web database tool uses
// (ADR-0038).
type DBToolAgent interface {
	DBToolOpen(ctx context.Context, database string, minutes int) (agentapi.DBToolOpenResponse, error)
}

// DBToolOpen calls db.tool.open.
func (a RPCAgent) DBToolOpen(ctx context.Context, database string, minutes int) (agentapi.DBToolOpenResponse, error) {
	return agentapi.DBToolOpen.Call(ctx, a.C, agentapi.DBToolOpenRequest{Database: database, Minutes: minutes})
}

// EnableDBTool turns on the web database tool on port, which nginx serves
// for the panel's host name with the panel's certificate.
func (s *Service) EnableDBTool(a DBToolAgent, port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("database tool port %d", port)
	}

	s.dbTool, s.dbToolPort = a, port

	return nil
}

// DBToolStatus says whether the tool can be opened and for how long.
type DBToolStatus struct {
	Available bool  `json:"available"`
	Minutes   []int `json:"minutes"`
}

// DBTool reports the tool's state. It needs the panel's own certificate,
// which the tool's port shares.
func (s *Service) DBTool(ctx context.Context) (DBToolStatus, error) {
	st := DBToolStatus{Minutes: slices.Clone(agentapi.DBToolDurations)}

	ok, err := s.dbToolReady(ctx)
	st.Available = ok

	return st, err
}

func (s *Service) dbToolReady(ctx context.Context) (bool, error) {
	if s.dbTool == nil || s.panelDomain == "" {
		return false, nil
	}

	p, err := s.PanelTLS(ctx)
	if err != nil {
		return false, err
	}

	return s.panelCovers(p, 0) && p.Live, nil
}

// DBToolSession is an opened session: the address to open in a new tab,
// once, and when the session ends.
type DBToolSession struct {
	URL     string    `json:"url"`
	Expires time.Time `json:"expires"`
}

// OpenDatabaseTool opens database in the tool for minutes. The address
// carries a one-time ticket; the database password stays on the server.
func (s *Service) OpenDatabaseTool(ctx context.Context, actor int64, database string, minutes int) (DBToolSession, error) {
	if err := (agentapi.DBToolOpenRequest{Database: database, Minutes: minutes}).Validate(); err != nil {
		return DBToolSession{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	d, err := s.st.DatabaseByName(ctx, database)
	if errors.Is(err, store.ErrNotFound) {
		return DBToolSession{}, ErrDatabaseNotFound
	}

	if err != nil {
		return DBToolSession{}, err
	}

	site, err := s.Get(ctx, d.Site)
	if err != nil {
		return DBToolSession{}, err
	}

	if site.State != store.SiteActive {
		return DBToolSession{}, ErrState
	}

	ok, err := s.dbToolReady(ctx)
	if err != nil {
		return DBToolSession{}, err
	}

	if !ok {
		return DBToolSession{}, ErrNoDBTool
	}

	res, err := s.dbTool.DBToolOpen(ctx, database, minutes)
	if err != nil {
		return DBToolSession{}, err
	}

	if !agentapi.ValidDBToolTicket(res.Ticket) {
		return DBToolSession{}, errors.New("hosting: agent returned an invalid ticket")
	}

	s.audit(ctx, actor, "database.tool.open", database, map[string]string{
		"site": d.Site, "minutes": strconv.Itoa(minutes), "dbUser": res.User,
		"expires": res.Expires.UTC().Format(time.RFC3339),
	})

	u := url.URL{
		Scheme:   "https",
		Host:     net.JoinHostPort(s.panelDomain, strconv.Itoa(s.dbToolPort)),
		Path:     "/",
		RawQuery: url.Values{"ticket": {res.Ticket}}.Encode(),
	}

	return DBToolSession{URL: u.String(), Expires: res.Expires}, nil
}
