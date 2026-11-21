// SPDX-License-Identifier: AGPL-3.0-or-later

package mail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/maki-it/hostashi/internal/secrets"
)

// SettingKey is the panel setting that holds the relay (ADR-0044).
const SettingKey = "mail.relay"

// sealContext binds the sealed password to its purpose (ADR-0044).
const sealContext = "smtp"

// ErrNotConfigured is returned when no relay is set up. Email is off then,
// and everything works as without it.
var ErrNotConfigured = errors.New("mail: no relay configured")

// KV is the part of the panel store the settings live in.
type KV interface {
	Setting(ctx context.Context, key string) (value string, ok bool, err error)
	SetSetting(ctx context.Context, key, value string) error
}

// Store reads and writes the relay settings, with the password sealed by
// the server key.
type Store struct {
	kv  KV
	box *secrets.Box
}

// NewStore returns the settings store.
func NewStore(kv KV, box *secrets.Box) *Store {
	return &Store{kv: kv, box: box}
}

type stored struct {
	Host     string  `json:"host"`
	Port     int     `json:"port"`
	TLS      TLSMode `json:"tls"`
	Username string  `json:"username,omitempty"`
	Sealed   string  `json:"password_sealed,omitempty"` // base64 of the sealed password
	From     string  `json:"from"`
	FromName string  `json:"from_name,omitempty"`
	ReplyTo  string  `json:"reply_to,omitempty"`
	CAPEM    string  `json:"ca_pem,omitempty"`
}

// Load returns the relay with its password opened, or ErrNotConfigured.
func (s *Store) Load(ctx context.Context) (Config, error) {
	v, ok, err := s.kv.Setting(ctx, SettingKey)
	if err != nil {
		return Config{}, err
	}

	if !ok || v == "null" {
		return Config{}, ErrNotConfigured
	}

	var st stored
	if err := json.Unmarshal([]byte(v), &st); err != nil {
		return Config{}, fmt.Errorf("mail: setting %s: %w", SettingKey, err)
	}

	c := Config{
		Host: st.Host, Port: st.Port, TLS: st.TLS, Username: st.Username,
		From: st.From, FromName: st.FromName, ReplyTo: st.ReplyTo, CAPEM: st.CAPEM,
	}

	if st.Sealed != "" {
		sealed, err := base64.StdEncoding.DecodeString(st.Sealed)
		if err != nil {
			return Config{}, fmt.Errorf("mail: setting %s: password: %w", SettingKey, err)
		}

		pw, err := s.box.Open(sealed, sealContext)
		if err != nil {
			return Config{}, fmt.Errorf("mail: setting %s: password: %w", SettingKey, err)
		}

		c.Password = string(pw)
	}

	return c, nil
}

// Save validates and stores the relay.
func (s *Store) Save(ctx context.Context, c Config) (Config, error) {
	c, err := c.Validate()
	if err != nil {
		return Config{}, err
	}

	st := stored{
		Host: c.Host, Port: c.Port, TLS: c.TLS, Username: c.Username,
		From: c.From, FromName: c.FromName, ReplyTo: c.ReplyTo, CAPEM: c.CAPEM,
	}

	if c.Password != "" {
		sealed, err := s.box.Seal([]byte(c.Password), sealContext)
		if err != nil {
			return Config{}, err
		}

		st.Sealed = base64.StdEncoding.EncodeToString(sealed)
	}

	b, err := json.Marshal(st)
	if err != nil {
		return Config{}, fmt.Errorf("mail: %w", err)
	}

	if err := s.kv.SetSetting(ctx, SettingKey, string(b)); err != nil {
		return Config{}, err
	}

	return c, nil
}

// Clear removes the relay; email is off afterwards.
func (s *Store) Clear(ctx context.Context) error {
	return s.kv.SetSetting(ctx, SettingKey, "null")
}
