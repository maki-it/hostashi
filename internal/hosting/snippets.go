// SPDX-License-Identifier: AGPL-3.0-or-later

package hosting

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/cloudpanel/snippetdraft"
	"github.com/maki-it/hostashi/internal/store"
)

// SnippetCheck calls site.snippets.check.
func (a RPCAgent) SnippetCheck(ctx context.Context, req agentapi.SnippetCheckRequest) (agentapi.SnippetCheckResponse, error) {
	return agentapi.SnippetCheck.Call(ctx, a.C, req)
}

// SnippetError means the agent rejected a snippet; Problems names every
// rejected line per slot (ADR-0029).
type SnippetError struct {
	Problems agentapi.SnippetCheckResponse
}

func (e *SnippetError) Error() string {
	return fmt.Sprintf("hosting: snippets rejected (%d problems in the server snippet, %d in the location / snippet)", len(e.Problems.Server), len(e.Problems.Root))
}

// Unwrap makes a SnippetError an ErrInvalid.
func (e *SnippetError) Unwrap() error { return ErrInvalid }

// Snippets returns the custom snippets of a site.
func (s *Service) Snippets(ctx context.Context, name string) (store.SiteSnippets, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return store.SiteSnippets{}, err
	}

	return s.st.SiteSnippets(ctx, site.ID)
}

// SetSnippets checks the snippets of an active site with the agent, stores
// them and applies the web state. On any failure the old snippets stay. The
// audit log records sizes only: snippets may hold credentials, for example
// in a proxy header.
func (s *Service) SetSnippets(ctx context.Context, actor int64, name, server, root string) (store.SiteSnippets, error) {
	if len(server) > agentapi.MaxSnippetBytes || len(root) > agentapi.MaxSnippetBytes {
		return store.SiteSnippets{}, fmt.Errorf("%w: a snippet is larger than %d bytes", ErrInvalid, agentapi.MaxSnippetBytes)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	site, err := s.Get(ctx, name)
	if err != nil {
		return store.SiteSnippets{}, err
	}

	if site.State != store.SiteActive {
		return store.SiteSnippets{}, ErrState
	}

	res, err := s.ag.SnippetCheck(ctx, agentapi.SnippetCheckRequest{
		User: site.Name, PHP: site.PHP != "", Snippets: agentapi.WebSnippets{Server: server, Root: root},
	})
	if err != nil {
		return store.SiteSnippets{}, err
	}

	if !res.OK() {
		return store.SiteSnippets{}, &SnippetError{Problems: res}
	}

	// A header that is switched on must not come twice (ADR-0056).
	hs, err := s.st.SiteHeaders(ctx, site.ID)
	if err != nil {
		return store.SiteSnippets{}, err
	}

	if err := hs.Headers.Conflict(server, root); err != nil {
		return store.SiteSnippets{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	now := s.now()

	old, err := s.st.SetSiteSnippets(ctx, store.SiteSnippets{SiteID: site.ID, Server: server, Root: root, UpdatedBy: actor}, now)
	if err != nil {
		return store.SiteSnippets{}, err
	}

	if err := s.applyWeb(ctx); err != nil {
		if _, rerr := s.st.SetSiteSnippets(context.WithoutCancel(ctx), old, old.UpdatedAt); rerr != nil {
			s.log.ErrorContext(ctx, "restore snippets after failed change", "site", name, "err", rerr)
		}

		return store.SiteSnippets{}, err
	}

	// The snippets can be long; the diff names their hashes.
	s.auditChange(ctx, actor, "site.snippets.set", name, map[string]string{
		"serverBytes": strconv.Itoa(len(server)), "rootBytes": strconv.Itoa(len(root)),
	}, snippetFields(old.Server, old.Root), snippetFields(server, root))

	return s.st.SiteSnippets(ctx, site.ID)
}

// SnippetTarget returns the paths of an active site on this server that
// snippets may use: home, document root and own PHP socket (ADR-0029).
func (s *Service) SnippetTarget(ctx context.Context, name string) (snippetdraft.Target, error) {
	site, err := s.Get(ctx, name)
	if err != nil {
		return snippetdraft.Target{}, err
	}

	if site.State != store.SiteActive {
		return snippetdraft.Target{}, ErrState
	}

	res, err := s.ag.SnippetCheck(ctx, agentapi.SnippetCheckRequest{User: site.Name, PHP: site.PHP != ""})
	if err != nil {
		return snippetdraft.Target{}, err
	}

	return snippetdraft.Target{Home: res.Home, WebRoot: res.WebRoot, PHPSocket: res.PHPSocket}, nil
}

// webSnippets returns the snippets of a site for web.apply, nil for none.
func webSnippets(all map[int64]store.SiteSnippets, site store.Site) *agentapi.WebSnippets {
	sn, ok := all[site.ID]
	if !ok || sn.Empty() {
		return nil
	}

	return &agentapi.WebSnippets{Server: sn.Server, Root: sn.Root}
}

// snippetFields describes the snippets for a diff by their SHA-256.
func snippetFields(server, root string) map[string]string {
	h := func(s string) string {
		if s == "" {
			return ""
		}

		sum := sha256.Sum256([]byte(s))

		return "sha256:" + hex.EncodeToString(sum[:])
	}

	return map[string]string{"server": h(server), "root": h(root)}
}
