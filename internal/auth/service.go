// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

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
	// ErrSetupNotAllowed is returned for a wrong setup token or a finished setup.
	ErrSetupNotAllowed = errors.New("auth: setup not allowed")
)

// Config configures a Service.
type Config struct {
	Store          *store.Store
	SetupTokenPath string
	Now            func() time.Time // default time.Now
}

// Service is the login, session and setup logic.
type Service struct {
	st        *store.Store
	tokenPath string
	now       func() time.Time

	ipLimit   *Limiter
	userLimit *Limiter

	dummyOnce sync.Once
	dummyHash string
}

// NewService creates a Service.
func NewService(c Config) *Service {
	now := c.Now
	if now == nil {
		now = time.Now
	}

	return &Service{
		st:        c.Store,
		tokenPath: c.SetupTokenPath,
		now:       now,
		ipLimit:   NewLimiter(10, 15*time.Minute, now),
		userLimit: NewLimiter(20, 15*time.Minute, now),
	}
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
func (s *Service) Login(ctx context.Context, username, password, ip string) (string, store.User, error) {
	ipKey, userKey := "ip:"+ip, "user:"+username
	if s.ipLimit.Blocked(ipKey) || s.userLimit.Blocked(userKey) {
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

		return "", store.User{}, ErrInvalidCredentials
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", store.User{}, fmt.Errorf("auth: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()

	if err := s.st.CreateSession(ctx, u.ID, hashToken(token), now, now.Add(SessionTTL)); err != nil {
		return "", store.User{}, fmt.Errorf("auth: %w", err)
	}

	s.ipLimit.Reset(ipKey)
	s.userLimit.Reset(userKey)
	_ = s.st.Audit(ctx, &u.ID, "login", username, map[string]string{"ip": ip})

	u.PasswordHash = ""

	return token, u, nil
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

// Cleanup removes expired sessions.
func (s *Service) Cleanup(ctx context.Context) error {
	if _, err := s.st.DeleteExpiredSessions(ctx, s.now()); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	return nil
}
