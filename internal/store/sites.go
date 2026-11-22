// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Site states (see migration 0006).
const (
	SiteCreating = "creating"
	SiteActive   = "active"
	SiteDeleting = "deleting"
)

// Errors of the site tables.
var (
	ErrSiteExists  = errors.New("store: a site with this name exists")
	ErrDomainTaken = errors.New("store: domain belongs to another site")
)

// Site is a stored site with its domains, the primary one first.
type Site struct {
	ID    int64
	Name  string
	Pool  string
	State string
	// Type is one of the SiteType constants (ADR-0054).
	Type string
	PHP  string // PHP version, empty for no PHP
	// WebDir is the document root folder below htdocs; SPA and AssetCache
	// are the static-site options (ADR-0054 addendum 1).
	WebDir     string
	SPA        bool
	AssetCache bool
	// AssetCacheSeconds and AssetCacheExts: 0 and empty use the defaults.
	AssetCacheSeconds int
	AssetCacheExts    []string
	// Account: the site has a Linux account and home. A redirect site is
	// created without one (ADR-0054).
	Account bool
	// Redirect is the redirect of a redirect site; it is kept after a
	// type change (nil: never set).
	Redirect *SiteRedirect
	// Proxy is the target of a reverse proxy site; it is kept after a
	// type change (nil: never set).
	Proxy   *SiteProxy
	Domains []string
	TLS     bool // HTTPS wanted
	// HSTS sends Strict-Transport-Security while the site is on HTTPS.
	HSTS bool
	// PHPPrograms lets the site's PHP start programs (ADR-0033).
	PHPPrograms bool
	// Disabled: the web server answers with 503 and PHP is stopped; the
	// account, files, databases and certificate stay.
	Disabled bool
	// DisabledPage is the maintenance page a disabled site shows (0: the
	// built-in page, ADR-0047). It is kept while the site is enabled.
	DisabledPage int64
	// Maintenance serves MaintenancePage (0: the built-in page) with 503 to
	// every address outside MaintenanceAllow (ADR-0027).
	Maintenance      bool
	MaintenancePage  int64
	MaintenanceAllow []string
	// TLSLive: the last accepted web state serves the site over HTTPS.
	TLSLive bool
	// TLSDomains and TLSNotAfter describe the live certificate (none: empty).
	TLSDomains  []string
	TLSNotAfter time.Time
	// TLSError is the reason of the last failed attempt at TLSFailedAt.
	TLSError    string
	TLSFailedAt time.Time
	// TLSSource is where the certificate should come from (TLSLetsEncrypt
	// or TLSCustom, ADR-0039); TLSCertSource is where the live one came
	// from ("" without one). TLSTrusted is false for an own certificate
	// accepted without a publicly trusted chain.
	TLSSource     string
	TLSCertSource string
	TLSTrusted    bool
	// TLSRenewAt is the last "renew now"; TLSRenewForced asks the renewal
	// loop for a new certificate regardless of the expiry.
	TLSRenewAt     time.Time
	TLSRenewForced bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Certificate sources of a site (ADR-0039).
const (
	TLSLetsEncrypt = "letsencrypt"
	TLSCustom      = "custom"
)

// Site types (ADR-0054). Until the other types arrive, a site is static or
// PHP, and the type follows its PHP version.
const (
	SiteTypeStatic   = "static"
	SiteTypePHP      = "php"
	SiteTypeNode     = "node"
	SiteTypeApp      = "app"
	SiteTypeProxy    = "proxy"
	SiteTypeRedirect = "redirect"
)

// SiteProxy is the target of a reverse proxy site (ADR-0054).
type SiteProxy struct {
	Target string
	// TargetHost sends the target's host as Host.
	TargetHost bool
	// Insecure does not verify an https target's certificate.
	Insecure bool
}

// SiteRedirect is the target of a redirect site (ADR-0054).
type SiteRedirect struct {
	Target   string
	Status   int
	KeepPath bool
}

// ErrSiteType: the change does not fit the site's type, for example a PHP
// version for a reverse proxy.
var ErrSiteType = errors.New("store: not possible for this type of site")

// phpType is the type of a static or PHP site with this PHP version.
func phpType(php string) string {
	if php == "" {
		return SiteTypeStatic
	}

	return SiteTypePHP
}

// CreateSite stores a new site in state creating together with its domains.
// Its type is php with a PHP version and static without one.
func (s *Store) CreateSite(ctx context.Context, name, pool, php string, domains []string, now time.Time) (Site, error) {
	return s.createSite(ctx, Site{Name: name, Pool: pool, Type: phpType(php), PHP: php, Account: true}, domains, now)
}

// CreateRedirectSite stores a new redirect site without a Linux account in
// state creating together with its domains (ADR-0054).
func (s *Store) CreateRedirectSite(ctx context.Context, name, pool string, r SiteRedirect, domains []string, now time.Time) (Site, error) {
	return s.createSite(ctx, Site{Name: name, Pool: pool, Type: SiteTypeRedirect, Redirect: &r}, domains, now)
}

// CreateProxySite stores a new reverse proxy site without a Linux account
// in state creating together with its domains (ADR-0054).
func (s *Store) CreateProxySite(ctx context.Context, name, pool string, p SiteProxy, domains []string, now time.Time) (Site, error) {
	return s.createSite(ctx, Site{Name: name, Pool: pool, Type: SiteTypeProxy, Proxy: &p}, domains, now)
}

func (s *Store) createSite(ctx context.Context, site Site, domains []string, now time.Time) (Site, error) {
	name, pool, php := site.Name, site.Pool, site.PHP

	var r SiteRedirect
	if site.Redirect != nil {
		r = *site.Redirect
	} else {
		r.Status = 301
	}

	var p SiteProxy
	if site.Proxy != nil {
		p = *site.Proxy
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var n int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sites WHERE name = ?`, name).Scan(&n); err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	if n > 0 {
		return Site{}, ErrSiteExists
	}

	res, err := tx.ExecContext(ctx,
		`INSERT INTO sites (name, pool, state, type, php, account, redirect_target, redirect_status, redirect_keep_path,
			proxy_target, proxy_target_host, proxy_insecure, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		name, pool, SiteCreating, site.Type, php, boolInt(site.Account), r.Target, r.Status, boolInt(r.KeepPath),
		p.Target, boolInt(p.TargetHost), boolInt(p.Insecure), now.Unix(), now.Unix())
	if err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	if err := insertDomains(ctx, tx, id, domains); err != nil {
		return Site{}, err
	}

	if err := tx.Commit(); err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	t := time.Unix(now.Unix(), 0).UTC()

	site.ID, site.State, site.Domains, site.TLSDomains, site.CreatedAt, site.UpdatedAt = id, SiteCreating, domains, []string{}, t, t

	return site, nil
}

func insertDomains(ctx context.Context, tx *sql.Tx, siteID int64, domains []string) error {
	for i, d := range domains {
		var owner int64

		err := tx.QueryRowContext(ctx, `SELECT site_id FROM site_domains WHERE domain = ?`, d).Scan(&owner)

		switch {
		case err == nil && owner != siteID:
			return fmt.Errorf("%w: %s", ErrDomainTaken, d)
		case err != nil && !errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("store: %w", err)
		}

		// A parked domain that becomes part of a site is no longer parked.
		if _, err := tx.ExecContext(ctx, `DELETE FROM parked_domains WHERE domain = ?`, d); err != nil {
			return fmt.Errorf("store: %w", err)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO site_domains (domain, site_id, position) VALUES (?, ?, ?)`, d, siteID, i); err != nil {
			return fmt.Errorf("store: %w", err)
		}
	}

	return nil
}

// SetSiteState changes the state of a site.
func (s *Store) SetSiteState(ctx context.Context, name, state string, now time.Time) error {
	res, err := s.w.ExecContext(ctx,
		`UPDATE sites SET state = ?, updated_at = ? WHERE name = ?`, state, now.Unix(), name)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}

	return nil
}

// SetSitePHP sets the PHP version of a site and returns the previous one.
// On a static or PHP site the type follows: a version makes it php, ""
// makes it static. Other types have no PHP: ErrSiteType.
func (s *Store) SetSitePHP(ctx context.Context, name, php string, now time.Time) (string, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var old, typ string
	if err := tx.QueryRowContext(ctx, `SELECT php, type FROM sites WHERE name = ?`, name).Scan(&old, &typ); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}

		return "", fmt.Errorf("store: %w", err)
	}

	if typ != SiteTypeStatic && typ != SiteTypePHP {
		return "", ErrSiteType
	}

	if _, err := tx.ExecContext(ctx, `UPDATE sites SET php = ?, type = ?, updated_at = ? WHERE name = ?`,
		php, phpType(php), now.Unix(), name); err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// ReplaceSiteDomains sets the domains of a site and returns the previous ones.
func (s *Store) ReplaceSiteDomains(ctx context.Context, name string, domains []string, now time.Time) ([]string, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var id int64
	if err := tx.QueryRowContext(ctx, `SELECT id FROM sites WHERE name = ?`, name).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}

		return nil, fmt.Errorf("store: %w", err)
	}

	old, err := domainsOf(ctx, tx, id)
	if err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM site_domains WHERE site_id = ?`, id); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	if err := insertDomains(ctx, tx, id, domains); err != nil {
		return nil, err
	}

	if _, err := tx.ExecContext(ctx, `UPDATE sites SET updated_at = ? WHERE id = ?`, now.Unix(), id); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func domainsOf(ctx context.Context, q queryer, siteID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT domain FROM site_domains WHERE site_id = ? ORDER BY position`, siteID)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := []string{}

	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out = append(out, d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// Sites returns all sites sorted by name, with their domains.
func (s *Store) Sites(ctx context.Context) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+siteColumns+` FROM sites ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	var out []Site

	for rows.Next() {
		st, err := scanSite(rows)
		if err != nil {
			_ = rows.Close()

			return nil, err
		}

		out = append(out, st)
	}

	if err := rows.Err(); err != nil {
		_ = rows.Close()

		return nil, fmt.Errorf("store: %w", err)
	}

	_ = rows.Close()

	// One query for all domains instead of one per site: the list backs
	// every metrics scrape, and at a thousand sites the per-site queries
	// took about 20 ms (docs/sqlite-load.md).
	domains, err := allDomains(ctx, s.db)
	if err != nil {
		return nil, err
	}

	for i := range out {
		out[i].Domains = domains[out[i].ID]
		if out[i].Domains == nil {
			out[i].Domains = []string{}
		}
	}

	return out, nil
}

// allDomains maps each site ID to its domains, the primary one first.
func allDomains(ctx context.Context, q queryer) (map[int64][]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT site_id, domain FROM site_domains ORDER BY site_id, position`)
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	defer rows.Close()

	out := map[int64][]string{}

	for rows.Next() {
		var (
			id int64
			d  string
		)

		if err := rows.Scan(&id, &d); err != nil {
			return nil, fmt.Errorf("store: %w", err)
		}

		out[id] = append(out[id], d)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}

	return out, nil
}

// SiteByName returns one site or ErrNotFound.
func (s *Store) SiteByName(ctx context.Context, name string) (Site, error) {
	st, err := scanSite(s.db.QueryRowContext(ctx, `SELECT `+siteColumns+` FROM sites WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Site{}, ErrNotFound
	}

	if err != nil {
		return Site{}, err
	}

	if st.Domains, err = domainsOf(ctx, s.db, st.ID); err != nil {
		return Site{}, err
	}

	return st, nil
}

type scanner interface{ Scan(dest ...any) error }

const siteColumns = `id, name, pool, state, type, php, web_dir, spa, asset_cache, asset_cache_seconds, asset_cache_exts, tls, hsts, php_programs, disabled, tls_live, tls_domains, tls_not_after, tls_error, tls_failed_at,
	maintenance, coalesce(maintenance_page, 0), maintenance_allow, coalesce(disabled_page, 0), tls_source, tls_cert_source, tls_trusted, tls_renew_at, tls_renew_forced,
	account, redirect_target, redirect_status, redirect_keep_path, proxy_target, proxy_target_host, proxy_insecure, created_at, updated_at`

func scanSite(r scanner) (Site, error) {
	var (
		st                         Site
		tls, hsts, progs           int
		disabled, live             int
		maint, trusted, forced     int
		tlsDomains, allow          string
		notAfter, failed, cre, upd int64
		renewAt                    int64
		spa, assets                int
		exts                       string
		account, keepPath          int
		redirect                   SiteRedirect
		proxy                      SiteProxy
		proxyHost, insecure        int
	)

	err := r.Scan(&st.ID, &st.Name, &st.Pool, &st.State, &st.Type, &st.PHP, &st.WebDir, &spa, &assets, &st.AssetCacheSeconds, &exts, &tls, &hsts, &progs, &disabled, &live, &tlsDomains, &notAfter, &st.TLSError, &failed,
		&maint, &st.MaintenancePage, &allow, &st.DisabledPage, &st.TLSSource, &st.TLSCertSource, &trusted, &renewAt, &forced,
		&account, &redirect.Target, &redirect.Status, &keepPath, &proxy.Target, &proxyHost, &insecure, &cre, &upd)
	if errors.Is(err, sql.ErrNoRows) {
		return Site{}, err
	}

	if err != nil {
		return Site{}, fmt.Errorf("store: %w", err)
	}

	st.TLS, st.HSTS, st.Disabled, st.TLSLive = tls == 1, hsts == 1, disabled == 1, live == 1
	st.PHPPrograms = progs == 1
	st.SPA, st.AssetCache = spa == 1, assets == 1
	st.Account = account == 1

	if redirect.Target != "" {
		redirect.KeepPath = keepPath == 1
		st.Redirect = &redirect
	}

	if proxy.Target != "" {
		proxy.TargetHost, proxy.Insecure = proxyHost == 1, insecure == 1
		st.Proxy = &proxy
	}

	if exts != "" {
		st.AssetCacheExts = strings.Fields(exts)
	}

	st.Maintenance, st.MaintenanceAllow = maint == 1, strings.Fields(allow)
	st.TLSDomains = strings.Fields(tlsDomains)
	st.TLSNotAfter, st.TLSFailedAt = unixOrZero(notAfter), unixOrZero(failed)
	st.TLSTrusted, st.TLSRenewForced, st.TLSRenewAt = trusted == 1, forced == 1, unixOrZero(renewAt)
	st.CreatedAt, st.UpdatedAt = time.Unix(cre, 0).UTC(), time.Unix(upd, 0).UTC()

	return st, nil
}

func unixOrZero(v int64) time.Time {
	if v == 0 {
		return time.Time{}
	}

	return time.Unix(v, 0).UTC()
}

// SetSiteTLS sets whether a site wants HTTPS and returns the previous value.
func (s *Store) SetSiteTLS(ctx context.Context, name string, on bool, now time.Time) (bool, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var old int
	if err := tx.QueryRowContext(ctx, `SELECT tls FROM sites WHERE name = ?`, name).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}

		return false, fmt.Errorf("store: %w", err)
	}

	v := 0
	if on {
		v = 1
	}

	// A new request starts without the old failure, so it is tried at once.
	if _, err := tx.ExecContext(ctx, `UPDATE sites SET tls = ?, tls_error = '', tls_failed_at = 0, updated_at = ? WHERE name = ?`,
		v, now.Unix(), name); err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	return old == 1, nil
}

// SetSiteCert records a newly installed Let's Encrypt certificate and
// clears the error and a forced renewal.
func (s *Store) SetSiteCert(ctx context.Context, name string, domains []string, notAfter, now time.Time) error {
	res, err := s.w.ExecContext(ctx,
		`UPDATE sites SET tls_domains = ?, tls_not_after = ?, tls_error = '', tls_failed_at = 0, tls_cert_source = 'letsencrypt',
			tls_trusted = 1, tls_renew_forced = 0, updated_at = ? WHERE name = ?`,
		strings.Join(domains, " "), notAfter.Unix(), now.Unix(), name)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}

	return nil
}

// SetSiteHSTS sets whether a site sends HSTS and returns the previous value.
func (s *Store) SetSiteHSTS(ctx context.Context, name string, on bool, now time.Time) (bool, error) {
	return s.setSiteFlag(ctx, `SELECT hsts FROM sites WHERE name = ?`, `UPDATE sites SET hsts = ?, updated_at = ? WHERE name = ?`, name, on, now)
}

// SetSitePHPPrograms sets whether a site's PHP may start programs and
// returns the previous value.
func (s *Store) SetSitePHPPrograms(ctx context.Context, name string, on bool, now time.Time) (bool, error) {
	return s.setSiteFlag(ctx, `SELECT php_programs FROM sites WHERE name = ?`, `UPDATE sites SET php_programs = ?, updated_at = ? WHERE name = ?`, name, on, now)
}

// WebOptions are the document root folder and the static-site options of a
// site (ADR-0054 addendum 1).
type WebOptions struct {
	Dir               string
	SPA               bool
	AssetCache        bool
	AssetCacheSeconds int
	AssetCacheExts    []string
}

// SetSiteWebOptions sets the web options of a site and returns the
// previous ones. Proxy and redirect sites have no files: ErrSiteType.
// The folder's syntax is checked by the caller and by the column's CHECK.
func (s *Store) SetSiteWebOptions(ctx context.Context, name string, o WebOptions, now time.Time) (WebOptions, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return WebOptions{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var (
		old         WebOptions
		typ         string
		spa, assets int
		exts        string
	)

	if err := tx.QueryRowContext(ctx, `SELECT type, web_dir, spa, asset_cache, asset_cache_seconds, asset_cache_exts FROM sites WHERE name = ?`, name).
		Scan(&typ, &old.Dir, &spa, &assets, &old.AssetCacheSeconds, &exts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return WebOptions{}, ErrNotFound
		}

		return WebOptions{}, fmt.Errorf("store: %w", err)
	}

	if typ == SiteTypeProxy || typ == SiteTypeRedirect {
		return WebOptions{}, ErrSiteType
	}

	old.SPA, old.AssetCache = spa == 1, assets == 1

	if exts != "" {
		old.AssetCacheExts = strings.Fields(exts)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE sites SET web_dir = ?, spa = ?, asset_cache = ?, asset_cache_seconds = ?, asset_cache_exts = ?, updated_at = ? WHERE name = ?`,
		o.Dir, boolInt(o.SPA), boolInt(o.AssetCache), o.AssetCacheSeconds, strings.Join(o.AssetCacheExts, " "), now.Unix(), name); err != nil {
		return WebOptions{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return WebOptions{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// SetSiteDisabled sets whether a site is disabled and returns the previous
// value.
func (s *Store) SetSiteDisabled(ctx context.Context, name string, on bool, now time.Time) (bool, error) {
	return s.setSiteFlag(ctx, `SELECT disabled FROM sites WHERE name = ?`, `UPDATE sites SET disabled = ?, updated_at = ? WHERE name = ?`, name, on, now)
}

// setSiteFlag sets one 0/1 column of a site and returns its previous value.
// The statements are constants of the callers, never built from input.
func (s *Store) setSiteFlag(ctx context.Context, get, set, name string, on bool, now time.Time) (bool, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var old int
	if err := tx.QueryRowContext(ctx, get, name).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrNotFound
		}

		return false, fmt.Errorf("store: %w", err)
	}

	v := 0
	if on {
		v = 1
	}

	if _, err := tx.ExecContext(ctx, set, v, now.Unix(), name); err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("store: %w", err)
	}

	return old == 1, nil
}

// SetTLSLive records which sites the agent now serves over HTTPS; all
// others are not.
func (s *Store) SetTLSLive(ctx context.Context, names []string) error {
	b, err := json.Marshal(names)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if _, err := s.w.ExecContext(ctx,
		`UPDATE sites SET tls_live = (name IN (SELECT value FROM json_each(?)))`, string(b)); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// SetSiteTLSError records a failed attempt to get a certificate.
func (s *Store) SetSiteTLSError(ctx context.Context, name, msg string, at time.Time) error {
	if len(msg) > 1000 {
		msg = strings.ToValidUTF8(msg[:1000], "")
	}

	if _, err := s.w.ExecContext(ctx, `UPDATE sites SET tls_error = ?, tls_failed_at = ?, tls_renew_forced = 0 WHERE name = ?`,
		msg, at.Unix(), name); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// SetSiteCustomCert records an own certificate that is now live: the
// source becomes TLSCustom and HTTPS is wanted.
func (s *Store) SetSiteCustomCert(ctx context.Context, name string, domains []string, notAfter time.Time, trusted bool, now time.Time) error {
	t := 0
	if trusted {
		t = 1
	}

	res, err := s.w.ExecContext(ctx,
		`UPDATE sites SET tls = 1, tls_source = 'custom', tls_cert_source = 'custom', tls_domains = ?, tls_not_after = ?, tls_trusted = ?,
			tls_error = '', tls_failed_at = 0, tls_renew_forced = 0, updated_at = ? WHERE name = ?`,
		strings.Join(domains, " "), notAfter.Unix(), t, now.Unix(), name)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}

	return nil
}

// SetSiteTLSSource sets where the certificate of a site should come from
// and returns the previous source. The live certificate stays.
func (s *Store) SetSiteTLSSource(ctx context.Context, name, source string, now time.Time) (string, error) {
	if source != TLSLetsEncrypt && source != TLSCustom {
		return "", fmt.Errorf("store: unknown certificate source %q", source)
	}

	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var old string
	if err := tx.QueryRowContext(ctx, `SELECT tls_source FROM sites WHERE name = ?`, name).Scan(&old); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNotFound
		}

		return "", fmt.Errorf("store: %w", err)
	}

	if _, err := tx.ExecContext(ctx, `UPDATE sites SET tls_source = ?, tls_error = '', tls_failed_at = 0, updated_at = ? WHERE name = ?`,
		source, now.Unix(), name); err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// ClearSiteCert forgets the certificate of a site after it was deleted;
// the source goes back to TLSLetsEncrypt.
func (s *Store) ClearSiteCert(ctx context.Context, name string, now time.Time) error {
	res, err := s.w.ExecContext(ctx,
		`UPDATE sites SET tls_domains = '', tls_not_after = 0, tls_cert_source = '', tls_trusted = 1, tls_source = 'letsencrypt',
			tls_renew_forced = 0, tls_error = '', tls_failed_at = 0, updated_at = ? WHERE name = ?`, now.Unix(), name)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n != 1 {
		return ErrNotFound
	}

	return nil
}

// ErrTooSoon means "renew now" was asked for again within the pause.
var ErrTooSoon = errors.New("store: asked again too soon")

// RequestSiteRenew asks the renewal loop for a new certificate of a site,
// at most once per gap: ErrTooSoon when the last request is younger.
func (s *Store) RequestSiteRenew(ctx context.Context, name string, gap time.Duration, now time.Time) error {
	res, err := s.w.ExecContext(ctx,
		`UPDATE sites SET tls_renew_at = ?, tls_renew_forced = 1, tls_error = '', tls_failed_at = 0 WHERE name = ? AND tls_renew_at <= ?`,
		now.Unix(), name, now.Add(-gap).Unix())
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}

	if n, _ := res.RowsAffected(); n == 1 {
		return nil
	}

	if _, err := s.SiteByName(ctx, name); err != nil {
		return err
	}

	return ErrTooSoon
}

// DeleteSite removes a site and its domains. A missing site is not an error.
func (s *Store) DeleteSite(ctx context.Context, name string) error {
	if _, err := s.w.ExecContext(ctx, `DELETE FROM sites WHERE name = ?`, name); err != nil {
		return fmt.Errorf("store: %w", err)
	}

	return nil
}

// SetSiteRedirect changes the target of a redirect site and returns the
// previous one. Other types refuse it with ErrSiteType.
func (s *Store) SetSiteRedirect(ctx context.Context, name string, r SiteRedirect, now time.Time) (SiteRedirect, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return SiteRedirect{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var (
		old      SiteRedirect
		typ      string
		keepPath int
	)

	if err := tx.QueryRowContext(ctx, `SELECT type, redirect_target, redirect_status, redirect_keep_path FROM sites WHERE name = ?`, name).
		Scan(&typ, &old.Target, &old.Status, &keepPath); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SiteRedirect{}, ErrNotFound
		}

		return SiteRedirect{}, fmt.Errorf("store: %w", err)
	}

	if typ != SiteTypeRedirect {
		return SiteRedirect{}, ErrSiteType
	}

	old.KeepPath = keepPath == 1

	if _, err := tx.ExecContext(ctx, `UPDATE sites SET redirect_target = ?, redirect_status = ?, redirect_keep_path = ?, updated_at = ? WHERE name = ?`,
		r.Target, r.Status, boolInt(r.KeepPath), now.Unix(), name); err != nil {
		return SiteRedirect{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return SiteRedirect{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}

// SetSiteProxy changes the target of a reverse proxy site and returns the
// previous one. Other types refuse it with ErrSiteType.
func (s *Store) SetSiteProxy(ctx context.Context, name string, p SiteProxy, now time.Time) (SiteProxy, error) {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return SiteProxy{}, fmt.Errorf("store: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	var (
		old                 SiteProxy
		typ                 string
		targetHost, unverif int
	)

	if err := tx.QueryRowContext(ctx, `SELECT type, proxy_target, proxy_target_host, proxy_insecure FROM sites WHERE name = ?`, name).
		Scan(&typ, &old.Target, &targetHost, &unverif); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SiteProxy{}, ErrNotFound
		}

		return SiteProxy{}, fmt.Errorf("store: %w", err)
	}

	if typ != SiteTypeProxy {
		return SiteProxy{}, ErrSiteType
	}

	old.TargetHost, old.Insecure = targetHost == 1, unverif == 1

	if _, err := tx.ExecContext(ctx, `UPDATE sites SET proxy_target = ?, proxy_target_host = ?, proxy_insecure = ?, updated_at = ? WHERE name = ?`,
		p.Target, boolInt(p.TargetHost), boolInt(p.Insecure), now.Unix(), name); err != nil {
		return SiteProxy{}, fmt.Errorf("store: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return SiteProxy{}, fmt.Errorf("store: %w", err)
	}

	return old, nil
}
