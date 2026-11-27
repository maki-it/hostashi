// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/backup"
	"github.com/maki-it/hostashi/internal/store"
)

// HostKeyScanner connects to an SSH server at addr (ip:port) and returns
// the host key it presents, without logging in.
type HostKeyScanner func(ctx context.Context, addr, user string) (ssh.PublicKey, error)

var (
	// ErrNotSSH: the site does not deploy over SSH.
	ErrNotSSH = errors.New("hosting: the site does not deploy over SSH")
	// ErrHostKeyUnreachable: the repository's server did not answer over
	// SSH. The reason is logged, not shown: it could tell what listens
	// where.
	ErrHostKeyUnreachable = errors.New("hosting: the repository's server did not answer over SSH")
	// ErrBadHostKey: not a host key Hostashi pins.
	ErrBadHostKey = errors.New("hosting: not a usable host key (Ed25519, ECDSA or RSA with at least 3072 bits)")
)

// sshGit returns the Git setting of a site that deploys over SSH and its
// parsed address.
func (s *Service) sshGit(ctx context.Context, name string) (store.Site, store.SiteGit, agentapi.GitRemote, error) {
	if s.gitBox == nil {
		return store.Site{}, store.SiteGit{}, agentapi.GitRemote{}, ErrNoGit
	}

	site, err := s.gitSite(ctx, name)
	if err != nil {
		return store.Site{}, store.SiteGit{}, agentapi.GitRemote{}, err
	}

	g, err := s.st.SiteGit(ctx, site.ID)
	if errors.Is(err, store.ErrNotFound) {
		return store.Site{}, store.SiteGit{}, agentapi.GitRemote{}, ErrGitNotConfigured
	}

	if err != nil {
		return store.Site{}, store.SiteGit{}, agentapi.GitRemote{}, err
	}

	rem, err := agentapi.ParseGitURL(g.URL)
	if err != nil || g.Auth != agentapi.GitAuthSSH || rem.Scheme != "ssh" {
		return store.Site{}, store.SiteGit{}, agentapi.GitRemote{}, ErrNotSSH
	}

	return site, g, rem, nil
}

// ScanGitHostKey reads the host key the repository's server presents. It
// is not trusted by this: the administrator compares the fingerprint with
// the one the Git host publishes and confirms it with SetGitHostKey. A
// name is resolved here and must not lead to an address a reverse proxy
// may not reach (loopback, link-local and the like).
func (s *Service) ScanGitHostKey(ctx context.Context, name string) (ssh.PublicKey, error) {
	_, _, rem, err := s.sshGit(ctx, name)
	if err != nil {
		return nil, err
	}

	port := rem.Port
	if port == 0 {
		port = 22
	}

	addrs, err := s.gitHostAddrs(ctx, rem.Host, port)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	scan := s.hostKeyScan
	if scan == nil {
		scan = backup.FetchHostKeyAt
	}

	var lastErr error

	for _, addr := range addrs {
		key, err := scan(ctx, addr, rem.User)
		if err == nil {
			if _, err := backup.ParseHostKey(string(ssh.MarshalAuthorizedKey(key))); err != nil {
				return nil, ErrBadHostKey
			}

			return key, nil
		}

		lastErr = err
	}

	s.log.WarnContext(ctx, "scan git host key", "site", name, "host", rem.Host, "err", lastErr)

	return nil, ErrHostKeyUnreachable
}

// gitHostAddrs resolves the repository's server like a proxy target.
func (s *Service) gitHostAddrs(ctx context.Context, host string, port int) ([]string, error) {
	var r Resolver = net.DefaultResolver
	if s.resolver != nil {
		r = s.resolver
	}

	lctx, cancel := context.WithTimeout(ctx, proxyLookupTimeout)
	defer cancel()

	u := &url.URL{Scheme: "ssh", Host: net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))}

	return agentapi.ResolveProxyTarget(lctx, u, func(ctx context.Context, host string) ([]netip.Addr, error) {
		return r.LookupNetIP(ctx, "ip", host)
	}, nil)
}

// SetGitHostKey pins the host key the administrator confirmed for the
// site's SSH repository. Deploys check the server against it only; a new
// host or port in the address drops it again.
func (s *Service) SetGitHostKey(ctx context.Context, actor int64, name, line string) (GitView, error) {
	key, err := backup.ParseHostKey(line)
	if err != nil {
		return GitView{}, ErrBadHostKey
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, g, _, err := s.sshGit(ctx, name)
	if err != nil {
		return GitView{}, err
	}

	old := g.HostKey
	g.HostKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key)))
	g.UpdatedBy = actor

	if err := s.st.SetSiteGit(ctx, g, s.now()); err != nil {
		return GitView{}, err
	}

	s.auditChange(ctx, actor, "site.git.hostkey", site.Name, nil,
		map[string]string{"hostKey": hostKeyFingerprint(old)}, map[string]string{"hostKey": hostKeyFingerprint(g.HostKey)})

	saved, err := s.st.SiteGit(ctx, site.ID)
	if err != nil {
		return GitView{}, err
	}

	return toGitView(saved), nil
}
