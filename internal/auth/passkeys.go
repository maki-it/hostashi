// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/maki-it/hostashi/internal/store"
)

const (
	// ceremonyTTL is how long a started registration or login stays valid.
	ceremonyTTL = 5 * time.Minute
	// maxCeremonies bounds the memory unauthenticated login starts can use.
	maxCeremonies = 1000
)

var (
	// ErrPasskeyUnavailable is returned when the panel address is not configured.
	ErrPasskeyUnavailable = errors.New("auth: passkeys need the public panel address")
	// ErrPasskeyFailed is returned for every failed passkey ceremony.
	ErrPasskeyFailed = errors.New("auth: passkey verification failed")
	// ErrPasskeyNotFound is returned when a passkey does not exist for this user.
	ErrPasskeyNotFound = errors.New("auth: passkey not found")
	// ErrTooManyPasskeys is returned at the per-user limit.
	ErrTooManyPasskeys = errors.New("auth: too many passkeys")
	// ErrBusy is returned when too many ceremonies are open.
	ErrBusy = errors.New("auth: too many open passkey requests, try again shortly")
)

// PasskeyConfig names the relying party. PublicURL is the address users
// open the panel at, for example https://panel.example.com. The RP ID is its
// host name, so passkeys stay bound to that name.
type PasskeyConfig struct {
	PublicURL   string
	DisplayName string // default "Hostashi"
}

type ceremonyKind int

const (
	kindRegister ceremonyKind = iota + 1
	kindLogin
)

type ceremony struct {
	kind    ceremonyKind
	userID  int64 // registration only
	session webauthn.SessionData
	expires time.Time
}

// ceremonies holds started WebAuthn ceremonies in memory. Each id is used
// once; a restart simply makes users start again.
type ceremonies struct {
	mu  sync.Mutex
	m   map[string]ceremony
	now func() time.Time
}

func (c *ceremonies) put(cer ceremony) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.now()
	for id, v := range c.m {
		if now.After(v.expires) {
			delete(c.m, id)
		}
	}

	if len(c.m) >= maxCeremonies {
		return "", ErrBusy
	}

	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("auth: %w", err)
	}

	id := base64.RawURLEncoding.EncodeToString(raw)
	cer.expires = now.Add(ceremonyTTL)
	c.m[id] = cer

	return id, nil
}

// take returns and removes a ceremony. Expired, unknown and wrong-kind ids fail.
func (c *ceremonies) take(id string, kind ceremonyKind) (ceremony, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	cer, ok := c.m[id]
	if !ok {
		return ceremony{}, false
	}

	delete(c.m, id)

	if cer.kind != kind || c.now().After(cer.expires) {
		return ceremony{}, false
	}

	return cer, true
}

// waUser adapts a panel user to the webauthn.User interface.
type waUser struct {
	u      store.User
	handle []byte
	keys   []store.Passkey
	creds  []webauthn.Credential
}

func (w *waUser) WebAuthnID() []byte                         { return w.handle }
func (w *waUser) WebAuthnName() string                       { return w.u.Username }
func (w *waUser) WebAuthnDisplayName() string                { return w.u.Username }
func (w *waUser) WebAuthnCredentials() []webauthn.Credential { return w.creds }

func newWebAuthn(c *PasskeyConfig) (*webauthn.WebAuthn, error) {
	if c == nil || c.PublicURL == "" {
		return nil, nil //nolint:nilnil // no configuration: passkeys are off, not an error
	}

	u, err := url.Parse(c.PublicURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.Path != "" && u.Path != "/" {
		return nil, fmt.Errorf("auth: public URL %q must be like https://panel.example.com", c.PublicURL)
	}

	if u.Scheme == "http" && u.Hostname() != "localhost" {
		return nil, fmt.Errorf("auth: passkeys need https unless the panel runs on localhost, got %q", c.PublicURL)
	}

	name := c.DisplayName
	if name == "" {
		name = "Hostashi"
	}

	wa, err := webauthn.New(&webauthn.Config{
		RPID:          u.Hostname(),
		RPDisplayName: name,
		RPOrigins:     []string{u.Scheme + "://" + u.Host},
	})
	if err != nil {
		return nil, fmt.Errorf("auth: webauthn: %w", err)
	}

	return wa, nil
}

func (s *Service) loadWAUser(ctx context.Context, u store.User) (*waUser, error) {
	handle, err := s.st.WebAuthnHandle(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	keys, err := s.st.Passkeys(ctx, u.ID)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	w := &waUser{u: u, handle: handle, keys: keys}

	for _, k := range keys {
		var c webauthn.Credential
		if err := json.Unmarshal([]byte(k.Credential), &c); err != nil {
			return nil, fmt.Errorf("auth: stored passkey %d: %w", k.ID, err)
		}

		w.creds = append(w.creds, c)
	}

	return w, nil
}

// PasskeyRegisterBegin starts adding a passkey for a logged-in user. It
// returns a ceremony id and the options for navigator.credentials.create.
func (s *Service) PasskeyRegisterBegin(ctx context.Context, userID int64) (string, *protocol.CredentialCreation, error) {
	if s.wa == nil {
		return "", nil, ErrPasskeyUnavailable
	}

	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return "", nil, fmt.Errorf("auth: %w", err)
	}

	w, err := s.loadWAUser(ctx, u)
	if err != nil {
		return "", nil, err
	}

	if len(w.keys) >= store.MaxPasskeys {
		return "", nil, ErrTooManyPasskeys
	}

	opts, sess, err := s.wa.BeginRegistration(w,
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementRequired,
			UserVerification: protocol.VerificationRequired,
		}),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthn.WithExclusions(webauthn.Credentials(w.creds).CredentialDescriptors()),
	)
	if err != nil {
		return "", nil, fmt.Errorf("auth: %w", err)
	}

	id, err := s.cer.put(ceremony{kind: kindRegister, userID: userID, session: *sess})
	if err != nil {
		return "", nil, err
	}

	return id, opts, nil
}

// PasskeyRegisterFinish checks the authenticator's answer and stores the passkey.
func (s *Service) PasskeyRegisterFinish(ctx context.Context, userID int64, ceremonyID, name string, response []byte, ip string) (store.Passkey, error) {
	if s.wa == nil {
		return store.Passkey{}, ErrPasskeyUnavailable
	}

	if !validTokenName(name) {
		return store.Passkey{}, fmt.Errorf("%w: name must be 1 to 64 characters without control characters", ErrInvalidToken)
	}

	cer, ok := s.cer.take(ceremonyID, kindRegister)
	if !ok || cer.userID != userID {
		return store.Passkey{}, ErrPasskeyFailed
	}

	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return store.Passkey{}, fmt.Errorf("auth: %w", err)
	}

	w, err := s.loadWAUser(ctx, u)
	if err != nil {
		return store.Passkey{}, err
	}

	parsed, err := protocol.ParseCredentialCreationResponseBytes(response)
	if err != nil {
		return store.Passkey{}, ErrPasskeyFailed
	}

	cred, err := s.wa.CreateCredential(w, cer.session, parsed)
	if err != nil {
		return store.Passkey{}, ErrPasskeyFailed
	}

	if !cred.Flags.UserVerified {
		return store.Passkey{}, ErrPasskeyFailed
	}

	raw, err := json.Marshal(cred)
	if err != nil {
		return store.Passkey{}, fmt.Errorf("auth: %w", err)
	}

	p, err := s.st.AddPasskey(ctx, store.Passkey{
		UserID: userID, CredentialID: cred.ID, Name: name, Credential: string(raw), CreatedAt: s.now().UTC(),
	})
	if errors.Is(err, store.ErrTooManyPasskeys) {
		return store.Passkey{}, ErrTooManyPasskeys
	}

	if err != nil {
		// A credential id that is already registered also ends here.
		return store.Passkey{}, fmt.Errorf("auth: %w", err)
	}

	_ = s.st.Audit(ctx, &userID, "passkey.register", name, map[string]string{"id": strconv.FormatInt(p.ID, 10), "ip": ip})
	s.notify(ctx, AccountEvent{UserID: userID, Kind: EventPasskeyAdded, Name: name, IP: ip})

	return p, nil
}

// PasskeyLoginBegin starts a passwordless login. The browser offers the
// user's passkeys for this site; no username is needed.
func (s *Service) PasskeyLoginBegin(ip string) (string, *protocol.CredentialAssertion, error) {
	if s.wa == nil {
		return "", nil, ErrPasskeyUnavailable
	}

	if s.ipLimit.Blocked(ipKey("ip:", ip)) {
		s.obs.Throttled(KindPasskey)

		return "", nil, ErrRateLimited
	}

	opts, sess, err := s.wa.BeginDiscoverableLogin(webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		return "", nil, fmt.Errorf("auth: %w", err)
	}

	id, err := s.cer.put(ceremony{kind: kindLogin, session: *sess})
	if err != nil {
		return "", nil, err
	}

	return id, opts, nil
}

// PasskeyLoginFinish verifies the assertion and starts a session. A passkey
// with user verification is possession plus PIN or biometrics, so it counts
// as both factors and no TOTP code is asked for.
func (s *Service) PasskeyLoginFinish(ctx context.Context, ceremonyID string, response []byte, ip string) (string, store.User, error) {
	if s.wa == nil {
		return "", store.User{}, ErrPasskeyUnavailable
	}

	key := ipKey("ip:", ip)
	if s.ipLimit.Blocked(key) {
		s.obs.Throttled(KindPasskey)

		return "", store.User{}, ErrRateLimited
	}

	fail := func(userID *int64, reason string) (string, store.User, error) {
		s.ipLimit.Fail(key)
		s.failed(ctx, KindPasskey, ip)
		_ = s.st.Audit(ctx, userID, "login.failed", "", map[string]string{"ip": ip, "method": "passkey", "reason": reason})

		return "", store.User{}, ErrPasskeyFailed
	}

	cer, ok := s.cer.take(ceremonyID, kindLogin)
	if !ok {
		return fail(nil, "unknown or expired request")
	}

	parsed, err := protocol.ParseCredentialRequestResponseBytes(response)
	if err != nil {
		return fail(nil, "malformed response")
	}

	var found *waUser

	handler := func(_, userHandle []byte) (webauthn.User, error) {
		u, err := s.st.UserByWebAuthnHandle(ctx, userHandle)
		if err != nil {
			return nil, err
		}

		if u.Disabled {
			return nil, store.ErrNotFound
		}

		found, err = s.loadWAUser(ctx, u)
		if err != nil {
			return nil, err
		}

		return found, nil
	}

	_, cred, err := s.wa.ValidatePasskeyLogin(handler, cer.session, parsed)
	if err != nil || found == nil {
		var uid *int64
		if found != nil {
			uid = &found.u.ID
		}

		return fail(uid, "verification failed")
	}

	if cred.Authenticator.CloneWarning {
		return fail(&found.u.ID, "sign counter went backwards, possible cloned authenticator")
	}

	if err := s.recordPasskeyUse(ctx, found, cred); err != nil {
		return "", store.User{}, err
	}

	token, err := s.newSession(ctx, found.u.ID)
	if err != nil {
		return "", store.User{}, err
	}

	s.ipLimit.Reset(key)
	_ = s.st.Audit(ctx, &found.u.ID, "login", found.u.Username, map[string]string{"ip": ip, "method": "passkey"})

	u := found.u
	u.PasswordHash, u.TOTPSecret = "", nil

	return token, u, nil
}

func (s *Service) recordPasskeyUse(ctx context.Context, w *waUser, cred *webauthn.Credential) error {
	for _, k := range w.keys {
		if string(k.CredentialID) != string(cred.ID) {
			continue
		}

		raw, err := json.Marshal(cred)
		if err != nil {
			return fmt.Errorf("auth: %w", err)
		}

		if err := s.st.UpdatePasskeyUse(ctx, k.ID, string(raw), s.now()); err != nil {
			return fmt.Errorf("auth: %w", err)
		}

		return nil
	}

	return errors.New("auth: verified credential is not stored")
}

// ListPasskeys returns the user's passkeys.
func (s *Service) ListPasskeys(ctx context.Context, userID int64) ([]store.Passkey, error) {
	ps, err := s.st.Passkeys(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}

	return ps, nil
}

// DeletePasskey removes one of the user's passkeys. An account that must
// have a second factor keeps its last one (ErrSecondFactorRequired).
func (s *Service) DeletePasskey(ctx context.Context, userID, id int64, ip string) error {
	u, err := s.st.UserByID(ctx, userID)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	own, err := s.st.Passkeys(ctx, userID)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if !slices.ContainsFunc(own, func(p store.Passkey) bool { return p.ID == id }) {
		return ErrPasskeyNotFound
	}

	if err := s.keepsSecondFactor(ctx, u, false, 1); err != nil {
		return err
	}

	p, err := s.st.DeletePasskey(ctx, userID, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrPasskeyNotFound
	}

	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	_ = s.st.Audit(ctx, &userID, "passkey.delete", p.Name, map[string]string{"id": strconv.FormatInt(p.ID, 10), "ip": ip})
	s.notify(ctx, AccountEvent{UserID: userID, Kind: EventPasskeyRemoved, Name: p.Name, IP: ip})

	return nil
}
