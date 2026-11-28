// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/maki-it/hostashi/internal/dnsprov"
	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of the DNS account functions (ADR-0076).
var (
	ErrDNSAccountNotFound = errors.New("hosting: unknown DNS account")
	ErrDNSAccountExists   = errors.New("hosting: a DNS account with this label exists")
	ErrDNSAccountsOff     = errors.New("hosting: DNS accounts are not available")
	// ErrDNSZoneTaken: every zone belongs to one account, so the account
	// for a name is never ambiguous.
	ErrDNSZoneTaken = errors.New("hosting: another DNS account serves this zone")
)

// MaxDNSZones bounds the zones of one account.
const MaxDNSZones = 50

// DNSAPI seals DNS credentials and talks to the providers; DNSProviders
// implements it.
type DNSAPI interface {
	Seal(creds map[string]string, id int64) ([]byte, error)
	Open(a store.DNSAccount) (map[string]string, error)
	// Check shows that creds may write zone: a TXT record is created and
	// deleted again.
	Check(ctx context.Context, provider string, creds map[string]string, zone string) error
}

// EnableDNSAccounts turns on DNS accounts.
func (s *Service) EnableDNSAccounts(d DNSAPI) { s.dns = d }

// NewDNSAccount describes an account to add or, for an update, the new
// state; nil Credentials on an update keep the stored ones.
type NewDNSAccount struct {
	Label       string
	Provider    string
	Zones       []string
	Credentials map[string]string
}

// DNSAccountView is an account with what may be shown of its credentials:
// the values of the fields that are no secret, such as an INWX user name.
type DNSAccountView struct {
	store.DNSAccount
	Settings map[string]string
}

// validDNSLabel accepts 1 to 64 printable characters.
func validDNSLabel(l string) error {
	if l == "" || len(l) > 64 || strings.TrimSpace(l) != l {
		return fmt.Errorf("%w: label must be 1 to 64 characters without leading or trailing spaces", ErrInvalid)
	}

	if strings.ContainsFunc(l, func(r rune) bool { return !unicode.IsPrint(r) }) {
		return fmt.Errorf("%w: label contains a control character", ErrInvalid)
	}

	return nil
}

// zoneLabelRe is one label of a zone: letters, digits, inner hyphens and
// underscores, since a delegated zone such as _acme-challenge.example.com
// has one.
var zoneLabelRe = regexp.MustCompile(`^[a-z0-9_]([a-z0-9_-]{0,61}[a-z0-9_])?$`)

// normalizeZones lower-cases the zones, drops a trailing dot and checks
// them: at least two labels, no wildcard, no duplicate.
func normalizeZones(in []string) ([]string, error) {
	if len(in) == 0 || len(in) > MaxDNSZones {
		return nil, fmt.Errorf("%w: between 1 and %d zones required", ErrInvalid, MaxDNSZones)
	}

	out := make([]string, 0, len(in))

	for _, z := range in {
		z = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(z), "."))

		labels := strings.Split(z, ".")
		if len(z) > 253 || len(labels) < 2 {
			return nil, fmt.Errorf("%w: zone %q needs at least two labels", ErrInvalid, z)
		}

		for _, l := range labels {
			if !zoneLabelRe.MatchString(l) {
				return nil, fmt.Errorf("%w: invalid zone %q", ErrInvalid, z)
			}
		}

		if slices.Contains(out, z) {
			return nil, fmt.Errorf("%w: zone %s is listed twice", ErrInvalid, z)
		}

		out = append(out, z)
	}

	return out, nil
}

// zonesFree refuses a zone that another account than self serves.
func (s *Service) zonesFree(ctx context.Context, self int64, zones []string) error {
	all, err := s.st.DNSAccounts(ctx)
	if err != nil {
		return err
	}

	for _, a := range all {
		if a.ID == self {
			continue
		}

		for _, z := range a.Zones {
			for _, mine := range zones {
				if z == mine {
					return fmt.Errorf("%w: %s is served by %q", ErrDNSZoneTaken, z, a.Label)
				}
			}
		}
	}

	return nil
}

func (s *Service) dnsView(a store.DNSAccount) DNSAccountView {
	v := DNSAccountView{DNSAccount: a, Settings: map[string]string{}}

	p, ok := dnsprov.Lookup(a.Provider)
	if !ok {
		return v
	}

	creds, err := s.dns.Open(a)
	if err != nil {
		s.log.Error("open DNS account credentials", "account", a.ID, "err", err)

		return v
	}

	for _, f := range p.Fields {
		if !f.Secret && creds[f.Name] != "" {
			v.Settings[f.Name] = creds[f.Name]
		}
	}

	return v
}

// DNSAccountList returns all accounts by label.
func (s *Service) DNSAccountList(ctx context.Context) ([]DNSAccountView, error) {
	if s.dns == nil {
		return nil, ErrDNSAccountsOff
	}

	list, err := s.st.DNSAccounts(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]DNSAccountView, 0, len(list))
	for _, a := range list {
		out = append(out, s.dnsView(a))
	}

	return out, nil
}

func (s *Service) dnsAccount(ctx context.Context, id int64) (store.DNSAccount, error) {
	if s.dns == nil {
		return store.DNSAccount{}, ErrDNSAccountsOff
	}

	a, err := s.st.DNSAccount(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.DNSAccount{}, ErrDNSAccountNotFound
	}

	return a, err
}

// DNSAccount returns one account.
func (s *Service) DNSAccount(ctx context.Context, id int64) (DNSAccountView, error) {
	a, err := s.dnsAccount(ctx, id)
	if err != nil {
		return DNSAccountView{}, err
	}

	return s.dnsView(a), nil
}

func dnsAuditView(a store.DNSAccount) map[string]string {
	return map[string]string{"label": a.Label, "provider": a.Provider, "zones": strings.Join(a.Zones, " ")}
}

// AddDNSAccount stores an account with its sealed credentials and checks
// its access at once. A failed check does not refuse the account: the
// result is on it, and the admin fixes the credentials or the zone.
func (s *Service) AddDNSAccount(ctx context.Context, actor int64, req NewDNSAccount) (DNSAccountView, error) {
	if s.dns == nil {
		return DNSAccountView{}, ErrDNSAccountsOff
	}

	if err := validDNSLabel(req.Label); err != nil {
		return DNSAccountView{}, err
	}

	if err := dnsprov.Validate(req.Provider, req.Credentials); err != nil {
		return DNSAccountView{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	zones, err := normalizeZones(req.Zones)
	if err != nil {
		return DNSAccountView{}, err
	}

	if err := s.zonesFree(ctx, 0, zones); err != nil {
		return DNSAccountView{}, err
	}

	a, err := s.st.CreateDNSAccount(ctx, store.NewDNSAccount{
		Label: req.Label, Provider: req.Provider, Zones: zones,
		Seal: func(id int64) ([]byte, error) { return s.dns.Seal(req.Credentials, id) },
	}, s.now())
	if errors.Is(err, store.ErrDNSAccountExists) {
		return DNSAccountView{}, ErrDNSAccountExists
	}

	if err != nil {
		return DNSAccountView{}, err
	}

	s.audit(ctx, actor, "dns_account.create", strconv.FormatInt(a.ID, 10), dnsAuditView(a))

	return s.checkDNSAccount(ctx, a, req.Credentials)
}

// UpdateDNSAccount changes an account. New credentials replace all fields
// and are checked at once.
func (s *Service) UpdateDNSAccount(ctx context.Context, actor, id int64, req NewDNSAccount) (DNSAccountView, error) {
	old, err := s.dnsAccount(ctx, id)
	if err != nil {
		return DNSAccountView{}, err
	}

	if req.Provider != "" && req.Provider != old.Provider {
		return DNSAccountView{}, fmt.Errorf("%w: the provider of an account cannot change; add a new account", ErrInvalid)
	}

	if err := validDNSLabel(req.Label); err != nil {
		return DNSAccountView{}, err
	}

	zones, err := normalizeZones(req.Zones)
	if err != nil {
		return DNSAccountView{}, err
	}

	if err := s.zonesFree(ctx, id, zones); err != nil {
		return DNSAccountView{}, err
	}

	// A zone that a DNS-01 site still needs stays. The lock keeps a
	// site from switching to DNS-01 meanwhile.
	s.mu.Lock()
	defer s.mu.Unlock()

	losers, err := s.dnsLosers(ctx, func(all []store.DNSAccount) []store.DNSAccount {
		out := slices.Clone(all)
		for i := range out {
			if out[i].ID == id {
				out[i].Zones = zones
			}
		}

		return out
	})
	if err != nil {
		return DNSAccountView{}, err
	}

	if len(losers) > 0 {
		return DNSAccountView{}, dnsInUse(losers)
	}

	u := store.DNSAccountUpdate{Label: req.Label, Zones: zones}

	if req.Credentials != nil {
		if err := dnsprov.Validate(old.Provider, req.Credentials); err != nil {
			return DNSAccountView{}, fmt.Errorf("%w: %w", ErrInvalid, err)
		}

		u.Seal = func(id int64) ([]byte, error) { return s.dns.Seal(req.Credentials, id) }
	}

	a, err := s.st.UpdateDNSAccount(ctx, id, u, s.now())
	if errors.Is(err, store.ErrDNSAccountExists) {
		return DNSAccountView{}, ErrDNSAccountExists
	}

	if errors.Is(err, store.ErrNotFound) {
		return DNSAccountView{}, ErrDNSAccountNotFound
	}

	if err != nil {
		return DNSAccountView{}, err
	}

	before, after := dnsAuditView(old), dnsAuditView(a)

	var detail map[string]string
	if req.Credentials != nil {
		// Never the values, only that they changed.
		detail = map[string]string{"credentials": "replaced"}
	}

	s.auditChange(ctx, actor, "dns_account.update", strconv.FormatInt(id, 10), detail, before, after)

	if req.Credentials == nil {
		return s.dnsView(a), nil
	}

	return s.checkDNSAccount(ctx, a, req.Credentials)
}

// DeleteDNSAccount removes an account.
func (s *Service) DeleteDNSAccount(ctx context.Context, actor, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	a, err := s.dnsAccount(ctx, id)
	if err != nil {
		return err
	}

	losers, err := s.dnsLosers(ctx, func(all []store.DNSAccount) []store.DNSAccount {
		return slices.DeleteFunc(slices.Clone(all), func(x store.DNSAccount) bool { return x.ID == id })
	})
	if err != nil {
		return err
	}

	if len(losers) > 0 {
		return dnsInUse(losers)
	}

	if err := s.st.DeleteDNSAccount(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return ErrDNSAccountNotFound
		}

		return err
	}

	s.audit(ctx, actor, "dns_account.delete", strconv.FormatInt(id, 10), dnsAuditView(a))

	return nil
}

// CheckDNSAccount checks the access of an account to each of its zones
// and records the result. A failed check is not an error.
func (s *Service) CheckDNSAccount(ctx context.Context, id int64) (DNSAccountView, error) {
	a, err := s.dnsAccount(ctx, id)
	if err != nil {
		return DNSAccountView{}, err
	}

	creds, err := s.dns.Open(a)
	if err != nil {
		return DNSAccountView{}, err
	}

	return s.checkDNSAccount(ctx, a, creds)
}

func (s *Service) checkDNSAccount(ctx context.Context, a store.DNSAccount, creds map[string]string) (DNSAccountView, error) {
	var msg string

	for _, z := range a.Zones {
		if err := s.dns.Check(ctx, a.Provider, creds, z); err != nil {
			msg = dnsprov.Redact(z+": "+err.Error(), creds)

			break
		}
	}

	at := s.now()
	if err := s.st.SetDNSAccountCheck(context.WithoutCancel(ctx), a.ID, msg, at); err != nil {
		return DNSAccountView{}, err
	}

	if msg != "" {
		s.log.WarnContext(ctx, "DNS account check failed", "account", a.ID, "err", msg)
	}

	a.LastError, a.CheckedAt = msg, at.UTC().Truncate(time.Second)

	return s.dnsView(a), nil
}

// DNSProviders is the real DNSAPI: it seals credentials with the server
// key and builds lego's providers with internal/dnsprov.
type DNSProviders struct {
	Box *secrets.Box
}

// DNSAccountPurpose is the seal's purpose string of an account.
func DNSAccountPurpose(id int64) string { return "dns-account:" + strconv.FormatInt(id, 10) }

// Seal implements DNSAPI.
func (d DNSProviders) Seal(creds map[string]string, id int64) ([]byte, error) {
	b, err := json.Marshal(creds)
	if err != nil {
		return nil, fmt.Errorf("hosting: %w", err)
	}

	return d.Box.Seal(b, DNSAccountPurpose(id))
}

// Open implements DNSAPI.
func (d DNSProviders) Open(a store.DNSAccount) (map[string]string, error) {
	b, err := d.Box.Open(a.SealedCredentials, DNSAccountPurpose(a.ID))
	if err != nil {
		return nil, fmt.Errorf("hosting: DNS account %d: %w", a.ID, err)
	}

	creds := map[string]string{}
	if err := json.Unmarshal(b, &creds); err != nil {
		return nil, fmt.Errorf("hosting: DNS account %d: %w", a.ID, err)
	}

	return creds, nil
}

// Check implements DNSAPI.
func (d DNSProviders) Check(ctx context.Context, provider string, creds map[string]string, zone string) error {
	p, err := dnsprov.New(provider, creds)
	if err != nil {
		return err
	}

	return dnsprov.Check(ctx, p, zone)
}
