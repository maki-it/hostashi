// SPDX-License-Identifier: AGPL-3.0-or-later

// Package scan reads the configuration of a CloudPanel server from the file
// system (read-only, root-confined) and turns it into Hostashi site
// descriptions plus findings that say what could not be adopted cleanly
// (ADR-0017). It runs on the old server, or on a read-only copy of its disk,
// and never writes. Site-owned files are only checked for existence and mode,
// except the crontab, which is read as data after an owner check.
package scan

import "time"

// Status of a site after the scan.
type Status string

const (
	// StatusClean: every directive was understood and maps onto the model.
	// Automatic, lossless migrations (for example FPM over TCP to a socket)
	// are info findings and keep the site clean.
	StatusClean Status = "clean"
	// StatusReview: the site can be adopted, but something is preserved as
	// an unvalidated custom snippet or needs a human decision.
	StatusReview Status = "review"
	// StatusBlocked: the site must not be adopted automatically.
	StatusBlocked Status = "blocked"
)

// Severity of one finding.
type Severity string

const (
	SevInfo    Severity = "info"
	SevReview  Severity = "review"
	SevBlocker Severity = "blocker"
)

// Finding is one observation. Msg never contains values of env[...] entries,
// file contents of site-controlled files or key material.
type Finding struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Msg      string   `json:"msg"`
	Source   string   `json:"source,omitempty"`
	Line     int      `json:"line,omitempty"`
}

// Custom is a directive that Hostashi does not model. It is preserved
// verbatim so that a human can review it; it must never be written to a live
// configuration without validation (S2: nginx -t on a candidate generation).
type Custom struct {
	Context string `json:"context"`
	Raw     string `json:"raw"`
	Source  string `json:"source"`
	Line    int    `json:"line"`
}

// TLS describes the certificate a site presents today.
type TLS struct {
	Kind     string    `json:"kind"` // none, letsencrypt, custom, selfsigned, missing
	CertPath string    `json:"cert_path,omitempty"`
	KeyPath  string    `json:"key_path,omitempty"`
	NotAfter time.Time `json:"not_after,omitempty"`
	Names    []string  `json:"names,omitempty"`
}

// Pool is the PHP-FPM pool serving a site.
type Pool struct {
	Name       string            `json:"name"`
	Version    string            `json:"php_version,omitempty"`
	User       string            `json:"user"`
	Group      string            `json:"group"`
	Listen     string            `json:"listen"`
	PM         string            `json:"pm,omitempty"`
	MaxChldren int               `json:"pm_max_children,omitempty"`
	Settings   map[string]string `json:"settings,omitempty"` // php_admin_value / php_value, no env
	EnvKeys    []string          `json:"env_keys,omitempty"` // names only, values are never stored
	Source     string            `json:"source"`
}

// CronJob is one crontab line, command kept opaque.
type CronJob struct {
	Schedule string `json:"schedule"`
	Command  string `json:"command"`
	Line     int    `json:"line"`
}

// SSHKey is one public key from the site user's authorized_keys. Public
// is the key in canonical form (type and base64, no options, no comment).
type SSHKey struct {
	Public      string `json:"public"`
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment,omitempty"`
	Source      string `json:"source"`
	Line        int    `json:"line"`
}

// Site is the adopted view of one vhost.
type Site struct {
	Name          string    `json:"name"`
	User          string    `json:"user"`
	Type          string    `json:"type"` // static, php, wordpress, nodejs, python, proxy
	Domains       []string  `json:"domains"`
	RedirectFrom  []string  `json:"redirect_from,omitempty"` // hosts that only redirect to Domains[0]
	DocRoot       string    `json:"doc_root"`
	Upstream      string    `json:"upstream,omitempty"`
	Pool          *Pool     `json:"pool,omitempty"`
	TLS           TLS       `json:"tls"`
	Cron          []CronJob `json:"cron,omitempty"`
	ClientMaxBody string    `json:"client_max_body_size,omitempty"`
	Custom        []Custom  `json:"custom,omitempty"`
	// SSHKeys are the accepted public keys the site user logs in with
	// (ADR-0032); the importer carries them over as SFTP only.
	SSHKeys []SSHKey `json:"ssh_keys,omitempty"`
	// Usage is what the site user's home occupies; nil if not measured.
	Usage    *Usage    `json:"usage,omitempty"`
	Sources  []string  `json:"sources"`
	Status   Status    `json:"status"`
	Findings []Finding `json:"findings,omitempty"`
}

// Summary counts sites by status.
type Summary struct {
	Sites   int     `json:"sites"`
	Clean   int     `json:"clean"`
	Review  int     `json:"review"`
	Blocked int     `json:"blocked"`
	CleanPc float64 `json:"clean_percent"`
	// AdoptablePc counts clean and review sites.
	AdoptablePc float64 `json:"adoptable_percent"`
}

// Format names the version of the report layout. The importer refuses a
// report with another value instead of guessing what its fields mean.
const Format = "hostashi-cloudpanel-scan/1"

// Report is the result of one scan.
type Report struct {
	Format  string    `json:"format"`
	Sites   []Site    `json:"sites"`
	Ignored []Finding `json:"ignored,omitempty"` // panel vhost, default server, ...
	Orphans []Finding `json:"orphans,omitempty"` // pools, users, crontabs without a site
	Global  []Finding `json:"global,omitempty"`  // scan-level problems
	Summary Summary   `json:"summary"`
}
