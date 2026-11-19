// SPDX-License-Identifier: AGPL-3.0-or-later

// Package fixture builds a synthetic server tree that imitates the file
// layout of a CloudPanel v2 server, so the scanner can be tested without a
// real instance.
//
// The layout below was written from general knowledge and later confirmed by
// two read-only scans of a real CloudPanel server (spike S6, REPORT.md). All
// configuration text here is written for the tests; nothing is copied from CloudPanel's
// vhost-templates or docs (clean-room rule, ADR-0001).
//
//	/etc/nginx/sites-enabled/<domain>.conf    vhost per site
//	/etc/nginx/ssl-certificates/<domain>.crt  certificate and key
//	/etc/php/<v>/fpm/pool.d/<domain>.conf     FPM pool, TCP on 127.0.0.1
//	/home/<user>/htdocs/<domain>              document root
//	/var/spool/cron/crontabs/<user>           user crontab
package fixture

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Case is one synthetic site and the result the scanner should produce.
type Case struct {
	Vhost      string // file name in sites-enabled
	Domain     string
	WantStatus string
	WantType   string
	Note       string
}

type builder struct {
	dir     string
	now     time.Time
	uid     int
	port    int
	passwd  []string
	leCA    *ca
	otherCA *ca
	err     error
	cases   []Case
}

type ca struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

// Build writes the tree to dir and returns the site cases (22, the same
// count as Kim's server web2; the mix of cases is invented).
func Build(dir string, now time.Time) ([]Case, error) {
	b := &builder{dir: dir, now: now, uid: 1001, port: 17001}
	b.passwd = []string{
		"root:x:0:0:root:/root:/bin/bash",
		"www-data:x:33:33:www-data:/var/www:/usr/sbin/nologin",
		"nobody:x:65534:65534:nobody:/nonexistent:/usr/sbin/nologin",
		"clp:x:1000:1000::/home/clp:/bin/bash",
	}
	b.mkdir("/home/clp", 0o750)
	b.leCA = b.newCA("Let's Encrypt", "Test R3 (synthetic)")
	b.otherCA = b.newCA("Example Trust Services", "Example DV CA (synthetic)")
	b.sites()
	b.extras()
	b.write("/etc/passwd", strings.Join(b.passwd, "\n")+"\n", 0o644)

	if b.err != nil {
		return nil, b.err
	}

	return b.cases, nil
}

func (b *builder) path(p string) string { return filepath.Join(b.dir, filepath.FromSlash(p)) }

func (b *builder) mkdir(p string, mode os.FileMode) {
	if b.err != nil {
		return
	}

	if err := os.MkdirAll(b.path(p), 0o755); err != nil {
		b.err = err
		return
	}

	b.err = os.Chmod(b.path(p), mode)
}

func (b *builder) write(p, content string, mode os.FileMode) {
	if b.err != nil {
		return
	}

	if err := os.MkdirAll(filepath.Dir(b.path(p)), 0o755); err != nil {
		b.err = err
		return
	}

	if err := os.WriteFile(b.path(p), []byte(content), mode); err != nil {
		b.err = err
		return
	}

	b.err = os.Chmod(b.path(p), mode)
}

func (b *builder) chown(p string, uid int) {
	if b.err != nil || os.Geteuid() != 0 {
		return
	}

	b.err = os.Lchown(b.path(p), uid, uid)
}

func (b *builder) newCA(org, cn string) *ca {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		b.err = err
		return nil
	}

	tpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{Organization: []string{org}, CommonName: cn},
		NotBefore:             b.now.Add(-365 * 24 * time.Hour),
		NotAfter:              b.now.Add(3650 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		b.err = err
		return nil
	}

	c, _ := x509.ParseCertificate(der)

	return &ca{c, key}
}

// cert writes cert and key for names; issuer nil means self-signed.
func (b *builder) cert(domain string, names []string, issuer *ca, notAfter time.Time) {
	if b.err != nil {
		return
	}

	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    b.now.Add(-60 * 24 * time.Hour),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	parent, signer := tpl, any(key)
	if issuer != nil {
		parent, signer = issuer.cert, issuer.key
	}

	der, err := x509.CreateCertificate(rand.Reader, tpl, parent, &key.PublicKey, signer)
	if err != nil {
		b.err = err
		return
	}

	kd, _ := x509.MarshalECPrivateKey(key)

	b.write("/etc/nginx/ssl-certificates/"+domain+".crt", string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), 0o644)
	b.write("/etc/nginx/ssl-certificates/"+domain+".key", string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd})), 0o600)
}

// site describes one synthetic site; zero values give the common case.
type site struct {
	domain, user, kind   string // kind: php, wordpress, static, nodejs, proxy
	php                  string
	www                  bool   // www alias that redirects to the apex
	extraServer          string // directives appended inside the TLS server
	extraLocation        string // raw location blocks appended
	poolExtra            string
	poolUser             string // overrides pool user
	noPool               bool
	cron                 string
	certIssuer           string // le (default), other, self, none
	certExpired          bool
	rootOverride         string
	upstream             string
	broken               bool
	tier                 string // "", "two" (TLS front + backend block), "cache" (front -> cache -> backend)
	want, wantType, note string
}

func (b *builder) add(s site) {
	if s.user == "" {
		s.user = strings.SplitN(strings.ReplaceAll(s.domain, ".", "-"), "-", 2)[0]
	}

	if s.php == "" {
		s.php = "8.3"
	}

	uid := b.uid
	b.uid++
	b.passwd = append(b.passwd, fmt.Sprintf("%s:x:%d:%d::/home/%s:/bin/bash", s.user, uid, uid, s.user))
	b.mkdir("/home/"+s.user, 0o750)
	b.chown("/home/"+s.user, uid)

	root := "/home/" + s.user + "/htdocs/" + s.domain
	if s.rootOverride != "" {
		root = s.rootOverride
	}

	b.mkdir(root, 0o750)
	b.chown(root, uid)

	switch s.kind {
	case "wordpress":
		b.write(root+"/wp-config.php", "<?php // synthetic\n", 0o640)
		b.chown(root+"/wp-config.php", uid)
	case "nodejs":
		b.write(root+"/package.json", "{}\n", 0o644)
	}

	names := []string{s.domain}
	if s.www {
		names = append(names, "www."+s.domain)
	}

	switch s.certIssuer {
	case "", "le":
		na := b.now.Add(60 * 24 * time.Hour)
		if s.certExpired {
			na = b.now.Add(-5 * 24 * time.Hour)
		}

		b.cert(s.domain, names, b.leCA, na)
	case "other":
		na := b.now.Add(300 * 24 * time.Hour)
		if s.certExpired {
			na = b.now.Add(-20 * 24 * time.Hour)
		}

		b.cert(s.domain, names, b.otherCA, na)
	case "self":
		b.cert(s.domain, names, nil, b.now.Add(365*24*time.Hour))
	}

	var v strings.Builder
	fmt.Fprintf(&v, "# synthetic vhost for %s, written for the scanner tests\n", s.domain)
	fmt.Fprintf(&v, "server {\n  listen 80;\n  listen [::]:80;\n  server_name %s;\n  return 301 https://$host$request_uri;\n}\n\n", strings.Join(names, " "))

	if s.www {
		fmt.Fprintf(&v, "server {\n  listen 443 ssl;\n  http2 on;\n  server_name www.%s;\n  ssl_certificate /etc/nginx/ssl-certificates/%s.crt;\n  ssl_certificate_key /etc/nginx/ssl-certificates/%s.key;\n  return 301 https://%s$request_uri;\n}\n\n", s.domain, s.domain, s.domain, s.domain)
	}

	fmt.Fprintf(&v, "server {\n  listen 443 ssl;\n  listen [::]:443 ssl;\n  http2 on;\n  server_name %s;\n", s.domain)

	if s.certIssuer != "none" {
		fmt.Fprintf(&v, "  ssl_certificate /etc/nginx/ssl-certificates/%s.crt;\n  ssl_certificate_key /etc/nginx/ssl-certificates/%s.key;\n", s.domain, s.domain)
	}

	front := v.String()
	v.Reset()
	fmt.Fprintf(&v, "  root %s;\n  index index.php index.html;\n  access_log /home/%s/logs/nginx/access.log;\n  error_log /home/%s/logs/nginx/error.log;\n  client_max_body_size 64m;\n", root, s.user, s.user)

	if s.broken {
		v.WriteString("  location / {\n    try_files $uri $uri/ /index.php?$args\n  }\n")
	}

	v.WriteString(s.extraServer)

	switch s.kind {
	case "php", "wordpress":
		fmt.Fprintf(&v, "  location / {\n    try_files $uri $uri/ /index.php?$args;\n  }\n  location ~ /\\. {\n    deny all;\n  }\n  location ~ \\.php$ {\n    include fastcgi_params;\n    fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name;\n    fastcgi_pass 127.0.0.1:%d;\n    fastcgi_read_timeout 300;\n  }\n", b.port)
	case "static":
		v.WriteString("  location / {\n    try_files $uri $uri/ =404;\n  }\n")
	case "nodejs", "proxy":
		fmt.Fprintf(&v, "  location / {\n    proxy_pass %s;\n    proxy_http_version 1.1;\n    proxy_set_header Host $host;\n    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n    proxy_set_header X-Forwarded-Proto $scheme;\n  }\n", s.upstream)
	}

	v.WriteString(s.extraLocation)
	app := v.String()

	var out string

	switch s.tier {
	case "":
		out = front + app + "}\n"
	default: // "two" or "cache": TLS front, application in a backend block on 8080
		port := "8080"
		if s.tier == "cache" {
			port = "6081" // a cache listens here and forwards to 8080 [synthetic]
		}

		out = front + "  location / {\n    proxy_pass http://127.0.0.1:" + port + ";\n    proxy_set_header Host $host;\n    proxy_set_header X-Forwarded-Proto https;\n  }\n}\n\n" +
			"server {\n  listen 127.0.0.1:8080;\n  server_name " + s.domain + ";\n" + app + "}\n"
	}

	b.write("/etc/nginx/sites-enabled/"+s.domain+".conf", out, 0o644)

	if s.kind == "php" || s.kind == "wordpress" {
		pu := s.user
		if s.poolUser != "" {
			pu = s.poolUser
		}

		if !s.noPool {
			pool := fmt.Sprintf("; synthetic pool for %s\n[%s]\nuser = %s\ngroup = %s\nlisten = 127.0.0.1:%d\npm = ondemand\npm.max_children = 10\npm.process_idle_timeout = 10s\npm.max_requests = 500\nphp_admin_value[memory_limit] = 256M\nphp_admin_value[upload_max_filesize] = 64M\nphp_admin_value[post_max_size] = 64M\nphp_admin_value[open_basedir] = /home/%s/:/tmp/\n%s",
				s.domain, s.domain, pu, pu, b.port, s.user, s.poolExtra)
			b.write("/etc/php/"+s.php+"/fpm/pool.d/"+s.domain+".conf", pool, 0o644)
		}

		b.port++
	}

	if s.cron != "" {
		p := "/var/spool/cron/crontabs/" + s.user
		b.write(p, s.cron, 0o600)
		b.chown(p, uid)
	}

	b.cases = append(b.cases, Case{Vhost: s.domain + ".conf", Domain: s.domain, WantStatus: s.want, WantType: s.wantType, Note: s.note})
}

const wpCron = "# synthetic\n*/5 * * * * php /home/%[1]s/htdocs/%[2]s/wp-cron.php > /dev/null 2>&1\n"

func (b *builder) sites() {
	// Common, clean cases.
	for i, d := range []string{"alpha-shop.de", "beta-praxis.de", "gamma-verein.de", "delta-blog.de"} {
		u := strings.SplitN(d, "-", 2)[0]
		b.add(site{
			domain: d, user: u, kind: "wordpress", www: true, php: []string{"8.1", "8.2", "8.3", "8.3"}[i],
			cron: fmt.Sprintf(wpCron, u, d), want: "clean", wantType: "wordpress", note: "WordPress, www redirect, LE cert, cron",
		})
	}

	for i, d := range []string{"epsilon-kanzlei.de", "zeta-handwerk.de"} {
		b.add(site{domain: d, kind: "php", www: i%2 == 0, php: []string{"8.2", "8.4"}[i], tier: []string{"", "two"}[i], want: "clean", wantType: "php", note: "plain PHP; zeta as two-tier vhost (pattern seen on web2)"})
	}

	b.add(site{domain: "theta-app.de", kind: "wordpress", php: "7.4", tier: "cache", want: "review", wantType: "wordpress", note: "two-tier with a cache port in between"})
	b.add(site{domain: "iota-landing.de", kind: "static", want: "clean", wantType: "static", note: "static HTML"})
	b.add(site{domain: "kappa-portal.de", kind: "proxy", upstream: "https://10.0.0.5:8080", want: "clean", wantType: "proxy", note: "reverse proxy to another host"})
	b.add(site{domain: "lambda-kunde.de", kind: "php", certIssuer: "other", want: "clean", wantType: "php", note: "purchased certificate, valid"})

	// Adoptable, but a human has to look.
	b.add(site{domain: "my-api.de", user: "myapi", kind: "nodejs", upstream: "http://127.0.0.1:3000", want: "review", wantType: "nodejs", note: "Node.js app, process manager not scanned"})
	b.add(site{
		domain: "nu-magazin.de", kind: "wordpress", www: true,
		extraServer:   "  add_header X-Frame-Options SAMEORIGIN;\n  add_header Referrer-Policy strict-origin-when-cross-origin;\n",
		extraLocation: "  location ~* \\.(?:css|js|png|jpg|svg|woff2)$ {\n    expires 30d;\n    access_log off;\n  }\n",
		want:          "review", wantType: "wordpress", note: "hand-edited: security headers and static cache rule",
	})
	b.add(site{
		domain: "xi-intern.de", kind: "php",
		extraLocation: "  location /admin/ {\n    auth_basic \"Restricted\";\n    auth_basic_user_file /etc/nginx/htpasswd/xi-intern;\n  }\n",
		want:          "review", wantType: "php", note: "basic auth on a path",
	})
	b.add(site{
		domain: "omikron-crm.de", kind: "php", poolExtra: "env[APP_ENV] = production\nenv[DB_PASSWORD] = s3cr3t-synthetic-value\n",
		want: "review", wantType: "php", note: "pool sets environment variables, value must not leak into the report",
	})
	b.add(site{domain: "pi-alt.de", kind: "php", tier: "two", certIssuer: "other", certExpired: true, want: "review", wantType: "php", note: "expired purchased certificate, two-tier"})
	b.add(site{
		domain: "rho-events.de", kind: "php",
		cron: "MAILTO=\"\"\n0 3 * * * /usr/bin/php /home/rho/htdocs/rho-events.de/artisan schedule:run\n30 2 * * * tar czf /home/sigma/backup.tgz /home/rho/htdocs\n",
		want: "review", wantType: "php", note: "cron with environment line and a reference to another home",
	})
	b.write("/etc/nginx/snippets/tau-rewrites.conf", "rewrite ^/alt/(.*)$ /neu/$1 permanent;\n", 0o644)
	b.add(site{
		domain: "tau-reisen.de", kind: "php", extraServer: "  include /etc/nginx/snippets/tau-rewrites.conf;\n",
		want: "review", wantType: "php", note: "include of a snippet with rewrites",
	})
	b.add(site{domain: "ups.ilon-test.de", user: "upsilon", kind: "static", certIssuer: "self", want: "review", wantType: "static", note: "self-signed certificate"})

	// Must not be adopted automatically.
	b.add(site{domain: "phi-shop.de", kind: "php", poolUser: "alpha", want: "blocked", wantType: "php", note: "pool runs as another site's user"})
	b.add(site{domain: "chi-kaputt.de", kind: "php", broken: true, want: "blocked", note: "vhost with syntax error (missing ';')"})
	b.add(site{domain: "psi-legacy.de", kind: "php", noPool: true, want: "blocked", wantType: "php", note: "PHP backend without pool"})
	b.add(site{domain: "omega-alt.de", kind: "static", rootOverride: "/var/www/omega", want: "blocked", note: "document root outside /home"})
}

// extras are files that are not sites but must be handled.
func (b *builder) extras() {
	b.write("/etc/nginx/sites-enabled/00-panel.conf", "server {\n  listen 8443 ssl;\n  server_name _;\n  ssl_certificate /etc/nginx/ssl-certificates/panel.crt;\n  ssl_certificate_key /etc/nginx/ssl-certificates/panel.key;\n  location / {\n    proxy_pass http://127.0.0.1:9000;\n  }\n}\n", 0o644)
	b.write("/etc/nginx/sites-enabled/01-default.conf", "server {\n  listen 80 default_server;\n  server_name _;\n  return 444;\n}\n", 0o644)
	b.write("/etc/nginx/sites-enabled/02-panel-domain.conf", "server {\n  listen 443 ssl;\n  server_name panel.example-host.de;\n  ssl_certificate /etc/nginx/ssl-certificates/panel.crt;\n  ssl_certificate_key /etc/nginx/ssl-certificates/panel.key;\n  location / {\n    proxy_pass https://127.0.0.1:8443/;\n  }\n}\n", 0o644)
	b.write("/etc/php/8.4/fpm/pool.d/default.conf", "[global]\npid = /run/php/x.pid\nerror_log = /var/log/x.log\n\n[default]\nuser = www-data\ngroup = www-data\nlisten = /run/php/php8.4-default.sock\npm = ondemand\npm.max_children = 2\n", 0o644)
	b.write("/etc/php/8.3/fpm/pool.d/www.conf", "[www]\nuser = www-data\ngroup = www-data\nlisten = /run/php/php8.3-fpm.sock\npm = dynamic\npm.max_children = 5\n", 0o644)
	b.write("/etc/php/8.2/fpm/pool.d/old-site.conf", "[old-site]\nuser = sigma\ngroup = sigma\nlisten = 127.0.0.1:16999\npm = ondemand\npm.max_children = 4\n", 0o644)
	b.passwd = append(b.passwd, "sigma:x:1900:1900::/home/sigma:/bin/bash")
	b.mkdir("/home/sigma", 0o750)
}
