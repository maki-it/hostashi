// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"fmt"
	"sort"
)

// ScopeInfo describes one API scope (ADR-0018). A scope is "resource:action".
type ScopeInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// HighRisk scopes must be named explicitly. There are no wildcards, so
	// nothing can grant them by accident.
	HighRisk bool `json:"highRisk"`
}

// scopeCatalog is the closed list of scopes a token may carry. A feature that
// adds an endpoint adds its scope here in the same change.
var scopeCatalog = map[string]ScopeInfo{
	"sites:read":              {Description: "List and read sites"},
	"sites:write":             {Description: "Create and change sites"},
	"sites:delete":            {Description: "Delete sites and their files", HighRisk: true},
	"sites.maintenance:write": {Description: "Switch the maintenance mode of sites and park domains"},
	"sites.snippets:write":    {Description: "Change the custom nginx snippets of sites (checked against an allowlist)", HighRisk: true},
	"cron:read":               {Description: "List the cron jobs of sites with their last and next runs"},
	"cron:write":              {Description: "Create, change, pause, run and delete cron jobs (they run commands as the site user)", HighRisk: true},
	"ssh:read":                {Description: "List the SSH keys of sites with their fingerprints and access"},
	"ssh:write":               {Description: "Add, change and delete SSH keys of sites (a key with shell access is a login as the site user)", HighRisk: true},
	"limits:read":             {Description: "Read the limit presets and the resource limits and disk use of sites"},
	"limits:write":            {Description: "Change the limit presets and the resource limits of sites"},
	"pages:read":              {Description: "List maintenance and parking pages and their files"},
	"pages:write":             {Description: "Upload, replace and delete maintenance and parking pages"},
	"databases:read":          {Description: "List databases and their users"},
	"databases:write":         {Description: "Create databases and users, set passwords, remove users"},
	"databases:delete":        {Description: "Delete databases and their data", HighRisk: true},
	"backups:read":            {Description: "Read backup settings, status and snapshots"},
	"backups:write":           {Description: "Change backup settings, confirm the target's host key, start a backup"},
	"doctor:read":             {Description: "Run the read-only checks of hostashi doctor and read their results"},
	"firewall:read":           {Description: "Read the firewall's opened ports and the allowlists for SSH and the panel"},
	"firewall:write":          {Description: "Open ports and set the allowlists for SSH and the panel (a wrong list locks out until the rollback)", HighRisk: true},
	"metrics:read":            {Description: "Read the Prometheus metrics endpoint"},
	"migrations:read":         {Description: "List uploaded CloudPanel scan reports and read their migration plans"},
	"migrations:write":        {Description: "Upload and delete CloudPanel scan reports"},
	"backups:restore":         {Description: "Restore a site's backups into its home", HighRisk: true},
}

// Scopes returns the catalog sorted by name.
func Scopes() []ScopeInfo {
	out := make([]ScopeInfo, 0, len(scopeCatalog))

	for name, info := range scopeCatalog {
		info.Name = name
		out = append(out, info)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// maxScopesPerToken keeps a token description small.
const maxScopesPerToken = 32

// normalizeScopes checks a requested scope list against the catalog and
// returns it sorted without duplicates. An empty list, an unknown scope or a
// wildcard is an error.
func normalizeScopes(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("%w: at least one scope is required", ErrInvalidToken)
	}

	if len(in) > maxScopesPerToken {
		return nil, fmt.Errorf("%w: too many scopes", ErrInvalidToken)
	}

	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))

	for _, s := range in {
		if _, ok := scopeCatalog[s]; !ok {
			return nil, fmt.Errorf("%w: unknown scope %q", ErrInvalidToken, truncate(s, 40))
		}

		if !seen[s] {
			seen[s] = true

			out = append(out, s)
		}
	}

	sort.Strings(out)

	return out, nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}

	return s
}
