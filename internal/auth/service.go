// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/maki-it/hostashi/internal/secrets"
	"github.com/maki-it/hostashi/internal/store"
)

// SessionTTL is the absolute lifetime of a session.
const SessionTTL = 12 * time.Hour

var (
	// ErrInvalidCredentials is returned for every failed login, whatever the reason.
	ErrInvalidCredentials = errors.New("auth: invalid username or password")
	// ErrRateLimited is returned when too many attempts were made.
	ErrRateLimited = errors.New("auth: too many attempts, try again later")
	// ErrUnauthenticated is returned for a missing, unknown or expired session.
	ErrUnauthenticated = errors.New("auth: not authenticated")
	// ErrTOTPRequired is returned when the password was right but the account
	// needs a second factor and none was given.
	ErrTOTPRequired = errors.New("auth: second factor required")
	// ErrTOTPUnavailable is returned when no encryption key is configured.
	ErrTOTPUnavailable = errors.New("auth: TOTP is not available")
	// ErrTOTPState is returned when TOTP is already enabled, or was not started.
	ErrTOTPState = errors.New("auth: TOTP is not in the right state for this action")
	// ErrSetupNotAllowed is returned for a wrong setup token or a finished setup.
	ErrSetupNotAllowed = errors.New("auth: setup not allowed")
)

// Config configures a Service.
type Config struct {
	Store          *store.Store
	SetupTokenPath string
	Box            *secrets.Box     // encrypts TOTP seeds; nil disables TOTP
	Issuer         string           // shown in authenticator apps, default "Hostashi"
	Now            func() time.Time // default time.Now
	Observer       Observer         // failed and throttled logins; nil: none
	FailLog        *slog.Logger     // gets the FailedEvent line for fail2ban; nil: none
}

// Service is the login, session and setup logic.
type Service struct {
	st        *store.Store
	tokenPath string
	box       *secrets.Box
	issuer    string
	now       func() time.Time
	obs       Observer
	flog      *slog.Logger

	ipLimit    *Limiter
	userLimit  *Limiter
	tokenLimit *Limiter
	emailLimit *Limiter
	notifyFn   func(context.Context, AccountEvent)

	wa  *webauthn.WebAuthn // nil: passkeys disabled
	cer *ceremonies

	dummyOnce sync.Once
	dummyHash string
}

// NewService creates a Service. Passkeys are off until EnablePasskeys.
func NewService(c Config) *Service {
	now := c.Now
	if now == nil {
		now = time.Now
	}

	issuer := c.Issuer
	if issuer == "" {
		issuer = "Hostashi"
	}

	obs := c.Observer
	if obs == nil {
		obs = nopObserver{}
	}

	flog := c.FailLog
	if flog == nil {
		flog = slog.New(slog.DiscardHandler)
	}

	return &Service{
		obs:       obs,
		flog:      flog,
		cer:       &ceremonies{m: map[string]ceremony{}, now: now},
		st:        c.Store,
		tokenPath: c.SetupTokenPath,
		box:       c.Box,
		issuer:    issuer,
		now:       now,
		ipLimit:   NewLimiter(10, 15*time.Minute, now),
		userLimit: NewLimiter(20, 15*time.Minute, now),
		// Unknown bearer tokens per address; a client with a valid token never counts.
		tokenLimit: NewLimiter(30, 15*time.Minute, now),
		// Confirmation emails per account, so nobody floods a stranger's
		// mailbox through the relay.
		emailLimit: NewLimiter(emailRequestsPerHour, time.Hour, now),
	}
}

// EnablePasskeys turns on passkey registration and login for the panel at
// c.PublicURL. Call it once, before serving requests.
func (s *Service) EnablePasskeys(c PasskeyConfig) error {
	wa, err := newWebAuthn(&c)
	if err != nil {
		return err
	}

	s.wa = wa

	return nil
}

// SetupRequired reports whether the first admin still has to be created.
func (s *Service) SetupRequired(ctx context.Context) (bool, error) {
	n, err := s.st.CountUsers(ctx)
	if err != nil {
		return false, fmt.Errorf("auth: %w", err)
	}

	return n == 0, nil
}

// Setup creates the first admin if the setup token matches. The token file
// is removed on success. Wrong tokens count against the IP limit.
func (s *Service) Setup(ctx context.Context, token, username, password, ip string) (store.User, error) {
	if s.ipLimit.Blocked("ip:" + ip) {
		return store.User{}, ErrRateLimited
	}

	if err := ValidateUsername(username); err != nil {
		return store.User{}, err
	}

	if err := ValidatePassword(password); err != nil {
		return store.User{}, err
	}

	want, err := ReadSetupToken(s.tokenPath, s.now())
	if err != nil || !tokenMatches(want, token) {
		s.ipLimit.Fail("ip:" + ip)

		return store.User{}, ErrSetupNotAllowed
	}

	hash, err := HashPassword(password)
	if err != nil {
		return store.User{}, err
	}

	u, err := s.st.CreateFirstUser(ctx, username, hash)
	if errors.Is(err, store.ErrSetupDone) {
		return store.User{}, ErrSetupNotAllowed
	}

	if err != nil {
		return store.User{}, fmt.Errorf("auth: %w", err)
	}

	_ = os.Remove(s.tokenPath)
	_ = s.st.Audit(ctx, &u.ID, "setup.complete", username, map[string]string{"ip": ip})

	return u, nil
}

func (s *Service) dummy() string {
	s.dummyOnce.Do(func() {
		s.dummyHash, _ = HashPassword("dummy password for timing equalization")
	})

	return s.dummyHash
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))

	return h[:]
}

// Login checks the credentials and returns a new session token and the user.
// Unknown user, wrong password and disabled account all return
// ErrInvalidCredentials, and all of them do one password hash.
func (s *Service) Login(ctx context.Context, username, password, code, ip string) (string, store.User, error) {
	ipKey, userKey := "ip:"+ip, "user:"+username
	if s.ipLimit.Blocked(ipKey) || s.userLimit.Blocked(userKey) {
		s.obs.Throttled(KindPassword)

		return "", store.User{}, ErrRateLimited
	}

	u, err := s.st.UserByUsername(ctx, username)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", store.User{}, fmt.Errorf("auth: %w", err)
	}

	hash := u.PasswordHash
	if errors.Is(err, store.ErrNotFound) || hash == "" {
		hash = s.dummy()
		u = store.User{}
	}

	ok, verr := VerifyPassword(hash, password)
	if verr != nil || !ok || u.ID == 0 || u.Disabled {
		s.ipLimit.Fail(ipKey)
		s.userLimit.Fail(userKey)
		_ = s.st.Audit(ctx, nil, "login.failed", username, map[string]string{"ip": ip})
		s.failed(ctx, KindPassword, ip)

		return "", store.User{}, ErrInvalidCredentials
	}

	if u.TOTPEnabled {
		if code == "" {
			return "", store.User{}, ErrTOTPRequired
		}

		if !s.secondFactor(ctx, u, code) {
			s.ipLimit.Fail(ipKey)
			s.userLimit.Fail(userKey)
			_ = s.st.Audit(ctx, &u.ID, "login.failed", username, map[string]string{"ip": ip, "reason": "second factor"})
			s.failed(ctx, KindTOTP, ip)

			return "", store.User{}, ErrInvalidCredentials
		}
	}

	// A passkey is no second step after a password: an account that must
	// have a second factor and has only passkeys logs in with one.
	if !u.TOTPEnabled && SecondFactorRequired(u) {
		has, err := s.HasSecondFactor(ctx, u)
		if err != nil {
			return "", store.User{}, err
		}

		if has {
			return "", store.User{}, ErrPasskeyRequired
		}
	}

	token, err := s.newSession(ctx, u.ID)
	if err != nil {
		return "", store.User{}, err
	}

	s.ipLimit.Reset(ipKey)
	s.userLimit.Reset(userKey)
	_ = s.st.Audit(ctx, &u.ID, "login", username, map[string]string{"ip": ip})

	u.PasswordHash, u.TOTPSecret = "", nil

	return token, u, nil
}

// newSession stores a new session for the user and returns its token.
func (s *Service) newSession(ctx context.Context, userID int64) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()

	if err := s.st.CreateSession(ctx, userID, hashToken(token), now, now.Add(SessionTTL)); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}

	return token, nil
}

// Authenticate returns the user for a session token.
func (s *Service) Authenticate(ctx context.Context, token string) (store.User, error) {
	if token == "" || len(token) > 128 {
		return store.User{}, ErrUnauthenticated
	}

	u, err := s.st.SessionUser(ctx, hashToken(token), s.now())
	if errors.Is(err, store.ErrNotFound) {
		return store.User{}, ErrUnauthenticated
	}

	if err != nil {
		return store.User{}, fmt.Errorf("auth: %w", err)
	}

	return u, nil
}

// Logout ends the session. An unknown token is not an error.
func (s *Service) Logout(ctx context.Context, token string) error {
	if token == "" || len(token) > 128 {
		return nil
	}

	if err := s.st.DeleteSession(ctx, hashToken(token)); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	return nil
}

// Cleanup removes expired sessions and invitations.
func (s *Service) Cleanup(ctx context.Context) error {
	if _, err := s.st.DeleteExpiredSessions(ctx, s.now()); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if err := s.st.DeleteExpiredInvitations(ctx, s.now()); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if err := s.st.DeleteExpiredEmailConfirmations(ctx, s.now()); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	return nil
}

func totpContext(userID int64) string { return fmt.Sprintf("totp:user:%d", userID) }

// secondFactor accepts a current TOTP code (each step once) or an unused
// recovery code, which is consumed.
func (s *Service) secondFactor(ctx context.Context, u store.User, code string) bool {
	code = strings.TrimSpace(code)

	if s.box != nil && len(u.TOTPSecret) > 0 {
		if secret, err := s.box.Open(u.TOTPSecret, totpContext(u.ID)); err == nil {
			if step, ok := VerifyTOTP(secret, code, s.now()); ok {
				accepted, err := s.st.AcceptTOTPStep(ctx, u.ID, step)

				return err == nil && accepted
			}
		}
	}

	if h, ok := recoveryHash(code); ok {
		used, err := s.st.UseRecoveryCode(ctx, u.ID, h)

		return err == nil && used
	}

	return false
}

// recoveryHash normalizes a recovery code (16 hex digits, dashes and case
// ignored) and returns its SHA-256.
func recoveryHash(code string) ([]byte, bool) {
	c := strings.ToLower(strings.ReplaceAll(code, "-", ""))
	if len(c) != 16 {
		return nil, false
	}

	if _, err := hex.DecodeString(c); err != nil {
		return nil, false
	}

	h := sha256.Sum256([]byte(c))

	return h[:], true
}

func newRecoveryCodes(n int) (codes []string, hashes [][]byte, err error) {
	for range n {
		raw := make([]byte, 8)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, fmt.Errorf("auth: %w", err)
		}

		c := hex.EncodeToString(raw)
		h := sha256.Sum256([]byte(c))
		codes = append(codes, c[0:4]+"-"+c[4:8]+"-"+c[8:12]+"-"+c[12:16])
		hashes = append(hashes, h[:])
	}

	return codes, hashes, nil
}

// TOTPEnroll starts enrollment: it stores a new encrypted seed (not active
// yet) and returns the seed and the otpauth URI for the authenticator app.
func (s *Service) TOTPEnroll(ctx context.Context, userID int64) (secret, uri string, err error) {
	if s.box == nil {
		return "", "", ErrTOTPUnavailable
	}

	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return "", "", fmt.Errorf("auth: %w", err)
	}

	seed, err := NewTOTPSecret()
	if err != nil {
		return "", "", err
	}

	sealed, err := s.box.Seal(seed, totpContext(u.ID))
	if err != nil {
		return "", "", fmt.Errorf("auth: %w", err)
	}

	if err := s.st.SetPendingTOTP(ctx, u.ID, sealed); err != nil {
		if errors.Is(err, store.ErrTOTPState) {
			return "", "", ErrTOTPState
		}

		return "", "", fmt.Errorf("auth: %w", err)
	}

	return TOTPSecretString(seed), TOTPURI(s.issuer, u.Username, seed), nil
}

// TOTPConfirm finishes enrollment with the first valid code. It turns TOTP
// on and returns eight one-time recovery codes, shown only this once.
func (s *Service) TOTPConfirm(ctx context.Context, userID int64, code string) ([]string, error) {
	if s.box == nil {
		return nil, ErrTOTPUnavailable
	}

	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	if u.TOTPEnabled || len(u.TOTPSecret) == 0 {
		return nil, ErrTOTPState
	}

	key := "user:" + u.Username
	if s.userLimit.Blocked(key) {
		return nil, ErrRateLimited
	}

	secret, err := s.box.Open(u.TOTPSecret, totpContext(u.ID))
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	step, ok := VerifyTOTP(secret, code, s.now())
	if !ok {
		s.userLimit.Fail(key)

		return nil, ErrInvalidCredentials
	}

	codes, hashes, err := newRecoveryCodes(8)
	if err != nil {
		return nil, err
	}

	if err := s.st.EnableTOTP(ctx, u.ID, step, hashes); err != nil {
		if errors.Is(err, store.ErrTOTPState) {
			return nil, ErrTOTPState
		}

		return nil, fmt.Errorf("auth: %w", err)
	}

	_ = s.st.Audit(ctx, &u.ID, "totp.enable", u.Username, nil)
	s.notify(ctx, AccountEvent{UserID: u.ID, Kind: EventTOTPEnabled})

	return codes, nil
}

// TOTPDisable turns TOTP off. It needs the password and a current code or
// a recovery code, so a stolen session alone cannot remove the second factor.
// An account that must have a second factor keeps TOTP unless it has a
// passkey (ErrSecondFactorRequired).
func (s *Service) TOTPDisable(ctx context.Context, userID int64, password, code string) error {
	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if !u.TOTPEnabled {
		return ErrTOTPState
	}

	key := "user:" + u.Username
	if s.userLimit.Blocked(key) {
		return ErrRateLimited
	}

	ok, verr := VerifyPassword(u.PasswordHash, password)
	if verr != nil || !ok || !s.secondFactor(ctx, u, code) {
		s.userLimit.Fail(key)

		return ErrInvalidCredentials
	}

	if err := s.keepsSecondFactor(ctx, u, true, 0); err != nil {
		return err
	}

	if err := s.st.DisableTOTP(ctx, u.ID); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	_ = s.st.Audit(ctx, &u.ID, "totp.disable", u.Username, nil)
	s.notify(ctx, AccountEvent{UserID: u.ID, Kind: EventTOTPDisabled})

	return nil
}
