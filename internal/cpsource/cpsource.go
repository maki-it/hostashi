// SPDX-License-Identifier: AGPL-3.0-or-later

// Package cpsource keeps, on the agent side, how this server reaches the
// old CloudPanel server (ADR-0026): host and port, an SSH key generated
// here, and the host key the administrator confirmed. Everything lives in a
// root-owned directory; the private key never leaves it. Nothing here talks
// to the network.
package cpsource

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/backup"
	"github.com/maki-it/hostashi/internal/statefile"
)

// Files below Dir.
const (
	SourceFile     = "source.json"
	KeyFile        = "id_ed25519"
	KnownHostsFile = "known_hosts"
	keyComment     = "hostashi-import"
)

// SFTPServer is the read-only SFTP server the key is locked to on the old
// server. Debian and Ubuntu install it here [U: openssh-sftp-server package
// layout]; the administrator can change the path in the line if needed.
const SFTPServer = "/usr/lib/openssh/sftp-server"

// Errors of the package.
var (
	ErrNotSet     = errors.New("cpsource: no source set")
	ErrNotReady   = errors.New("cpsource: no source or no confirmed host key")
	ErrBadHostKey = backup.ErrBadHostKey
)

// Source is the agent's view of the migration source.
type Source struct {
	Dir string // root-owned directory, created with mode 0700

	mu sync.Mutex
}

func owner() uint32 { return uint32(os.Geteuid()) } //nolint:gosec // uids fit in uint32

// AuthorizedKeysLine locks key to a read-only SFTP session and one port
// forward to MariaDB on the old server: no shell, no other command, no agent
// or X11 forwarding, no terminal.
func AuthorizedKeysLine(publicKey string) string {
	return `restrict,port-forwarding,permitopen="127.0.0.1:3306",command="` + SFTPServer + ` -R" ` + publicKey
}

// Get returns the source without secrets.
func (s *Source) Get() (agentapi.CloudPanelSourceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.info()
}

// Set stores host and port. The SSH key is created on first use and never
// replaced, so the line on the old server stays valid. Another host or port
// drops the pinned host key.
func (s *Source) Set(src agentapi.CloudPanelSource) (agentapi.CloudPanelSourceInfo, error) {
	if err := src.Validate(); err != nil {
		return agentapi.CloudPanelSourceInfo{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return agentapi.CloudPanelSourceInfo{}, fmt.Errorf("cpsource: %w", err)
	}

	if err := statefile.CheckDir(s.Dir, owner()); err != nil {
		return agentapi.CloudPanelSourceInfo{}, err
	}

	old, err := s.source()
	if err != nil && !errors.Is(err, ErrNotSet) {
		return agentapi.CloudPanelSourceInfo{}, err
	}

	if err := s.ensureKey(); err != nil {
		return agentapi.CloudPanelSourceInfo{}, err
	}

	if old != nil && *old != src {
		if err := s.remove(KnownHostsFile); err != nil {
			return agentapi.CloudPanelSourceInfo{}, err
		}
	}

	b, err := json.Marshal(src)
	if err != nil {
		return agentapi.CloudPanelSourceInfo{}, fmt.Errorf("cpsource: %w", err)
	}

	if err := statefile.Write(s.Dir, SourceFile, append(b, '\n')); err != nil {
		return agentapi.CloudPanelSourceInfo{}, err
	}

	return s.info()
}

// SetHostKey pins the source's host key: one public key in authorized_keys
// form, of a type backup.ParseHostKey accepts.
func (s *Source) SetHostKey(line string) (agentapi.CloudPanelSourceInfo, error) {
	key, err := backup.ParseHostKey(line)
	if err != nil {
		return agentapi.CloudPanelSourceInfo{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	src, err := s.source()
	if err != nil {
		return agentapi.CloudPanelSourceInfo{}, err
	}

	known := knownhosts.Line([]string{src.Addr()}, key) + "\n"
	if err := statefile.Write(s.Dir, KnownHostsFile, []byte(known)); err != nil {
		return agentapi.CloudPanelSourceInfo{}, err
	}

	return s.info()
}

// Delete removes the source, the SSH key and the pinned host key. The key
// line on the old server then opens nothing any more.
func (s *Source) Delete() (agentapi.CloudPanelSourceInfo, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, f := range []string{KnownHostsFile, KeyFile, SourceFile} {
		if err := s.remove(f); err != nil {
			return agentapi.CloudPanelSourceInfo{}, err
		}
	}

	return agentapi.CloudPanelSourceInfo{}, nil
}

// ClientConfig is the SSH client configuration for the transfer: the
// generated key, user root and only the pinned host key.
func (s *Source) ClientConfig() (agentapi.CloudPanelSource, *ssh.ClientConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	src, err := s.source()
	if err != nil {
		return agentapi.CloudPanelSource{}, nil, err
	}

	signer, err := s.signer()
	if err != nil {
		return agentapi.CloudPanelSource{}, nil, err
	}

	pinned, err := s.pinned(*src)
	if err != nil {
		return agentapi.CloudPanelSource{}, nil, err
	}

	if pinned == nil {
		return agentapi.CloudPanelSource{}, nil, ErrNotReady
	}

	return *src, &ssh.ClientConfig{
		User:              "root",
		Auth:              []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback:   pinnedCallback(pinned),
		HostKeyAlgorithms: []string{pinned.Type()},
		Timeout:           15 * time.Second,
	}, nil
}

// pinnedCallback accepts exactly the pinned key, whatever name or address
// the server is reached under.
func pinnedCallback(pinned ssh.PublicKey) ssh.HostKeyCallback {
	want := pinned.Marshal()

	return func(_ string, _ net.Addr, k ssh.PublicKey) error {
		if string(k.Marshal()) != string(want) {
			return fmt.Errorf("cpsource: host key %s is not the pinned %s", ssh.FingerprintSHA256(k), ssh.FingerprintSHA256(pinned))
		}

		return nil
	}
}

func (s *Source) info() (agentapi.CloudPanelSourceInfo, error) {
	src, err := s.source()
	if errors.Is(err, ErrNotSet) {
		return agentapi.CloudPanelSourceInfo{}, nil
	}

	if err != nil {
		return agentapi.CloudPanelSourceInfo{}, err
	}

	signer, err := s.signer()
	if err != nil {
		return agentapi.CloudPanelSourceInfo{}, err
	}

	pub := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) + " " + keyComment
	info := agentapi.CloudPanelSourceInfo{Source: src, PublicKey: pub, AuthorizedKeysLine: AuthorizedKeysLine(pub)}

	key, err := s.pinned(*src)
	if err != nil {
		return agentapi.CloudPanelSourceInfo{}, err
	}

	if key != nil {
		info.HostKey = ssh.FingerprintSHA256(key)
		info.Ready = true
	}

	return info, nil
}

func (s *Source) source() (*agentapi.CloudPanelSource, error) {
	raw, err := statefile.Read(s.Dir, SourceFile, owner(), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotSet
	}

	if err != nil {
		return nil, err
	}

	var src agentapi.CloudPanelSource
	if err := json.Unmarshal(raw, &src); err != nil {
		return nil, fmt.Errorf("cpsource: %s: %w", SourceFile, err)
	}

	if err := src.Validate(); err != nil {
		return nil, fmt.Errorf("cpsource: %s: %w", SourceFile, err)
	}

	return &src, nil
}

func (s *Source) ensureKey() error {
	if _, err := statefile.Read(s.Dir, KeyFile, owner(), 8192); err == nil || !errors.Is(err, os.ErrNotExist) {
		return err
	}

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("cpsource: ssh key: %w", err)
	}

	block, err := ssh.MarshalPrivateKey(priv, keyComment)
	if err != nil {
		return fmt.Errorf("cpsource: ssh key: %w", err)
	}

	return statefile.Write(s.Dir, KeyFile, pem.EncodeToMemory(block))
}

func (s *Source) signer() (ssh.Signer, error) {
	raw, err := statefile.Read(s.Dir, KeyFile, owner(), 8192)
	if err != nil {
		return nil, fmt.Errorf("cpsource: ssh key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(raw)
	if err != nil {
		return nil, fmt.Errorf("cpsource: ssh key: %w", err)
	}

	return signer, nil
}

// pinned returns the pinned host key for src, nil if none.
func (s *Source) pinned(src agentapi.CloudPanelSource) (ssh.PublicKey, error) {
	raw, err := statefile.Read(s.Dir, KnownHostsFile, owner(), agentapi.MaxHostKey+512)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	_, hosts, key, _, _, err := ssh.ParseKnownHosts(raw)
	if err != nil {
		return nil, fmt.Errorf("cpsource: %s: %w", KnownHostsFile, err)
	}

	want := knownhosts.Normalize(src.Addr())

	for _, h := range hosts {
		if h == want {
			return key, nil
		}
	}

	return nil, nil
}

func (s *Source) remove(name string) error {
	if err := os.Remove(filepath.Join(s.Dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("cpsource: remove %s: %w", name, err)
	}

	return nil
}
