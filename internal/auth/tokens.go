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
	}

	if !req.Unlimited {
		exp := now.Add(time.Duration(days) * 24 * time.Hour).UTC()
		t.ExpiresAt = &exp
	}

	t, err = s.st.CreateAPIToken(ctx, t, hashToken(secret))
	if errors.Is(err, store.ErrTooManyTokens) {
		return NewToken{}, ErrTooManyTokens
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
	})

	return NewToken{Secret: secret, Token: t}, nil
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
	key := "token-ip:" + ip
	if s.tokenLimit.Blocked(key) {
		s.obs.Throttled(KindToken)

		return Principal{}, ErrRateLimited
	}

	fail := func() (Principal, error) {
		s.tokenLimit.Fail(key)
		s.obs.Failure(KindToken)

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

	return Principal{User: u, TokenID: t.ID, Scopes: t.Scopes}, nil
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

	if err := s.st.SetSetting(ctx, allowlistKey, string(b)); err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	_ = s.st.Audit(ctx, &userID, "settings.api_ip_allowlist", "", map[string]string{"entries": strings.Join(norm, " "), "ip": ip})

	return norm, nil
}

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

	for _, e := range list {
		p, err := netip.ParsePrefix(e)
		if err == nil && p.Contains(addr) {
			return true, nil
		}
	}

	return false, nil
}
