// SPDX-License-Identifier: AGPL-3.0-or-later

package agentapi

import (
	"fmt"

	"github.com/maki-it/hostashi/internal/agentrpc"
)

// SnippetCheckRequest asks the agent to check a site's custom snippets
// (ADR-0029) without applying them. The agent knows the site's home and PHP
// socket, so only it can check paths; web.apply checks again.
type SnippetCheckRequest struct {
	User string `json:"user"`
	// PHP tells whether the site runs PHP, so fastcgi_pass to its own
	// socket is allowed.
	PHP      bool        `json:"php,omitempty"`
	Snippets WebSnippets `json:"snippets"`
}

// Validate checks the site name and the sizes.
func (r SnippetCheckRequest) Validate() error {
	if err := ValidateSiteUser(r.User); err != nil {
		return err
	}

	if len(r.Snippets.Server) > MaxSnippetBytes || len(r.Snippets.Root) > MaxSnippetBytes {
		return fmt.Errorf("a snippet is larger than %d bytes", MaxSnippetBytes)
	}

	return nil
}

// SnippetProblem is one rejected line of a snippet.
type SnippetProblem struct {
	Line int    `json:"line"`
	Msg  string `json:"msg"`
}

// SnippetCheckResponse lists the problems per slot; both empty means the
// snippets are accepted. Home, WebRoot and PHPSocket are the paths the
// check used, so the panel can show them and point imported snippets at
// them (empty PHPSocket: no PHP).
type SnippetCheckResponse struct {
	Server    []SnippetProblem `json:"server"`
	Root      []SnippetProblem `json:"root"`
	Home      string           `json:"home"`
	WebRoot   string           `json:"webRoot"`
	PHPSocket string           `json:"phpSocket,omitempty"`
}

// OK reports whether both snippets passed.
func (r SnippetCheckResponse) OK() bool { return len(r.Server) == 0 && len(r.Root) == 0 }

// SnippetCheck checks custom snippets of a site.
var SnippetCheck = agentrpc.Op[SnippetCheckRequest, SnippetCheckResponse]{Name: "site.snippets.check"}
