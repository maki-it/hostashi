// SPDX-License-Identifier: AGPL-3.0-or-later

package scan

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/maki-it/hostashi/internal/agentapi"
	"github.com/maki-it/hostashi/internal/nginxconf"
)

// Options for one scan.
type Options struct {
	// Root is the directory that stands for "/" (a fixture tree in tests,
	// "/" or a read-only mount of the old server's disk in real use).
	Root string
	// RequireRootOwned refuses config files that are not owned by root or
	// are writable by group or others, and crontabs not owned by their user.
	RequireRootOwned bool
	// Now is used for certificate expiry; zero means time.Now().
	Now time.Time
}

const (
	sitesEnabled = "/etc/nginx/sites-enabled"
	maxVhosts    = 2000
	maxSmall     = 256 << 10
	minSiteUID   = 1000
	// panelUser is the system user CloudPanel itself runs as [U: from memory,
	// not checked against a real instance].
	panelUser = "clp"
)

var phpVersionDirRe = regexp.MustCompile(`^[5-9]\.[0-9]{1,2}$`)

type poolRef struct {
	def  *poolDef
	used bool
}

// Run scans the tree. It never writes anything.
func Run(o Options) (*Report, error) {
	r, err := newReader(o.Root, o.RequireRootOwned)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	now := o.Now
	if now.IsZero() {
		now = time.Now()
	}

	rep := &Report{Format: Format}
	s := &scanner{r: r, now: now, rep: rep, inc: includer{r: r}}

	s.loadPasswd()
	s.loadPools()

	names, err := r.ReadDir(sitesEnabled, maxVhosts)
	if err != nil {
		return nil, fmt.Errorf("listing %s: %w", sitesEnabled, err)
	}

	if r.Exists("/home/" + panelUser) {
		rep.Global = append(rep.Global, Finding{SevInfo, "panel_detected", "home of the panel user " + panelUser + " exists; looks like a CloudPanel server", "", 0})
	}

	for _, n := range names {
		s.vhostFile(path.Join(sitesEnabled, n))
	}

	s.orphans()

	sort.Slice(rep.Sites, func(i, j int) bool { return rep.Sites[i].Name < rep.Sites[j].Name })

	for i := range rep.Sites {
		st := &rep.Sites[i]
		st.Status = statusOf(st.Findings)
		rep.Summary.Sites++

		switch st.Status {
		case StatusClean:
			rep.Summary.Clean++
		case StatusReview:
			rep.Summary.Review++
		case StatusBlocked:
			rep.Summary.Blocked++
		}
	}

	if n := rep.Summary.Sites; n > 0 {
		rep.Summary.CleanPc = 100 * float64(rep.Summary.Clean) / float64(n)
		rep.Summary.AdoptablePc = 100 * float64(rep.Summary.Clean+rep.Summary.Review) / float64(n)
	}

	return rep, nil
}

func statusOf(fs []Finding) Status {
	st := StatusClean

	for _, f := range fs {
		switch f.Severity {
		case SevBlocker:
			return StatusBlocked
		case SevReview:
			st = StatusReview
		case SevInfo:
		}
	}

	return st
}

type scanner struct {
	r      *reader
	now    time.Time
	rep    *Report
	inc    includer
	users  map[string]passwdEntry
	pools  map[string]*poolRef // by listenKey
	poolsL []*poolRef
	used   map[string]bool // site users that have a site
}

func (s *scanner) global(sev Severity, code, msg, src string) {
	s.rep.Global = append(s.rep.Global, Finding{sev, code, msg, src, 0})
}

func (s *scanner) loadPasswd() {
	s.users = map[string]passwdEntry{}
	s.used = map[string]bool{}

	b, err := s.r.ReadFile("/etc/passwd", 4<<20)
	if err != nil {
		s.global(SevBlocker, "passwd_unreadable", err.Error(), "/etc/passwd")
		return
	}

	var fs []Finding

	s.users, fs = parsePasswd(string(b))
	s.rep.Global = append(s.rep.Global, fs...)
}

func (s *scanner) loadPools() {
	s.pools = map[string]*poolRef{}

	versions, err := s.r.ReadDir("/etc/php", 64)
	if err != nil {
		s.global(SevInfo, "php_none", "no /etc/php directory", "/etc/php")
		return
	}

	for _, v := range versions {
		if !phpVersionDirRe.MatchString(v) {
			continue
		}

		dir := "/etc/php/" + v + "/fpm/pool.d"

		files, err := s.r.ReadDir(dir, 2000)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				s.global(SevReview, "pool_dir_unreadable", err.Error(), dir)
			}

			continue
		}

		for _, f := range files {
			if !strings.HasSuffix(f, ".conf") {
				continue
			}

			p := dir + "/" + f

			b, err := s.r.ReadFile(p, maxSmall)
			if err != nil {
				s.global(SevReview, "pool_unreadable", err.Error(), p)
				continue
			}

			defs, fs := parsePools(string(b), p, v)
			s.rep.Global = append(s.rep.Global, fs...)

			for i := range defs {
				ref := &poolRef{def: &defs[i]}
				s.poolsL = append(s.poolsL, ref)

				if defs[i].Listen == "" {
					continue
				}

				k := listenKey(defs[i].Listen, false)
				if prev, dup := s.pools[k]; dup {
					s.global(SevBlocker, "pool_listen_conflict", fmt.Sprintf("pools %s and %s listen on the same address", prev.def.Name, defs[i].Name), p)
				}

				s.pools[k] = ref
			}
		}
	}
}

func (s *scanner) vhostFile(p string) {
	base := path.Base(p)
	site := Site{Name: strings.TrimSuffix(base, ".conf"), Sources: []string{p}}
	block := func(code, msg string, line int) {
		site.Findings = append(site.Findings, Finding{SevBlocker, code, msg, p, line})
		s.rep.Sites = append(s.rep.Sites, site)
	}

	b, err := s.r.ReadFile(p, nginxconf.MaxBytes)
	if err != nil {
		block("vhost_unreadable", err.Error(), 0)
		return
	}

	ds, err := nginxconf.Parse(string(b))
	if err != nil {
		block("vhost_syntax", err.Error(), 0)
		return
	}

	var servers []server

	for _, d := range ds {
		if d.Name == "server" && d.IsBlock() {
			servers = append(servers, analyseServer(d, p, s.inc))
			continue
		}

		site.Custom = append(site.Custom, Custom{Context: "file", Raw: d.Raw, Source: p, Line: d.Line})
		site.Findings = append(site.Findings, Finding{SevReview, "vhost_outside_server", "directive " + d.Name + " outside a server block", p, d.Line})
	}

	var content, redirects []server

	for _, sv := range servers {
		switch {
		case sv.Panel:
			s.rep.Ignored = append(s.rep.Ignored, Finding{SevInfo, "panel_vhost", "server on port " + panelPort + " is the old panel itself", p, sv.Line})
		case sv.Default && (len(sv.Names) == 0 || (len(sv.Names) == 1 && sv.Names[0] == "_")):
			s.rep.Ignored = append(s.rep.Ignored, Finding{SevInfo, "default_server", "catch-all default server, Hostashi has its own", p, sv.Line})
		case sv.RedirectOnly:
			redirects = append(redirects, sv)
		default:
			content = append(content, sv)
		}
	}

	if len(content) == 0 {
		if len(redirects) > 0 {
			s.rep.Ignored = append(s.rep.Ignored, Finding{SevReview, "redirect_only_file", "file contains only redirects and no site", p, 0})
		}

		return
	}

	primary := content[0]
	for _, c := range content[1:] {
		if c.SSL && !primary.SSL {
			primary = c
		}
	}
	// Two-tier vhost: the TLS server proxies to a loopback port that
	// another server block in the same file listens on (seen on web2).
	// The backend block is the application; the front block only
	// terminates TLS. A cache (for example Varnish) may sit in between.
	app := primary

	var backend *server

	if port, ok := loopbackPort(primary.ProxyPass); ok {
		if port == panelPort {
			s.rep.Ignored = append(s.rep.Ignored, Finding{SevInfo, "panel_proxy", "vhost proxies to the old panel on port " + panelPort + " (panel under its own domain)", p, primary.Line})
			return
		}

		var others []int

		for i, c := range content {
			if c.Line == primary.Line {
				continue
			}

			others = append(others, i)
			if hasPort(c, port) {
				backend = &content[i]
			}
		}

		if backend == nil && len(others) == 1 && !content[others[0]].SSL {
			backend = &content[others[0]]
			site.Findings = append(site.Findings, Finding{SevReview, "vhost_cache_tier", fmt.Sprintf("front server proxies to port %s, backend block listens on %s: a cache (likely Varnish) sits in between and is not adopted", port, strings.Join(backend.Ports, ",")), p, primary.Line})
		}

		if backend != nil {
			site.Findings = append(site.Findings, Finding{SevInfo, "vhost_two_tier", "TLS server proxies to a backend server block in the same file; adopted as one site", p, backend.Line})

			app.Root = backend.Root
			if app.Root == "" {
				app.Root = primary.Root
			}

			app.FastCGI, app.ProxyPass = backend.FastCGI, backend.ProxyPass
			if backend.MaxBody != "" {
				app.MaxBody = backend.MaxBody
			}

			if app.FastCGI == "" && app.ProxyPass == "" {
				app.ProxyPass = "" // static backend
			}
		}
	}

	for _, c := range content {
		if c.Line == primary.Line || (backend != nil && c.Line == backend.Line) {
			continue
		}

		if c.Root != primary.Root || c.FastCGI != primary.FastCGI || c.ProxyPass != primary.ProxyPass {
			site.Findings = append(site.Findings, Finding{SevBlocker, "vhost_multi_app", "file serves different applications in several server blocks", p, c.Line})
		} else {
			site.Findings = append(site.Findings, Finding{SevInfo, "vhost_plain_http", "site is also served without TLS on port 80; Hostashi will redirect to HTTPS instead", p, c.Line})
		}
	}

	tiers := []server{primary}
	if backend != nil {
		tiers = append(tiers, *backend)
	}

	for ti, t := range tiers {
		site.Findings = append(site.Findings, t.Findings...)
		for _, c := range t.Custom {
			if ti == 1 {
				c.Context = "backend " + c.Context
			}

			site.Custom = append(site.Custom, c)
			site.Findings = append(site.Findings, Finding{SevReview, "custom_directive", c.Context + ": not modeled, preserved as custom snippet", c.Source, c.Line})
		}
	}

	// Domains
	seen := map[string]bool{}
	for _, n := range primary.Names {
		if d, ok := s.domain(&site, n, p, primary.Line, seen); ok {
			site.Domains = append(site.Domains, d)
		}
	}

	if len(site.Domains) == 0 {
		block("no_domain", "server block has no usable server_name", primary.Line)
		return
	}

	site.Name = site.Domains[0]

	// Redirect servers in the same file
	for _, rs := range redirects {
		host, same, ok := redirectTarget(rs.Return)
		switch {
		case ok && same:
			site.Findings = append(site.Findings, Finding{SevInfo, "https_redirect", "HTTP to HTTPS redirect, Hostashi default", p, rs.Line})
		case ok && seen[strings.ToLower(host)]:
			for _, n := range rs.Names {
				if d, ok := s.domain(&site, n, p, rs.Line, map[string]bool{}); ok && !seen[d] {
					site.RedirectFrom = append(site.RedirectFrom, d)
				}
			}
		default:
			site.Custom = append(site.Custom, Custom{Context: "server (redirect)", Raw: strings.Join(rs.Return, " "), Source: p, Line: rs.Line})
			site.Findings = append(site.Findings, Finding{SevReview, "custom_redirect", "redirect to a host outside the site", p, rs.Line})
		}
	}

	// Site user from the document root
	user, ok := s.siteUser(&site, app, p)
	if !ok {
		s.rep.Sites = append(s.rep.Sites, site)
		return
	}

	site.User = user
	s.used[user] = true

	s.siteType(&site, app, p)
	s.tls(&site, primary, p)
	s.cron(&site)

	if app.MaxBody != "" {
		site.ClientMaxBody = app.MaxBody
	}

	s.rep.Sites = append(s.rep.Sites, site)
}

// loopbackPort returns the port of an http://127.0.0.1:N or localhost URL.
func loopbackPort(u string) (string, bool) {
	pu, err := url.Parse(u)
	if err != nil || pu.Port() == "" {
		return "", false
	}

	h := pu.Hostname()
	if ip := net.ParseIP(h); h != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return "", false
	}

	return pu.Port(), true
}

func hasPort(sv server, port string) bool {
	for _, p := range sv.Ports {
		if p == port {
			return true
		}
	}

	return false
}

func (s *scanner) domain(site *Site, n, p string, line int, seen map[string]bool) (string, bool) {
	if n == "_" || n == "" {
		return "", false
	}

	d := strings.ToLower(n)
	switch {
	case !utf8.ValidString(d) || strings.IndexFunc(d, func(r rune) bool { return r > 127 }) >= 0:
		site.Findings = append(site.Findings, Finding{SevBlocker, "domain_not_punycode", "server_name is not ASCII; expected punycode", p, line})
		return "", false
	case strings.HasPrefix(d, "*.") || strings.HasPrefix(d, "~") || strings.HasPrefix(d, "."):
		site.Findings = append(site.Findings, Finding{SevReview, "domain_pattern", "wildcard or regex server_name " + d + " is not modeled", p, line})
		return "", false
	}

	if err := agentapi.ValidateDomain(d); err != nil {
		site.Findings = append(site.Findings, Finding{SevBlocker, "domain_invalid", err.Error(), p, line})
		return "", false
	}

	if seen[d] {
		return "", false
	}

	seen[d] = true

	return d, true
}

func (s *scanner) siteUser(site *Site, sv server, p string) (string, bool) {
	fail := func(code, msg string) (string, bool) {
		site.Findings = append(site.Findings, Finding{SevBlocker, code, msg, p, sv.Line})
		return "", false
	}

	root := sv.Root
	if root == "" {
		return fail("no_root", "no document root, cannot tell which user owns the site")
	}

	if !strings.HasPrefix(root, "/") || path.Clean(root) != strings.TrimSuffix(root, "/") || strings.Contains(root, "$") {
		return fail("root_unclean", "document root is not a clean absolute path")
	}

	parts := strings.Split(strings.TrimPrefix(path.Clean(root), "/"), "/")
	if len(parts) < 2 || parts[0] != "home" {
		return fail("root_outside_home", "document root "+root+" is not below /home/<user>")
	}

	user := parts[1]
	site.DocRoot = path.Clean(root)

	pw, ok := s.users[user]
	switch {
	case !ok:
		return fail("user_missing", "no passwd entry for "+user)
	case pw.UID < minSiteUID:
		return fail("user_system", fmt.Sprintf("user %s has uid %d (system range)", user, pw.UID))
	case path.Clean(pw.Home) != "/home/"+user:
		return fail("user_home_mismatch", fmt.Sprintf("home of %s is %s, not /home/%s", user, pw.Home, user))
	}

	if err := agentapi.ValidateSiteUser(user); err != nil {
		site.Findings = append(site.Findings, Finding{SevReview, "user_name_nonstandard", err.Error(), p, sv.Line})
	}

	if m, err := s.r.Mode("/home/" + user); err != nil || !m.IsDir() {
		return fail("home_missing", "home directory /home/"+user+" missing or not a directory")
	} else if m.Perm()&0o007 != 0 {
		site.Findings = append(site.Findings, Finding{SevInfo, "home_world_access", fmt.Sprintf("home mode %04o gives others access; adoption tightens it", m.Perm()), "/home/" + user, 0})
	}

	if m, err := s.r.Mode(site.DocRoot); err != nil || !m.IsDir() {
		site.Findings = append(site.Findings, Finding{SevReview, "docroot_missing", "document root does not exist or is not a directory", p, sv.Line})
	}

	return user, true
}

func (s *scanner) siteType(site *Site, sv server, p string) {
	switch {
	case sv.FastCGI != "":
		site.Type = "php"

		ref, ok := s.pools[listenKey(sv.FastCGI, true)]
		if !ok {
			site.Findings = append(site.Findings, Finding{SevBlocker, "php_pool_missing", "no FPM pool listens on " + sv.FastCGI, p, sv.Line})
			return
		}

		ref.used = true
		pd := ref.def
		pool := pd.Pool
		site.Pool = &pool
		site.Sources = append(site.Sources, pd.Source)

		site.Findings = append(site.Findings, pd.Findings...)
		if pd.User != site.User {
			site.Findings = append(site.Findings, Finding{SevBlocker, "pool_user_mismatch", fmt.Sprintf("pool runs as %q, but the site belongs to %q", pd.User, site.User), pd.Source, pd.Line})
		}

		if strings.HasPrefix(listenKey(pd.Listen, false), "tcp:") {
			site.Findings = append(site.Findings, Finding{SevInfo, "fpm_tcp_migrate", "pool listens on TCP " + pd.Listen + ", reachable by every local process; Hostashi moves it to a unix socket owned by the site", pd.Source, pd.Line})
		}

		if s.r.Exists(site.DocRoot+"/wp-config.php") || s.r.Exists(site.DocRoot+"/wp-includes") {
			site.Type = "wordpress"
		}
	case sv.ProxyPass != "":
		site.Upstream = sv.ProxyPass

		u, err := url.Parse(sv.ProxyPass)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			site.Type = "proxy"
			site.Findings = append(site.Findings, Finding{SevReview, "proxy_upstream", "upstream " + sv.ProxyPass + " is not a plain http(s) URL", p, sv.Line})

			return
		}

		ip := net.ParseIP(u.Hostname())
		loopback := u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback())

		home := "/home/" + site.User
		switch {
		case !loopback:
			site.Type = "proxy"
		case s.anyExists(site.DocRoot, home+"/htdocs", "package.json"):
			site.Type = "nodejs"
		case s.anyExists(site.DocRoot, home+"/htdocs", "requirements.txt", "pyproject.toml"):
			site.Type = "python"
		default:
			site.Type = "proxy"
			site.Findings = append(site.Findings, Finding{SevReview, "proxy_local_unknown", "local upstream, but no Node.js or Python project found; the process manager is not adopted", p, sv.Line})
		}

		if site.Type == "nodejs" || site.Type == "python" {
			site.Findings = append(site.Findings, Finding{SevReview, "app_process", "the app process (port " + u.Port() + ") is started outside nginx; how it is supervised today is not scanned", p, sv.Line})
		}
	default:
		site.Type = "static"
	}
}

// anyExists checks file names in a few directories without reading them.
func (s *scanner) anyExists(dirs ...string) bool {
	var ds, fs []string

	for _, x := range dirs {
		if strings.HasPrefix(x, "/") {
			ds = append(ds, x)
		} else {
			fs = append(fs, x)
		}
	}

	for _, d := range ds {
		for _, f := range fs {
			if s.r.Exists(d + "/" + f) {
				return true
			}
		}
	}

	return false
}

func (s *scanner) tls(site *Site, sv server, p string) {
	if !sv.SSL {
		site.TLS = TLS{Kind: "none"}
		site.Findings = append(site.Findings, Finding{SevInfo, "tls_none", "site has no TLS today", p, sv.Line})

		return
	}

	if sv.Cert == "" || sv.Key == "" {
		site.TLS = TLS{Kind: "missing"}
		site.Findings = append(site.Findings, Finding{SevBlocker, "tls_incomplete", "TLS listener without certificate or key", p, sv.Line})

		return
	}

	b, err := s.r.ReadFile(sv.Cert, maxSmall)
	if err != nil {
		site.TLS = TLS{Kind: "missing", CertPath: sv.Cert, KeyPath: sv.Key}
		site.Findings = append(site.Findings, Finding{SevReview, "tls_unreadable", "certificate: " + err.Error(), p, sv.Line})

		return
	}

	t, fs := inspectCert(b, site.Domains, s.now)
	t.CertPath, t.KeyPath = sv.Cert, sv.Key
	site.TLS = t

	for i := range fs {
		fs[i].Source = sv.Cert
	}

	site.Findings = append(site.Findings, fs...)
	if m, err := s.r.Mode(sv.Key); err != nil {
		site.Findings = append(site.Findings, Finding{SevReview, "tls_key_missing", "private key file not found", sv.Key, 0})
	} else if m.Perm()&0o004 != 0 {
		dm, derr := s.r.Mode(path.Dir(sv.Key))
		if derr == nil && dm.Perm()&0o001 == 0 {
			site.Findings = append(site.Findings, Finding{SevInfo, "tls_key_mode", fmt.Sprintf("private key mode %04o, but its directory (%04o) is closed to others", m.Perm(), dm.Perm()), sv.Key, 0})
		} else {
			site.Findings = append(site.Findings, Finding{SevReview, "tls_key_world_readable", fmt.Sprintf("private key mode %04o and its directory let every local user read it", m.Perm()), sv.Key, 0})
		}
	}
}

func (s *scanner) cron(site *Site) {
	p := "/var/spool/cron/crontabs/" + site.User
	if !s.r.Exists(p) {
		return
	}

	b, err := s.r.ReadOwned(p, s.users[site.User].UID, maxSmall)
	if err != nil {
		site.Findings = append(site.Findings, Finding{SevReview, "cron_unreadable", err.Error(), p, 0})
		return
	}

	jobs, fs := parseCrontab(string(b), p, site.User)
	site.Cron = jobs
	site.Sources = append(site.Sources, p)
	site.Findings = append(site.Findings, fs...)
}

func (s *scanner) orphans() {
	for _, ref := range s.poolsL {
		d := ref.def
		switch {
		case ref.used:
		case d.User == "www-data":
			s.rep.Ignored = append(s.rep.Ignored, Finding{SevInfo, "pool_default", "default pool " + d.Name + " running as www-data", d.Source, d.Line})
		default:
			s.rep.Orphans = append(s.rep.Orphans, Finding{SevReview, "pool_orphan", fmt.Sprintf("pool %s (user %s) is not used by any vhost", d.Name, d.User), d.Source, d.Line})
		}
	}

	var names []string
	for n := range s.users {
		names = append(names, n)
	}

	sort.Strings(names)

	for _, n := range names {
		u := s.users[n]
		if u.UID < minSiteUID || u.UID == 65534 || s.used[n] || !strings.HasPrefix(u.Home, "/home/") {
			continue
		}

		if n == panelUser {
			s.rep.Ignored = append(s.rep.Ignored, Finding{SevInfo, "panel_user", "panel system user " + n, "/etc/passwd", u.Line})
			continue
		}

		s.rep.Orphans = append(s.rep.Orphans, Finding{SevReview, "user_orphan", "user " + n + " has a home below /home but no site", "/etc/passwd", u.Line})
	}

	if tabs, err := s.r.ReadDir("/var/spool/cron/crontabs", 2000); err == nil {
		for _, t := range tabs {
			if !s.used[t] {
				s.rep.Orphans = append(s.rep.Orphans, Finding{SevReview, "cron_orphan", "crontab of " + t + " belongs to no site; not adopted", "/var/spool/cron/crontabs/" + t, 0})
			}
		}
	}
}
