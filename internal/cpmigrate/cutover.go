// SPDX-License-Identifier: AGPL-3.0-or-later

package cpmigrate

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/store"
)

// Defaults of the DNS cut-over (ADR-0031).
const (
	DefaultCutoverEvery = time.Minute
	CutoverDeadline     = 48 * time.Hour
	lookupTimeout       = 10 * time.Second
)

// Errors of the cut-over.
var (
	// ErrNoCutover means the cut-over is not enabled, or the site has none.
	ErrNoCutover = errors.New("cpmigrate: no cut-over")
	// ErrCutoverRunning means a cut-over of the site still waits.
	ErrCutoverRunning = errors.New("cpmigrate: a cut-over of this site is still waiting")
	// ErrDomainNotFound means a domain does not exist or has no address.
	ErrDomainNotFound = errors.New("cpmigrate: no such domain or no address")
	// ErrSiteState means the site does not exist or is not active.
	ErrSiteState = errors.New("cpmigrate: the site does not exist or is not active")
)

// CutoverSites reads sites and turns on their HTTPS (hosting.Service).
type CutoverSites interface {
	Get(ctx context.Context, name string) (store.Site, error)
	SetTLS(ctx context.Context, actor int64, name string, on bool) (store.Site, error)
}

// Resolver looks up the A and AAAA addresses of a host name and the
// largest TTL of the records that led to them (dnsq.Client). A name
// without an address is ErrDomainNotFound.
type Resolver interface {
	Lookup(ctx context.Context, host string) ([]netip.Addr, time.Duration, error)
}

// AddrsFunc returns the addresses a domain must point to.
type AddrsFunc func(ctx context.Context) ([]netip.Addr, error)

// DomainAnswer is the last lookup of one domain.
type DomainAnswer struct {
	Domain string   `json:"domain"`
	Addrs  []string `json:"addrs"`
	// Here is true when the domain resolves only to this server.
	Here bool `json:"here"`
	// TTL is the time in seconds a resolver may keep the answer; nil
	// when the lookup failed. A caching resolver reports what is left.
	TTL   *int64 `json:"ttl,omitempty"`
	Error string `json:"error,omitempty"`
}

// Cutover is the DNS cut-over of one site.
type Cutover struct {
	Site      string         `json:"site"`
	State     string         `json:"state"` // waiting, switched, failed, canceled
	StartedAt time.Time      `json:"startedAt"`
	Deadline  time.Time      `json:"deadline"`
	CheckedAt *time.Time     `json:"checkedAt,omitempty"`
	DoneAt    *time.Time     `json:"doneAt,omitempty"`
	Server    []string       `json:"server"` // the addresses the domains must point to now
	Answers   []DomainAnswer `json:"answers"`
	Error     string         `json:"error,omitempty"`
}

type cutover struct {
	sites    CutoverSites
	resolver Resolver
	addrs    AddrsFunc
	kick     chan struct{}
}

// EnableCutover turns on the DNS cut-over. RunCutover watches the waiting
// ones; the state is in the database, so a cut-over survives a restart.
func (s *Service) EnableCutover(sites CutoverSites, r Resolver, addrs AddrsFunc) {
	s.cutover = &cutover{sites: sites, resolver: r, addrs: addrs, kick: make(chan struct{}, 1)}
}

// RunCutover checks the waiting cut-overs every interval
// (DefaultCutoverEvery if zero) and right after one starts, until ctx ends.
func (s *Service) RunCutover(ctx context.Context, every time.Duration) {
	c := s.cutover
	if c == nil {
		return
	}

	if every <= 0 {
		every = DefaultCutoverEvery
	}

	t := time.NewTicker(every)
	defer t.Stop()

	for {
		s.CheckCutovers(ctx)

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-c.kick:
		}
	}
}

// StartCutover starts watching the domains of an active site. When all of
// them resolve only to this server, HTTPS is turned on.
func (s *Service) StartCutover(ctx context.Context, actor int64, site string) (Cutover, error) {
	c := s.cutover
	if c == nil {
		return Cutover{}, ErrNoCutover
	}

	if err := (agentapi.CloudPanelActivateRequest{Site: site}).Validate(); err != nil {
		return Cutover{}, fmt.Errorf("%w: %w", ErrBadRequest, err)
	}

	st, err := c.sites.Get(ctx, site)
	if err != nil || st.State != store.SiteActive {
		return Cutover{}, ErrSiteState
	}

	now := s.now().UTC()

	err = s.st.StartCutover(ctx, store.Cutover{Site: site, StartedBy: &actor, StartedAt: now, Deadline: now.Add(CutoverDeadline)})
	switch {
	case errors.Is(err, store.ErrCutoverRunning):
		return Cutover{}, ErrCutoverRunning
	case errors.Is(err, store.ErrNotFound):
		return Cutover{}, ErrSiteState
	case err != nil:
		return Cutover{}, err
	}

	s.audit(ctx, actor, "cloudpanel.cutover.start", site, nil)

	select {
	case c.kick <- struct{}{}:
	default:
	}

	return s.Cutover(ctx, site)
}

// Cutover returns the cut-over of a site.
func (s *Service) Cutover(ctx context.Context, site string) (Cutover, error) {
	if s.cutover == nil {
		return Cutover{}, ErrNoCutover
	}

	if err := (agentapi.CloudPanelActivateRequest{Site: site}).Validate(); err != nil {
		return Cutover{}, fmt.Errorf("%w: %w", ErrBadRequest, err)
	}

	c, err := s.st.CutoverBySite(ctx, site)
	if errors.Is(err, store.ErrNotFound) {
		return Cutover{}, ErrNoCutover
	}

	if err != nil {
		return Cutover{}, err
	}

	return s.view(ctx, c), nil
}

// CancelCutover stops a waiting cut-over. HTTPS stays as it is.
func (s *Service) CancelCutover(ctx context.Context, actor int64, site string) (Cutover, error) {
	c, err := s.Cutover(ctx, site)
	if err != nil {
		return Cutover{}, err
	}

	if c.State != store.CutoverWaiting {
		return c, nil
	}

	row, err := s.st.CutoverBySite(ctx, site)
	if err != nil {
		return Cutover{}, err
	}

	row.State, row.DoneAt = store.CutoverCanceled, s.now().UTC()
	if _, err := s.st.UpdateCutover(ctx, row); err != nil {
		return Cutover{}, err
	}

	s.audit(ctx, actor, "cloudpanel.cutover.cancel", site, nil)

	return s.Cutover(ctx, site)
}

// CheckCutovers looks at every waiting cut-over once.
func (s *Service) CheckCutovers(ctx context.Context) {
	c := s.cutover
	if c == nil {
		return
	}

	list, err := s.st.WaitingCutovers(ctx)
	if err != nil {
		s.log.ErrorContext(ctx, "cloudpanel: list cut-overs", "err", err)

		return
	}

	if len(list) == 0 {
		return
	}

	server, err := c.addrs(ctx)
	if err != nil {
		s.log.WarnContext(ctx, "cloudpanel: server addresses for the cut-over", "err", err)

		return
	}

	for _, row := range list {
		if ctx.Err() != nil {
			return
		}

		s.checkOne(ctx, row, server)
	}
}

func (s *Service) checkOne(ctx context.Context, row store.Cutover, server []netip.Addr) {
	c := s.cutover
	now := s.now().UTC()
	row.CheckedAt = now

	site, err := c.sites.Get(ctx, row.Site)

	var answers []DomainAnswer

	switch {
	case err != nil || site.State != store.SiteActive:
		row.State, row.DoneAt, row.Error = store.CutoverFailed, now, "the site is not active any more"
	case len(server) == 0:
		row.Error = "this server has no public address"
	default:
		answers = s.lookup(ctx, site.Domains, server)
		row.Error = ""
	}

	row.Answers, _ = json.Marshal(answers) //nolint:errchkjson // plain strings and bools

	here := len(answers) > 0 && !slices.ContainsFunc(answers, func(a DomainAnswer) bool { return !a.Here })

	switch {
	case row.State != store.CutoverWaiting:
	case here:
		row.State, row.DoneAt = store.CutoverSwitched, now
	case !now.Before(row.Deadline):
		row.State, row.DoneAt = store.CutoverFailed, now
		row.Error = "the domains did not point to this server within 48 hours"
	}

	actor := int64(0)
	if row.StartedBy != nil {
		actor = *row.StartedBy
	}

	if row.State == store.CutoverSwitched {
		if _, err := c.sites.SetTLS(ctx, actor, row.Site, true); err != nil {
			row.Error = "the domains point here, but HTTPS could not be turned on: " + err.Error()
		}
	}

	ok, err := s.st.UpdateCutover(ctx, row)
	if err != nil {
		s.log.ErrorContext(ctx, "cloudpanel: store cut-over", "site", row.Site, "err", err)

		return
	}

	if ok && row.State != store.CutoverWaiting {
		s.audit(ctx, actor, "cloudpanel.cutover."+row.State, row.Site, nil)
	}
}

// lookup resolves every domain. A domain is here when it has at least one
// address and all its addresses are the server's.
func (s *Service) lookup(ctx context.Context, domains []string, server []netip.Addr) []DomainAnswer {
	out := make([]DomainAnswer, 0, len(domains))

	for _, d := range domains {
		lctx, cancel := context.WithTimeout(ctx, lookupTimeout)
		addrs, ttl, err := s.cutover.resolver.Lookup(lctx, lookupName(d))

		cancel()

		a := DomainAnswer{Domain: d, Addrs: []string{}}

		if err != nil {
			a.Error = lookupError(err)
			out = append(out, a)

			continue
		}

		secs := int64(ttl / time.Second)
		a.Here, a.TTL = len(addrs) > 0, &secs

		for _, ip := range addrs {
			ip = ip.Unmap()
			a.Addrs = append(a.Addrs, ip.String())

			if !slices.Contains(server, ip) {
				a.Here = false
			}
		}

		slices.Sort(a.Addrs)
		a.Addrs = slices.Compact(a.Addrs)
		out = append(out, a)
	}

	return out
}

// lookupName is the name to resolve for a site domain: for a wildcard
// "*.example.com" a random label below example.com, so the answer shows
// where a wildcard record points (ADR-0075).
func lookupName(domain string) string {
	rest, ok := strings.CutPrefix(domain, agentapi.WildcardPrefix)
	if !ok {
		return domain
	}

	return "hostashi-check-" + strings.ToLower(rand.Text()[:10]) + "." + rest
}

// lookupError gives a short reason without the resolver's address.
func lookupError(err error) string {
	var (
		de *net.DNSError
		ne net.Error
	)

	switch {
	case errors.Is(err, ErrDomainNotFound), errors.As(err, &de) && de.IsNotFound:
		return "no such domain or no address"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &ne) && ne.Timeout():
		return "lookup timed out"
	default:
		return "lookup failed"
	}
}

func (s *Service) view(ctx context.Context, c store.Cutover) Cutover {
	out := Cutover{Site: c.Site, State: c.State, StartedAt: c.StartedAt, Deadline: c.Deadline, Error: c.Error, Answers: []DomainAnswer{}, Server: []string{}}

	if !c.CheckedAt.IsZero() {
		out.CheckedAt = &c.CheckedAt
	}

	if !c.DoneAt.IsZero() {
		out.DoneAt = &c.DoneAt
	}

	if err := json.Unmarshal(c.Answers, &out.Answers); err != nil || out.Answers == nil {
		out.Answers = []DomainAnswer{}
	}

	if s.cutover != nil {
		if addrs, err := s.cutover.addrs(ctx); err == nil {
			for _, a := range addrs {
				out.Server = append(out.Server, a.String())
			}
		}
	}

	return out
}
