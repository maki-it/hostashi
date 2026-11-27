// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

// Browser login for the CLI (ADR-0058 addendum 1, RFC 8628): the CLI asks
// for a device code, the user approves its short user code in the panel,
// and the CLI's poll gets an ordinary API token once.

// Device login limits.
const (
	DeviceCodeTTL   = 10 * time.Minute
	DevicePollEvery = 5 * time.Second
	maxDeviceGrants = 100
	deviceCodeBytes = 32
	// userCodeAlphabet has no vowels and no look-alikes (RFC 8628 6.1).
	userCodeAlphabet = "BCDFGHJKLMNPQRSTVWXZ"
	userCodeLen      = 8
	maxClientName    = 64
)

// Device login errors. The poll errors carry the RFC 8628 error codes.
var (
	ErrAuthorizationPending = errors.New("auth: authorization_pending")
	ErrSlowDown             = errors.New("auth: slow_down")
	ErrAccessDenied         = errors.New("auth: access_denied")
	ErrExpiredToken         = errors.New("auth: expired_token")
	// ErrDeviceNotFound is a user code that is unknown, expired or used.
	ErrDeviceNotFound = errors.New("auth: no such device login")
	// ErrInvalidClient is a client name that is empty, too long or has
	// control characters.
	ErrInvalidClient = errors.New("auth: invalid client name")
)

// DeviceStart is the answer to a new device login.
type DeviceStart struct {
	DeviceCode string
	UserCode   string // with a dash, e.g. WDJB-MJHT
	ExpiresIn  time.Duration
	Interval   time.Duration
}

// DeviceRequest is a pending device login as the approval page shows it.
type DeviceRequest struct {
	UserCode string
	Client   string
	IP       string
	Created  time.Time
	Expires  time.Time
}

type deviceGrant struct {
	DeviceRequest

	deviceHash [sha256.Size]byte
	lastPoll   time.Time
	denied     bool
	token      *NewToken // set on approval, handed out by one poll
}

type deviceGrants struct {
	mu sync.Mutex
	m  map[string]*deviceGrant // by normalized user code
}

func (d *deviceGrants) pruneLocked(now time.Time) {
	for k, g := range d.m {
		if !now.Before(g.Expires) {
			delete(d.m, k)
		}
	}
}

// normalizeUserCode upper-cases code and drops dashes and spaces; ""
// when it cannot be a user code.
func normalizeUserCode(code string) string {
	var b strings.Builder

	for _, r := range strings.ToUpper(code) {
		switch {
		case r == '-' || r == ' ':
		case strings.ContainsRune(userCodeAlphabet, r):
			_, _ = b.WriteRune(r)
		default:
			return ""
		}
	}

	if b.Len() != userCodeLen {
		return ""
	}

	return b.String()
}

func formatUserCode(c string) string { return c[:4] + "-" + c[4:] }

func newUserCode() (string, error) {
	// Rejection sampling keeps every letter equally likely.
	limit := 256 - 256%len(userCodeAlphabet)
	out := make([]byte, 0, userCodeLen)
	buf := make([]byte, 16)

	for len(out) < userCodeLen {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}

		for _, c := range buf {
			if int(c) < limit && len(out) < userCodeLen {
				out = append(out, userCodeAlphabet[int(c)%len(userCodeAlphabet)])
			}
		}
	}

	return string(out), nil
}

func validClientName(name string) bool {
	if n := utf8.RuneCountInString(name); n < 1 || n > maxClientName {
		return false
	}

	for _, r := range name {
		if unicode.IsControl(r) {
			return false
		}
	}

	return strings.TrimSpace(name) == name
}

// StartDevice opens a device login for client from ip. Starts count
// against the address like failed logins, so nobody fills the table.
func (s *Service) StartDevice(_ context.Context, client, ip string) (DeviceStart, error) {
	if !validClientName(client) {
		return DeviceStart{}, fmt.Errorf("%w: 1 to %d characters without control characters", ErrInvalidClient, maxClientName)
	}

	key := ipKey("device-ip:", ip)
	if s.deviceLimit.Blocked(key) {
		s.obs.Throttled(KindDevice)

		return DeviceStart{}, ErrRateLimited
	}

	s.deviceLimit.Fail(key)

	raw := make([]byte, deviceCodeBytes)
	if _, err := rand.Read(raw); err != nil {
		return DeviceStart{}, fmt.Errorf("auth: %w", err)
	}

	deviceCode := base64.RawURLEncoding.EncodeToString(raw)

	now := s.now()

	s.devices.mu.Lock()
	defer s.devices.mu.Unlock()

	s.devices.pruneLocked(now)

	if len(s.devices.m) >= maxDeviceGrants {
		return DeviceStart{}, ErrBusy
	}

	var code string

	for {
		c, err := newUserCode()
		if err != nil {
			return DeviceStart{}, fmt.Errorf("auth: %w", err)
		}

		if _, taken := s.devices.m[c]; !taken {
			code = c

			break
		}
	}

	s.devices.m[code] = &deviceGrant{
		DeviceRequest: DeviceRequest{UserCode: formatUserCode(code), Client: client, IP: ip, Created: now.UTC(), Expires: now.Add(DeviceCodeTTL).UTC()},
		deviceHash:    sha256.Sum256([]byte(deviceCode)),
	}

	return DeviceStart{DeviceCode: deviceCode, UserCode: formatUserCode(code), ExpiresIn: DeviceCodeTTL, Interval: DevicePollEvery}, nil
}

// pendingDevice finds the open login of userCode for the user; misses
// count against the account, so codes cannot be guessed.
func (s *Service) pendingDevice(userID int64, userCode string) (*deviceGrant, error) {
	key := fmt.Sprintf("device-user:%d", userID)
	if s.deviceLimit.Blocked(key) {
		s.obs.Throttled(KindDevice)

		return nil, ErrRateLimited
	}

	s.devices.pruneLocked(s.now())

	g, ok := s.devices.m[normalizeUserCode(userCode)]
	if !ok || g.denied || g.token != nil {
		s.deviceLimit.Fail(key)

		return nil, ErrDeviceNotFound
	}

	return g, nil
}

// DeviceRequestOf returns the open login of userCode for the approval page.
func (s *Service) DeviceRequestOf(_ context.Context, userID int64, userCode string) (DeviceRequest, error) {
	s.devices.mu.Lock()
	defer s.devices.mu.Unlock()

	g, err := s.pendingDevice(userID, userCode)
	if err != nil {
		return DeviceRequest{}, err
	}

	return g.DeviceRequest, nil
}

// ApproveDevice approves (with a token for req) or denies the open login of
// userCode. The token is named after the client unless req names it; the
// usual token rules apply. ip is the approver's address.
func (s *Service) ApproveDevice(ctx context.Context, userID int64, userCode string, approve bool, req TokenRequest, ip string) error {
	s.devices.mu.Lock()

	g, err := s.pendingDevice(userID, userCode)
	if err != nil {
		s.devices.mu.Unlock()

		return err
	}

	if !approve {
		g.denied = true
		s.devices.mu.Unlock()

		_ = s.st.Audit(ctx, &userID, "token.device.deny", g.Client, map[string]string{"ip": ip, "clientIp": g.IP})

		return nil
	}

	// Taken out while the token is made, so a second approval of the
	// same code cannot make a second token.
	key := normalizeUserCode(userCode)
	delete(s.devices.m, key)
	s.devices.mu.Unlock()

	if req.Name == "" {
		req.Name = g.Client
	}

	nt, err := s.CreateToken(ctx, userID, req, ip)
	if err != nil {
		s.devices.mu.Lock()
		s.devices.m[key] = g
		s.devices.mu.Unlock()

		return err
	}

	_ = s.st.Audit(ctx, &userID, "token.device", nt.Token.Name, map[string]string{
		"id": strconv.FormatInt(nt.Token.ID, 10), "ip": ip, "clientIp": g.IP,
	})

	s.devices.mu.Lock()
	g.token = &nt
	s.devices.m[key] = g
	s.devices.mu.Unlock()

	return nil
}

// PollDevice is the CLI's poll with its device code. It returns the token
// once, after which the login is gone.
func (s *Service) PollDevice(_ context.Context, deviceCode string) (NewToken, error) {
	h := sha256.Sum256([]byte(deviceCode))
	now := s.now()

	s.devices.mu.Lock()
	defer s.devices.mu.Unlock()

	for k, g := range s.devices.m {
		if subtle.ConstantTimeCompare(g.deviceHash[:], h[:]) != 1 {
			continue
		}

		switch {
		case !now.Before(g.Expires):
			delete(s.devices.m, k)

			return NewToken{}, ErrExpiredToken
		case g.denied:
			delete(s.devices.m, k)

			return NewToken{}, ErrAccessDenied
		case g.token != nil:
			delete(s.devices.m, k)

			return *g.token, nil
		case !g.lastPoll.IsZero() && now.Sub(g.lastPoll) < DevicePollEvery:
			g.lastPoll = now

			return NewToken{}, ErrSlowDown
		default:
			g.lastPoll = now

			return NewToken{}, ErrAuthorizationPending
		}
	}

	return NewToken{}, ErrExpiredToken
}
