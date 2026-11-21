// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/sshkeys"
	"github.com/maki-it/hostashi/internal/store"
)

// Errors of the SSH keys.
var (
	ErrNoSSH          = errors.New("hosting: SSH access is not available")
	ErrSSHKeyNotFound = errors.New("hosting: no such SSH key")
	ErrSSHKeyLimit    = fmt.Errorf("hosting: a site has at most %d SSH keys", sshkeys.MaxKeys)
	ErrSSHKeyExists   = errors.New("hosting: the site already has this key")
	errSSHNotActive   = fmt.Errorf("%w: the site is not active", ErrState)
)

// SSHAgent is the part of the agent the SSH keys use (ADR-0032).
type SSHAgent interface {
	SSHApply(ctx context.Context, req agentapi.SSHApplyRequest) (agentapi.SSHApplied, error)
	SSHHostKeys(ctx context.Context) ([]agentapi.SSHHostKey, error)
}

// SSHApply calls ssh.apply.
func (a RPCAgent) SSHApply(ctx context.Context, req agentapi.SSHApplyRequest) (agentapi.SSHApplied, error) {
	return agentapi.SSHApply.Call(ctx, a.C, req)
}

// SSHHostKeys calls ssh.hostkeys.
func (a RPCAgent) SSHHostKeys(ctx context.Context) ([]agentapi.SSHHostKey, error) {
	res, err := agentapi.SSHHostKeys.Call(ctx, a.C, agentapi.SSHHostKeysRequest{})

	return res.Keys, err
}

// EnableSSH sets the agent for SSH keys. Without it they answer ErrNoSSH.
func (s *Service) EnableSSH(a SSHAgent) { s.ssh = a }

// SSHKeyInput is a new key as an admin pastes it.
type SSHKeyInput struct {
	Label  string
	Access sshkeys.Access // empty: SFTP only
	Public string         // one authorized_keys line without options
}

// SSHKeyChange is what can change on a stored key.
type SSHKeyChange struct {
	Label  string
	Access sshkeys.Access
}

func checkLabelAccess(label string, access sshkeys.Access) error {
	if err := sshkeys.CheckLabel(label); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	if err := sshkeys.CheckAccess(access); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	return nil
}

func agentKeys(keys []store.SSHKey) []agentapi.SSHKey {
	out := make([]agentapi.SSHKey, len(keys))
	for i, k := range keys {
		out[i] = agentapi.SSHKey{Label: k.Label, Access: sshkeys.Access(k.Access), Public: k.Public}
	}

	return out
}

// sshSite returns an active site for a key change. A disabled site is
// active too: the agent keeps its keys and gives it an empty key file.
func (s *Service) sshSite(ctx context.Context, name string) (store.Site, error) {
	if s.ssh == nil {
		return store.Site{}, ErrNoSSH
	}

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.Site{}, err
	}

	if site.State != store.SiteActive {
		return store.Site{}, errSSHNotActive
	}

	return site, nil
}

// applySSH sends the complete key list of a site.
func (s *Service) applySSH(ctx context.Context, site store.Site) error {
	keys, err := s.st.SSHKeys(ctx, site.ID)
	if err != nil {
		return err
	}

	_, err = s.ssh.SSHApply(ctx, agentapi.SSHApplyRequest{Site: site.Name, Keys: agentKeys(keys)})

	return err
}

// SSHKeys lists the keys of a site.
func (s *Service) SSHKeys(ctx context.Context, site string) ([]store.SSHKey, error) {
	st, err := s.Get(ctx, site)
	if err != nil {
		return nil, err
	}

	return s.st.SSHKeys(ctx, st.ID)
}

// SSHKey returns one key of a site.
func (s *Service) SSHKey(ctx context.Context, site string, id int64) (store.SSHKey, error) {
	st, err := s.Get(ctx, site)
	if err != nil {
		return store.SSHKey{}, err
	}

	k, err := s.st.SSHKey(ctx, st.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return store.SSHKey{}, ErrSSHKeyNotFound
	}

	return k, err
}

func sshAudit(k store.SSHKey) map[string]string {
	return map[string]string{
		"id": strconv.FormatInt(k.ID, 10), "label": k.Label, "access": k.Access, "fingerprint": k.Fingerprint,
	}
}

// CreateSSHKey adds a key to a site and sends the site's keys to the agent.
func (s *Service) CreateSSHKey(ctx context.Context, actor int64, site string, in SSHKeyInput) (store.SSHKey, error) {
	if in.Access == "" {
		in.Access = sshkeys.SFTP
	}

	if err := checkLabelAccess(in.Label, in.Access); err != nil {
		return store.SSHKey{}, err
	}

	public, fp, err := sshkeys.Parse(in.Public)
	if err != nil {
		return store.SSHKey{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.sshSite(ctx, site)
	if err != nil {
		return store.SSHKey{}, err
	}

	k, err := s.st.InsertSSHKey(ctx, store.SSHKey{
		SiteID: st.ID, Label: in.Label, Access: string(in.Access), Public: public, Fingerprint: fp,
	}, sshkeys.MaxKeys, s.now())

	switch {
	case errors.Is(err, store.ErrTooMany):
		return store.SSHKey{}, ErrSSHKeyLimit
	case errors.Is(err, store.ErrSSHKeyExists):
		return store.SSHKey{}, ErrSSHKeyExists
	case err != nil:
		return store.SSHKey{}, err
	}

	if err := s.applySSH(ctx, st); err != nil {
		if _, rerr := s.st.DeleteSSHKey(context.WithoutCancel(ctx), st.ID, k.ID); rerr != nil {
			s.log.ErrorContext(ctx, "remove SSH key after failed apply", "site", site, "err", rerr)
		}

		return store.SSHKey{}, err
	}

	s.audit(ctx, actor, "ssh.key.create", site, sshAudit(k))

	return k, nil
}

// UpdateSSHKey changes a key's label and access.
func (s *Service) UpdateSSHKey(ctx context.Context, actor int64, site string, id int64, in SSHKeyChange) (store.SSHKey, error) {
	if err := checkLabelAccess(in.Label, in.Access); err != nil {
		return store.SSHKey{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.sshSite(ctx, site)
	if err != nil {
		return store.SSHKey{}, err
	}

	old, err := s.st.UpdateSSHKey(ctx, store.SSHKey{ID: id, SiteID: st.ID, Label: in.Label, Access: string(in.Access)}, s.now())
	if errors.Is(err, store.ErrNotFound) {
		return store.SSHKey{}, ErrSSHKeyNotFound
	}

	if err != nil {
		return store.SSHKey{}, err
	}

	if err := s.applySSH(ctx, st); err != nil {
		if rerr := s.st.RestoreSSHKey(context.WithoutCancel(ctx), old); rerr != nil {
			s.log.ErrorContext(ctx, "restore SSH key after failed apply", "site", site, "err", rerr)
		}

		return store.SSHKey{}, err
	}

	k, err := s.st.SSHKey(ctx, st.ID, id)
	if err != nil {
		return store.SSHKey{}, err
	}

	s.auditChange(ctx, actor, "ssh.key.update", site, sshAudit(k), sshAudit(old), sshAudit(k))

	return k, nil
}

// DeleteSSHKey removes a key. Running sessions of the key are not ended.
func (s *Service) DeleteSSHKey(ctx context.Context, actor int64, site string, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	st, err := s.sshSite(ctx, site)
	if err != nil {
		return err
	}

	old, err := s.st.DeleteSSHKey(ctx, st.ID, id)
	if errors.Is(err, store.ErrNotFound) {
		return ErrSSHKeyNotFound
	}

	if err != nil {
		return err
	}

	if err := s.applySSH(ctx, st); err != nil {
		if rerr := s.st.RestoreSSHKey(context.WithoutCancel(ctx), old); rerr != nil {
			s.log.ErrorContext(ctx, "restore SSH key after failed apply", "site", site, "err", rerr)
		}

		return err
	}

	s.audit(ctx, actor, "ssh.key.delete", site, sshAudit(old))

	return nil
}

// SSHHostKeys returns the fingerprints of the server's SSH host keys, so a
// user can check them at the first login.
func (s *Service) SSHHostKeys(ctx context.Context) ([]agentapi.SSHHostKey, error) {
	if s.ssh == nil {
		return nil, ErrNoSSH
	}

	return s.ssh.SSHHostKeys(ctx)
}

// SyncSSH sends the keys of every active site again, for example after a
// change failed while the agent was down. It goes on after a failure and
// returns all errors.
func (s *Service) SyncSSH(ctx context.Context) error {
	if s.ssh == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	sites, err := s.st.Sites(ctx)
	if err != nil {
		return err
	}

	var errs []error

	for _, site := range sites {
		if site.State == store.SiteActive {
			if err := s.applySSH(ctx, site); err != nil {
				errs = append(errs, fmt.Errorf("site %s: %w", site.Name, err))
			}
		}
	}

	return errors.Join(errs...)
}
