// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/maki-it/hostashi/internal/authz"
	"github.com/maki-it/hostashi/internal/store"
)

// TokenPrefix starts every API token, so that secret scanners and humans can
// recognize one.
const TokenPrefix = "hst_"

const (
	tokenSecretBytes = 32
	tokenLenMax      = 128
	prefixShown      = 6

	// DefaultTokenDays is the lifetime of a token when none is requested, and
	// the longest lifetime that can be asked for. A token without expiry needs
	// Unlimited (ADR-0018).
	DefaultTokenDays = 365

	// maxAllowlistEntries bounds the IP allowlist for token access.
	maxAllowlistEntries = 64

	allowlistKey = "api.ip_allowlist"
)

var (
	// ErrInvalidToken is returned for a token request that is not acceptable.
	ErrInvalidToken = errors.New("auth: invalid API token request")
	// ErrTokenNotAllowedFromIP is returned when the allowlist excludes the caller.
	ErrTokenNotAllowedFromIP = errors.New("auth: API tokens are not accepted from this address")
	// ErrTooManyTokens is returned when the user holds too many active tokens.
	ErrTooManyTokens = errors.New("auth: too many API tokens")
	// ErrTokenNotFound is returned when a token does not exist for this user.
	ErrTokenNotFound = errors.New("auth: API token not found")
	// ErrInvalidAllowlist is returned for an allowlist that cannot be parsed.
	ErrInvalidAllowlist = errors.New("auth: invalid IP allowlist")
)

// Principal is who a request acts as.
type Principal struct {
	User    store.User
	TokenID int64    // 0 for a browser session
	Scopes  []string // only for tokens
	// Sites limits a token to these sites; nil means every site the owner
	// reaches (ADR-0018, ADR-0035).
	Sites []string
}

// SiteLimited reports whether the principal is a token with a site list.
func (p Principal) SiteLimited() bool { return p.Sites != nil }

// TokenReaches reports whether the token's site list allows site. A
// session and a token without a list allow every site; the owner's role
// and grants are checked on top.
func (p Principal) TokenReaches(site string) bool {
	return p.Sites == nil || slices.Contains(p.Sites, site)
}

// IsToken reports whether the request came with an API token.
func (p Principal) IsToken() bool { return p.TokenID != 0 }

// Allows reports whether the principal may use scope. A session is limited
// by the owner's role, which the handler checks; a token is limited by its
// scopes on top of that.
func (p Principal) Allows(scope string) bool {
	if !p.IsToken() {
		return true
	}

	return slices.Contains(p.Scopes, scope)
}

// TokenRequest describes a new token.
type TokenRequest struct {
	Name   string
	Scopes []string
	// Days is the lifetime in days, 1..DefaultTokenDays. 0 means DefaultTokenDays.
	Days int
	// Unlimited creates a token that never expires. It cannot be combined with Days.
	Unlimited bool
	// Sites limits the token to these sites; nil means every site the
	// owner reaches. Each must be one the owner reaches now.
	Sites []string
}

// NewToken is returned once, when a token is created.
type NewToken struct {
	Secret string // shown once, never stored
	Token  store.APIToken
}

func validTokenName(name string) bool {
	if n := utf8.RuneCountInString(name); n < 1 || n > 64 {
		return false
	}

	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}

	return strings.TrimSpace(name) == name
}

// CreateToken makes a token for the user. The secret is returned once.
func (s *Service) CreateToken(ctx context.Context, userID int64, req TokenRequest, ip string) (NewToken, error) {
	if !validTokenName(req.Name) {
		return NewToken{}, fmt.Errorf("%w: name must be 1 to 64 characters without control characters", ErrInvalidToken)
	}

	scopes, err := normalizeScopes(req.Scopes)
	if err != nil {
		return NewToken{}, err
	}

	owner, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return NewToken{}, fmt.Errorf("auth: %w", err)
	}

	// A token never grants more than its owner (ADR-0035): only the scopes
	// of the owner's role, only sites the owner reaches.
	useful := authz.RoleScopes(authz.Role(owner.Role))
	for _, sc := range scopes {
		if !slices.Contains(useful, sc) {
			return NewToken{}, fmt.Errorf("%w: the scope %q is not available to the role %s", ErrInvalidToken, sc, owner.Role)
		}
	}

	if err := s.checkTokenSites(ctx, owner, req.Sites); err != nil {
		return NewToken{}, err
	}

	if req.Unlimited && req.Days != 0 {
		return NewToken{}, fmt.Errorf("%w: unlimited cannot be combined with a lifetime", ErrInvalidToken)
	}

	days := req.Days
	if days == 0 {
		days = DefaultTokenDays
	}

	if days < 1 || days > DefaultTokenDays {
		return NewToken{}, fmt.Errorf("%w: lifetime must be 1 to %d days", ErrInvalidToken, DefaultTokenDays)
	}

	raw := make([]byte, tokenSecretBytes)
	if _, err := rand.Read(raw); err != nil {
		return NewToken{}, fmt.Errorf("auth: %w", err)
	}

	body := base64.RawURLEncoding.EncodeToString(raw)
	secret := TokenPrefix + body
	now := s.now()

	t := store.APIToken{
		UserID:    userID,
		Name:      req.Name,
		Prefix:    body[:prefixShown],
		Scopes:    scopes,
		CreatedAt: now.UTC(),
		Sites:     req.Sites,
	}

	if !req.Unlimited {
		exp := now.Add(time.Duration(days) * 24 * time.Hour).UTC()
		t.ExpiresAt = &exp
	}

	t, err = s.st.CreateAPIToken(ctx, t, hashToken(secret))
	if errors.Is(err, store.ErrTooManyTokens) {
		return NewToken{}, ErrTooManyTokens
	}

	if errors.Is(err, store.ErrNotFound) {
		return NewToken{}, fmt.Errorf("%w: no such site", ErrInvalidToken)
	}

	if err != nil {
		return NewToken{}, fmt.Errorf("auth: %w", err)
	}

	expires := "never"
	if t.ExpiresAt != nil {
		expires = t.ExpiresAt.Format(time.RFC3339)
	}

	_ = s.st.Audit(ctx, &userID, "token.create", t.Name, map[string]string{
		"id": strconv.FormatInt(t.ID, 10), "scopes": strings.Join(t.Scopes, " "), "expires": expires, "ip": ip,
		"sites": tokenSitesAudit(t.Sites),
	})
	s.notify(ctx, AccountEvent{UserID: userID, Kind: EventTokenCreated, Name: t.Name, IP: ip})

	return NewToken{Secret: secret, Token: t}, nil
}

// checkTokenSites refuses a site list with a site the owner does not reach,
// or that is too long. A user's site must be granted; for admins and
// managers the store refuses a site that does not exist.
func (s *Service) checkTokenSites(ctx context.Context, owner store.User, sites []string) error {
	if sites == nil {
		return nil
	}

	if len(sites) == 0 {
		return fmt.Errorf("%w: a site list needs at least one site; leave it out for every site", ErrInvalidToken)
	}

	if len(sites) > store.MaxTokenSites {
		return fmt.Errorf("%w: at most %d sites", ErrInvalidToken, store.MaxTokenSites)
	}

	if authz.SeesAllSites(authz.Role(owner.Role)) {
		return nil
	}

	for _, site := range sites {
		ok, err := s.st.HasSiteGrant(ctx, owner.ID, site)
		if err != nil {
			return fmt.Errorf("auth: %w", err)
		}

		if !ok {
			return fmt.Errorf("%w: no such site", ErrInvalidToken)
		}
	}

	return nil
}

func tokenSitesAudit(sites []string) string {
	if sites == nil {
		return "all"
	}

	return strings.Join(sites, " ")
}

// ListTokens returns the user's tokens, revoked ones included.
func (s *Service) ListTokens(ctx context.Context, userID int64) ([]store.APIToken, error) {
	ts, err := s.st.ListAPITokens(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	return ts, nil
}

// RevokeToken revokes one of the user's tokens at once.
func (s *Service) RevokeToken(ctx context.Context, userID, id int64, ip string) error {
	t, err := s.st.RevokeAPIToken(ctx, userID, id, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return ErrTokenNotFound
	}

	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	_ = s.st.Audit(ctx, &userID, "token.revoke", t.Name, map[string]string{"id": strconv.FormatInt(t.ID, 10), "ip": ip})

	return nil
}

// AuthenticateToken checks a bearer token from ip. A wrong token, an
// expired or revoked one and a disabled owner all give ErrUnauthenticated.
// Failures count against the address, so guessing is rate limited, and are
// logged without the token.
func (s *Service) AuthenticateToken(ctx context.Context, raw, ip string) (Principal, error) {
	key := ipKey("token-ip:", ip)
	if s.tokenLimit.Blocked(key) {
		s.obs.Throttled(KindToken)

		return Principal{}, ErrRateLimited
	}

	fail := func() (Principal, error) {
		s.tokenLimit.Fail(key)
		s.failed(ctx, KindToken, ip)

		return Principal{}, ErrUnauthenticated
	}

	if !strings.HasPrefix(raw, TokenPrefix) || len(raw) > tokenLenMax {
		return fail()
	}

	t, u, err := s.st.APITokenByHash(ctx, hashToken(raw), s.now())
	if errors.Is(err, store.ErrNotFound) {
		_ = s.st.Audit(ctx, nil, "token.rejected", "", map[string]string{"ip": ip})

		return fail()
	}

	if err != nil {
		return Principal{}, fmt.Errorf("auth: %w", err)
	}

	allowed, err := s.tokenIPAllowed(ctx, ip)
	if err != nil {
		return Principal{}, err
	}

	if !allowed {
		_ = s.st.Audit(ctx, &u.ID, "token.denied", t.Name, map[string]string{"id": strconv.FormatInt(t.ID, 10), "ip": ip})

		return Principal{}, ErrTokenNotAllowedFromIP
	}

	if err := s.st.TouchAPIToken(ctx, t.ID, s.now(), ip); err != nil {
		return Principal{}, fmt.Errorf("auth: %w", err)
	}

	return Principal{User: u, TokenID: t.ID, Scopes: t.Scopes, Sites: t.Sites}, nil
}

// SessionPrincipal returns the principal of a browser session.
func (s *Service) SessionPrincipal(ctx context.Context, sessionToken string) (Principal, error) {
	u, err := s.Authenticate(ctx, sessionToken)
	if err != nil {
		return Principal{}, err
	}

	return Principal{User: u}, nil
}

// IPAllowlist returns the networks API tokens are accepted from. An empty
// list means every address.
func (s *Service) IPAllowlist(ctx context.Context) ([]string, error) {
	v, ok, err := s.st.Setting(ctx, allowlistKey)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	if !ok {
		return []string{}, nil
	}

	var list []string
	if err := json.Unmarshal([]byte(v), &list); err != nil {
		return nil, fmt.Errorf("auth: stored allowlist: %w", err)
	}

	return list, nil
}

func parseNet(entry string) (netip.Prefix, error) {
	if p, err := netip.ParsePrefix(entry); err == nil {
		return p.Masked(), nil
	}

	a, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%w: %q is not an address or network", ErrInvalidAllowlist, truncate(entry, 64))
	}

	return netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen()), nil
}

// SetIPAllowlist replaces the list. Entries are addresses or CIDR networks.
// An empty list removes the restriction. Browser sessions are never affected.
func (s *Service) SetIPAllowlist(ctx context.Context, userID int64, entries []string, ip string) ([]string, error) {
	if len(entries) > maxAllowlistEntries {
		return nil, fmt.Errorf("%w: at most %d entries", ErrInvalidAllowlist, maxAllowlistEntries)
	}

	norm := make([]string, 0, len(entries))

	for _, e := range entries {
		p, err := parseNet(strings.TrimSpace(e))
		if err != nil {
			return nil, err
		}

		if !slices.Contains(norm, p.String()) {
			norm = append(norm, p.String())
		}
	}

	b, err := json.Marshal(norm)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	was, err := s.IPAllowlist(ctx)
	if err != nil {
		return nil, err
	}

	if err := s.st.SetSetting(ctx, allowlistKey, string(b)); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	_ = s.st.AuditChange(ctx, &userID, "settings.api_ip_allowlist", "", map[string]string{"entries": strings.Join(norm, " "), "ip": ip},
		store.DiffOf(map[string]string{"entries": strings.Join(was, " ")}, map[string]string{"entries": strings.Join(norm, " ")}))

	return norm, nil
}

var loopback4 = netip.AddrFrom4([4]byte{127, 0, 0, 1})

// tokenIPAllowed reports whether a token may be used from ip. An empty
// allowlist allows every address; 127.0.0.1 and ::1 are always allowed.
func (s *Service) tokenIPAllowed(ctx context.Context, ip string) (bool, error) {
	list, err := s.IPAllowlist(ctx)
	if err != nil {
		return false, err
	}

	if len(list) == 0 {
		return true, nil
	}

	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false, nil //nolint:nilerr // an address that cannot be parsed is never on the list
	}

	addr = addr.Unmap()

	// The server itself is always allowed: the hostashi command on it talks
	// to http://127.0.0.1:8080 (Kim, 2026-11-24). Only nginx's X-Real-IP or
	// a TCP peer sets the address, and a trusted proxy range may never hold
	// loopback (agentapi.checkRealIPRange), so a remote client cannot claim
	// it. Local users of the server still need a valid token.
	if addr == loopback4 || addr == netip.IPv6Loopback() {
		return true, nil
	}

	for _, e := range list {
		p, err := netip.ParsePrefix(e)
		if err == nil && p.Contains(addr) {
			return true, nil
		}
	}

	return false, nil
}
