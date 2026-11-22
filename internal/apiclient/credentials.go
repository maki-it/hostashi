// SPDX-License-Identifier: AGPL-3.0-or-later

package apiclient

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// DefaultURL is the panel's own address on the server (ADR-0058).
const DefaultURL = "http://127.0.0.1:8080"

// Environment variables that override the stored login, for scripts.
const (
	EnvURL   = "HOSTASHI_URL"
	EnvToken = "HOSTASHI_TOKEN" //nolint:gosec // the name of a variable, not a token
)

// ErrNotLoggedIn means there is no token: neither stored nor in the
// environment.
var ErrNotLoggedIn = errors.New("not logged in: run hostashi login, or set " + EnvToken)

// Credentials is the stored login of the CLI.
type Credentials struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// CredentialsPath is ~/.config/hostashi/credentials.json, or the same
// below $XDG_CONFIG_HOME.
func CredentialsPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}

	return filepath.Join(dir, "hostashi", "credentials.json"), nil
}

// LoadCredentials reads path; a missing file is an empty login.
func LoadCredentials(path string) (Credentials, error) {
	var c Credentials

	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}

	if err != nil {
		return c, err
	}

	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("%s: %w", path, err)
	}

	return c, nil
}

// Resolve returns the panel URL and token to use: the environment wins
// over the stored login, and the URL falls back to DefaultURL.
func Resolve(stored Credentials, getenv func(string) string) (Credentials, error) {
	c := stored

	if v := getenv(EnvURL); v != "" {
		c.URL = v
		// A token stored for another panel must not be sent here.
		if v != stored.URL {
			c.Token = ""
		}
	}

	if v := getenv(EnvToken); v != "" {
		c.Token = v
	}

	if c.URL == "" {
		c.URL = DefaultURL
	}

	u, err := CheckURL(c.URL)
	if err != nil {
		return c, err
	}

	c.URL = u

	return c, nil
}

// SaveCredentials writes c to path with mode 0600 in a directory with
// mode 0700, replacing the old file in one step.
func SaveCredentials(path string, c Credentials) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}

	f, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return err
	}

	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }()

	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()

		return err
	}

	if _, err := f.Write(append(b, '\n')); err != nil {
		_ = f.Close()

		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

// RemoveCredentials deletes the stored login; a missing file is fine.
func RemoveCredentials(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	return nil
}
