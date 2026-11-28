// SPDX-License-Identifier: AGPL-3.0-or-later

// Package dnsprov is the short allowlist of DNS providers for DNS-01
// certificates (ADR-0076): their credential fields and how to build lego's
// provider for them. Every provider is a lego package imported on its own;
// lego's generic exec, httpreq and acmedns providers are not offered.
package dnsprov

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-acme/lego/v5/challenge"
	"github.com/go-acme/lego/v5/providers/dns/cloudflare"
	"github.com/go-acme/lego/v5/providers/dns/hetzner"
	"github.com/go-acme/lego/v5/providers/dns/inwx"
)

// Provider ids.
const (
	INWX       = "inwx"
	Hetzner    = "hetzner"
	Cloudflare = "cloudflare"
)

// Field is one credential a provider needs.
type Field struct {
	Name  string `json:"name"`
	Label string `json:"label"`
	// Secret fields are never shown again after they are stored.
	Secret   bool `json:"secret"`
	Optional bool `json:"optional"`
}

// Provider is one DNS provider of the allowlist.
type Provider struct {
	ID     string  `json:"id"`
	Name   string  `json:"name"`
	Fields []Field `json:"fields"`
	// Hint says in one line which narrow credential to create.
	Hint string `json:"hint"`
}

var providers = []Provider{
	{
		ID: Cloudflare, Name: "Cloudflare",
		Fields: []Field{{Name: "apiToken", Label: "API token", Secret: true}},
		Hint:   "An API token with Zone: Read and DNS: Edit on the needed zones only, never the global API key.",
	},
	{
		ID: Hetzner, Name: "Hetzner",
		Fields: []Field{{Name: "apiToken", Label: "API token (Hetzner Cloud)", Secret: true}},
		Hint:   "A read/write token of a separate Hetzner Cloud project that holds only DNS zones: a token of a project with servers can delete them.",
	},
	{
		ID: INWX, Name: "INWX",
		Fields: []Field{
			{Name: "username", Label: "Username"},
			{Name: "password", Label: "Password", Secret: true},
			{Name: "sharedSecret", Label: "Two-factor shared secret", Secret: true, Optional: true},
		},
		Hint: "A sub-user with DNS rights only, if your account offers one.",
	},
}

// maxValue bounds one credential value.
const maxValue = 1024

// ErrUnknown is returned for a provider id that is not on the allowlist.
var ErrUnknown = errors.New("dnsprov: unknown DNS provider")

// All returns the providers by name.
func All() []Provider {
	out := slices.Clone(providers)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// Lookup returns the provider with id.
func Lookup(id string) (Provider, bool) {
	for _, p := range providers {
		if p.ID == id {
			return p, true
		}
	}

	return Provider{}, false
}

// Validate checks creds against the provider's fields: every required
// field set, no unknown field, no control characters. It never names a
// value.
func Validate(id string, creds map[string]string) error {
	p, ok := Lookup(id)
	if !ok {
		return fmt.Errorf("%w %q", ErrUnknown, id)
	}

	known := make(map[string]bool, len(p.Fields))

	for _, f := range p.Fields {
		known[f.Name] = true

		v := creds[f.Name]
		if v == "" && !f.Optional {
			return fmt.Errorf("dnsprov: %s needs %s", p.Name, f.Label)
		}

		if len(v) > maxValue {
			return fmt.Errorf("dnsprov: %s is too long", f.Label)
		}

		if strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return fmt.Errorf("dnsprov: %s contains a control character", f.Label)
		}
	}

	for k := range creds {
		if !known[k] {
			return fmt.Errorf("dnsprov: %s has no field %q", p.Name, k)
		}
	}

	return nil
}

// Settings that are the same for every provider. Let's Encrypt waits for
// the TXT record to show at the zone's name servers; a provider whose
// name servers take longer fails the order and the next run retries.
const (
	ttl                = 120
	propagationTimeout = 5 * time.Minute
	pollingInterval    = 5 * time.Second
	httpTimeout        = 30 * time.Second
)

// New builds lego's DNS-01 provider for id. Each call returns a fresh
// provider: some keep the records they created for CleanUp, so one
// order or check uses one provider from Present to CleanUp.
func New(id string, creds map[string]string) (challenge.ProviderTimeout, error) {
	if err := Validate(id, creds); err != nil {
		return nil, err
	}

	hc := &http.Client{Timeout: httpTimeout}

	switch id {
	case Cloudflare:
		cfg := cloudflare.NewDefaultConfig()
		cfg.AuthEmail, cfg.AuthKey, cfg.ZoneToken = "", "", ""
		cfg.AuthToken = creds["apiToken"]
		cfg.TTL, cfg.PropagationTimeout, cfg.PollingInterval, cfg.HTTPClient = ttl, propagationTimeout, pollingInterval, hc

		return cloudflare.NewDNSProviderConfig(cfg)
	case Hetzner:
		cfg := hetzner.NewDefaultConfig()
		// APIToken wins over a legacy key from the environment.
		cfg.APIToken = creds["apiToken"]
		cfg.TTL, cfg.PropagationTimeout, cfg.PollingInterval, cfg.HTTPClient = ttl, propagationTimeout, pollingInterval, hc

		return hetzner.NewDNSProviderConfig(cfg)
	case INWX:
		cfg := inwx.NewDefaultConfig()
		cfg.Sandbox = false
		cfg.Username, cfg.Password, cfg.SharedSecret = creds["username"], creds["password"], creds["sharedSecret"]
		// INWX's lowest TTL is 300.
		cfg.TTL, cfg.PropagationTimeout, cfg.PollingInterval = 300, propagationTimeout, pollingInterval

		return inwx.NewDNSProviderConfig(cfg)
	default:
		return nil, fmt.Errorf("%w %q", ErrUnknown, id)
	}
}

// Redact replaces every credential value in msg, so a provider's error
// can be shown and logged.
func Redact(msg string, creds map[string]string) string {
	vals := make([]string, 0, len(creds))

	for _, v := range creds {
		// Short values such as a one-letter user name would blank out
		// ordinary words; they are no secret either.
		if len(v) >= 4 {
			vals = append(vals, v)
		}
	}

	// Longest first, so a value that contains another is replaced whole.
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })

	for _, v := range vals {
		msg = strings.ReplaceAll(msg, v, "[redacted]")
	}

	return msg
}

// checkToken and checkKeyAuth make the TXT record of an access check: a
// value no CA ever asks for.
const (
	checkToken   = "hostashi-access-check"
	checkKeyAuth = "hostashi-access-check.not-an-acme-challenge"
)

// Check creates and deletes a TXT record at _acme-challenge.<zone>, the
// one call every provider has, to show that the credentials may write
// the zone. lego follows a CNAME there as it does for a real order.
func Check(ctx context.Context, p challenge.Provider, zone string) error {
	if err := p.Present(ctx, zone, checkToken, checkKeyAuth); err != nil {
		return err
	}

	return p.CleanUp(ctx, zone, checkToken, checkKeyAuth)
}

// MatchZone returns the longest zone in zones that fqdn falls into, on a
// label boundary; ok is false when none does. Names are compared without
// a trailing dot and in lower case.
func MatchZone(fqdn string, zones []string) (string, bool) {
	name := strings.ToLower(strings.TrimSuffix(fqdn, "."))

	best := ""

	for _, z := range zones {
		z = strings.ToLower(strings.TrimSuffix(z, "."))
		if z == "" {
			continue
		}

		if (name == z || strings.HasSuffix(name, "."+z)) && len(z) > len(best) {
			best = z
		}
	}

	return best, best != ""
}
