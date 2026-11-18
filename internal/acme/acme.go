// SPDX-License-Identifier: AGPL-3.0-or-later

// Package acme obtains site certificates from an ACME CA (Let's Encrypt by
// default) with lego as a library (ADR-0003). It runs in the unprivileged
// web process and never sees a site's private key: it signs nothing but the
// ACME account requests, and turns a CSR from the agent into a certificate
// chain. HTTP-01 challenges are files in a directory that nginx serves on
// port 80 for every site.
package acme

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
)

// LetsEncrypt is the default directory.
const LetsEncrypt = lego.LEDirectoryProduction

// KeyStore keeps the sealed account key. The production one is the
// settings table, sealed with the panel's secret key.
type KeyStore interface {
	Load(ctx context.Context, name string) ([]byte, bool, error)
	Save(ctx context.Context, name string, pemKey []byte) error
}

// Client obtains certificates.
type Client struct {
	// Directory is the ACME directory URL.
	Directory string
	// HTTPClient talks to the CA; nil uses lego's default.
	HTTPClient *http.Client
	// ChallengeDir receives the HTTP-01 token files (the
	// .well-known/acme-challenge directory nginx serves).
	ChallengeDir string
	Keys         KeyStore

	mu   sync.Mutex
	lego *lego.Client
}

type account struct {
	key crypto.PrivateKey
	reg *registration.Resource
}

func (a *account) GetEmail() string                        { return "" }
func (a *account) GetRegistration() *registration.Resource { return a.reg }
func (a *account) GetPrivateKey() crypto.PrivateKey        { return a.key }

// keyName is the settings key of the account key for one directory.
func (c *Client) keyName() string {
	sum := sha256.Sum256([]byte(c.Directory))

	return "acme.account." + hex.EncodeToString(sum[:8])
}

func (c *Client) accountKey(ctx context.Context) (crypto.PrivateKey, error) {
	b, ok, err := c.Keys.Load(ctx, c.keyName())
	if err != nil {
		return nil, err
	}

	if ok {
		blk, _ := pem.Decode(b)
		if blk == nil {
			return nil, errors.New("acme: stored account key is unreadable")
		}

		return x509.ParsePKCS8PrivateKey(blk.Bytes)
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("acme: %w", err)
	}

	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("acme: %w", err)
	}

	if err := c.Keys.Save(ctx, c.keyName(), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})); err != nil {
		return nil, err
	}

	return key, nil
}

// client returns the lego client with a registered account, creating both
// on first use.
func (c *Client) client(ctx context.Context) (*lego.Client, error) {
	if c.lego != nil {
		return c.lego, nil
	}

	key, err := c.accountKey(ctx)
	if err != nil {
		return nil, err
	}

	acc := &account{key: key}
	cfg := lego.NewConfig(acc)
	cfg.CADirURL = c.Directory
	cfg.UserAgent = "hostashi"

	if c.HTTPClient != nil {
		cfg.HTTPClient = c.HTTPClient
	}

	cl, err := lego.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("acme: %w", err)
	}

	if err := cl.Challenge.SetHTTP01Provider(&files{dir: c.ChallengeDir}); err != nil {
		return nil, fmt.Errorf("acme: %w", err)
	}

	// An account exists for the key after the first run; otherwise create it.
	reg, err := cl.Registration.ResolveAccountByKey()
	if err != nil {
		reg, err = cl.Registration.Register(registration.RegisterOptions{TermsOfServiceAgreed: true})
		if err != nil {
			return nil, fmt.Errorf("acme: register account: %w", err)
		}
	}

	acc.reg = reg
	c.lego = cl

	return cl, nil
}

// Obtain turns a PEM certificate signing request into a certificate chain
// (leaf first). The CA validates every name in the CSR over HTTP-01.
func (c *Client) Obtain(ctx context.Context, csrPEM []byte) ([]byte, error) {
	blk, _ := pem.Decode(csrPEM)
	if blk == nil || blk.Type != "CERTIFICATE REQUEST" {
		return nil, errors.New("acme: not a certificate request")
	}

	csr, err := x509.ParseCertificateRequest(blk.Bytes)
	if err != nil {
		return nil, fmt.Errorf("acme: %w", err)
	}

	// lego has no context; one client at a time keeps the account state simple.
	c.mu.Lock()
	defer c.mu.Unlock()

	cl, err := c.client(ctx)
	if err != nil {
		return nil, err
	}

	res, err := cl.Certificate.ObtainForCSR(certificate.ObtainForCSRRequest{CSR: csr, Bundle: true})
	if err != nil {
		return nil, fmt.Errorf("acme: %w", err)
	}

	return res.Certificate, nil
}

// files is the HTTP-01 provider: one file per token in dir.
type files struct{ dir string }

// tokenRe is the base64url alphabet of ACME tokens (RFC 8555 8.3).
var tokenRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,128}$`)

func (f *files) path(token string) (string, error) {
	if !tokenRe.MatchString(token) {
		return "", fmt.Errorf("acme: unexpected token %q", token)
	}

	return filepath.Join(f.dir, token), nil
}

// Present writes the key authorization. The file must be readable by nginx
// (the directory's group); the panel's umask is 0077, so the mode is set
// explicitly.
func (f *files) Present(_, token, keyAuth string) error {
	p, err := f.path(token)
	if err != nil {
		return err
	}

	if err := os.WriteFile(p, []byte(keyAuth), 0o640); err != nil { //nolint:gosec // nginx (the directory's group) serves the file
		return fmt.Errorf("acme: challenge file: %w", err)
	}

	if err := os.Chmod(p, 0o640); err != nil {
		return fmt.Errorf("acme: challenge file: %w", err)
	}

	return nil
}

// CleanUp removes the file.
func (f *files) CleanUp(_, token, _ string) error {
	p, err := f.path(token)
	if err != nil {
		return err
	}

	if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("acme: challenge file: %w", err)
	}

	return nil
}

// Timeout gives the CA time to validate (lego's default is one minute).
func (f *files) Timeout() (timeout, interval time.Duration) { return 2 * time.Minute, 2 * time.Second }

// SettingsKeys keeps account keys in the panel's settings, sealed with box.
type SettingsKeys struct {
	Get  func(ctx context.Context, key string) (string, bool, error)
	Set  func(ctx context.Context, key, value string) error
	Seal func(plain []byte, context string) ([]byte, error)
	Open func(sealed []byte, context string) ([]byte, error)
}

// Load reads and opens a key.
func (k SettingsKeys) Load(ctx context.Context, name string) ([]byte, bool, error) {
	v, ok, err := k.Get(ctx, name)
	if err != nil || !ok {
		return nil, false, err
	}

	// Settings hold JSON values; the key is a JSON string of base64.
	var b64 string
	if err := json.Unmarshal([]byte(v), &b64); err != nil {
		return nil, false, fmt.Errorf("acme: stored account key: %w", err)
	}

	sealed, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, false, fmt.Errorf("acme: stored account key: %w", err)
	}

	plain, err := k.Open(sealed, name)
	if err != nil {
		return nil, false, fmt.Errorf("acme: stored account key: %w", err)
	}

	return plain, true, nil
}

// Save seals and stores a key.
func (k SettingsKeys) Save(ctx context.Context, name string, pemKey []byte) error {
	sealed, err := k.Seal(pemKey, name)
	if err != nil {
		return err
	}

	v, err := json.Marshal(base64.StdEncoding.EncodeToString(sealed))
	if err != nil {
		return fmt.Errorf("acme: %w", err)
	}

	return k.Set(ctx, name, string(v))
}
