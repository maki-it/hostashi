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
	"pagecache:purge":         {Description: "Empty the page cache of sites, and nothing else (for a CMS that purges on save)"},
	"sites.snippets:write":    {Description: "Change the custom nginx snippets of sites (checked against an allowlist)", HighRisk: true},
	"objectcache:read":        {Description: "Read the object cache of sites: size, state and memory use"},
	"objectcache:write":       {Description: "Turn the object cache of sites on or off, flush it, and (admins and managers) change its size"},
	"cron:read":               {Description: "List the cron jobs of sites with their last and next runs"},
	"cron:write":              {Description: "Create, change, pause, run and delete cron jobs (they run commands as the site user)", HighRisk: true},
	"git:write":               {Description: "Set the Git repository, token, deploy key and build commands of sites (the commands run as the site user)", HighRisk: true},
	"git:deploy":              {Description: "Start a Git deploy of sites, roll back to a kept release and read the deploy logs (runs the stored build commands as the site user)"},
	"ssh:read":                {Description: "List the SSH keys of sites with their fingerprints and access"},
	"ssh:write":               {Description: "Add, change and delete SSH keys of sites (a key with shell access is a login as the site user)", HighRisk: true},
	"limits:read":             {Description: "Read the limit presets and the resource limits and disk use of sites"},
	"limits:write":            {Description: "Change the limit presets and the resource limits of sites"},
	"pages:read":              {Description: "List maintenance and parking pages and their files"},
	"pages:write":             {Description: "Upload, replace and delete maintenance and parking pages"},
	"databases:read":          {Description: "List databases and their users"},
	"databases:write":         {Description: "Create databases and users, set passwords, remove users"},
	"databases:delete":        {Description: "Delete databases and their data", HighRisk: true},
	"db_servers:write":        {Description: "Add, check and remove external database servers (stores their administrator password)", HighRisk: true},
	"backups:read":            {Description: "Read backup settings, status and snapshots"},
	"backups:write":           {Description: "Change backup settings, confirm the target's host key, start a backup"},
	"audit:read":              {Description: "Read and export the audit log of the panel (every account, site, action and the IP addresses involved)"},
	"doctor:read":             {Description: "Run the read-only checks of hostashi doctor and read their results"},
	"services:read":           {Description: "Read the state and restarts of the server's services and the dashboard's CPU, memory, disk and network figures"},
	"services:write":          {Description: "Start, stop, restart and reload the server's services where the service catalog allows it", HighRisk: true},
	"system:read":             {Description: "Read the server's hostname, time zone, clock, uptime and whether a reboot is pending"},
	"system:write":            {Description: "Change the server's time zone and hostname, rename storage pools or pick the default one, switch automatic Hostashi updates, and reboot the server", HighRisk: true},
	"firewall:read":           {Description: "Read the firewall's opened ports and the allowlists for SSH and the panel"},
	"firewall:write":          {Description: "Open ports and set the allowlists for SSH and the panel (a wrong list locks out until the rollback)", HighRisk: true},
	"php:install":             {Description: "Install more PHP versions from sury.org (runs apt as root with a fixed package list)", HighRisk: true},
	"node:install":            {Description: "Install the Node.js LTS builds Hostashi pins from nodejs.org (downloads and unpacks them as root, checked against a fixed SHA-256)", HighRisk: true},
	"python:install":          {Description: "Install the CPython builds Hostashi pins from python-build-standalone (downloads and unpacks them as root, checked against a fixed SHA-256)", HighRisk: true},
	"metrics:read":            {Description: "Read the Prometheus metrics endpoint"},
	"migrations:read":         {Description: "List uploaded CloudPanel scan reports and read their migration plans"},
	"migrations:write":        {Description: "Upload and delete CloudPanel scan reports"},
	"backups:restore":         {Description: "Restore a site's backups into its home", HighRisk: true},
	"backups:delete":          {Description: "Delete the backups of deleted sites (removed by the next backup run)", HighRisk: true},
	"files:read":              {Description: "List and download the files of sites"},
	"files:write":             {Description: "Upload, change, move, copy and delete the files of sites (this changes the sites' code)", HighRisk: true},
	"malware:read":            {Description: "Read the malware scan of sites: schedule, runs and flagged files (paths and signatures, never their content)"},
	"malware:write":           {Description: "Start malware scans of sites and quarantine flagged files; (admins and managers) change the schedule, mark false positives and put quarantined files back"},
	"malware.engine:write":    {Description: "Install or remove ClamAV for the malware scan (runs apt as root with a fixed package list)", HighRisk: true},
	"settings:write":          {Description: "Create, change, import and delete the own application profiles that sites can follow"},
	"logs:read":               {Description: "Read and follow the access, error and PHP logs of sites (they hold visitors' IP addresses)"},
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

// maxScopesPerToken keeps a token description small. A token with every
// scope of the catalog must still fit (TestScopeCatalogFitsOneToken).
const maxScopesPerToken = 64

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
