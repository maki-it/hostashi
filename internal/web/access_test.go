// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/fcgi"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"github.com/maki-it/hostashi/internal/agentapi"
)

func accessUsers(t *testing.T) []agentapi.WebAuthUser {
	t.Helper()

	h, err := bcrypt.GenerateFromPassword([]byte("correct horse battery"), agentapi.AccessBcryptCost)
	if err != nil {
		t.Fatal(err)
	}

	return []agentapi.WebAuthUser{{Name: "kim", Hash: string(h)}}
}

func basic(user, pw string) map[string]string {
	return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pw))}
}

func TestAccessRender(t *testing.T) {
	users := accessUsers(t)
	n := &Nginx{Ctl: &testCtl{}, Listen: []string{"80"}, ListenTLS: []string{"443"}, ControlSocket: "/run/x.sock", PIDFile: "/run/x.pid", ErrorLog: "/var/log/x", MimeTypes: "/etc/x", ACMERoot: "/var/lib/acme", AuthDir: "/var/lib/hostashi-web-auth"}

	sites := []Site{
		{User: "tls", Domains: []string{"tls.example.com"}, Root: "/srv/tls", Cert: "/c/t.pem", Key: "/c/t.key", Access: agentapi.WebAccess{IPMode: agentapi.AccessIPAllow, IPs: []string{"203.0.113.7", "2001:db8::/48"}}},
		{User: "wp", Domains: []string{"wp.example.com"}, Root: "/srv/wp", PHPSocket: "/run/php/wp.sock", Access: agentapi.WebAccess{IPMode: agentapi.AccessIPDeny, IPs: []string{"198.51.100.0/24"}, Auth: &agentapi.WebAuth{Paths: []string{"/wp-admin/", "/wp-login.php"}, Bypass: []string{"203.0.113.0/24"}, Users: users}}},
		{User: "px", Domains: []string{"px.example.com"}, Proxy: &agentapi.WebProxy{Target: "http://backend.example.net"}, ProxyAddrs: []string{"192.0.2.10:80"}, Access: agentapi.WebAccess{Auth: &agentapi.WebAuth{Users: users}}},
		{User: "plain", Domains: []string{"plain.example.com"}, Root: "/srv/plain"},
	}

	f, err := n.Render(Desired{Sites: sites}, 1)
	if err != nil {
		t.Fatal(err)
	}

	tls := string(f.Content["sites/tls.conf"])
	// The HTTP block that only redirects and the HTTPS block both apply it.
	if got := strings.Count(tls, "allow 203.0.113.7/32;"); got != 2 {
		t.Errorf("tls.conf has the rule in %d server blocks:\n%s", got, tls)
	}

	for _, want := range []string{"allow 2001:db8::/48;", "deny all;", "allow all;\n        auth_basic off;\n        root /var/lib/acme;"} {
		if !strings.Contains(tls, want) {
			t.Errorf("tls.conf lacks %q:\n%s", want, tls)
		}
	}

	if strings.Contains(tls, "auth_basic $") {
		t.Errorf("tls.conf asks for a password:\n%s", tls)
	}

	wp := string(f.Content["sites/wp.conf"])
	file := "/var/lib/hostashi-web-auth/" + AuthFileName(sites[1])

	for _, want := range []string{
		"geo $hostashi_noauth_7770 {\n    default 0;\n    203.0.113.0/24 1;\n}",
		"map \"$hostashi_noauth_7770$uri\" $hostashi_realm_7770 {\n    default off;\n    \"~^1\" off;\n    \"~*^0/wp-admin(/|$)\" \"Restricted\";\n    \"~*^0/wp-login[.]php(/|$)\" \"Restricted\";\n}",
		"map $hostashi_realm_7770 $hostashi_authz_7770 {\n    default \"\";\n    off $http_authorization;\n}",
		"deny 198.51.100.0/24;\n    allow all;\n    auth_basic $hostashi_realm_7770;\n    auth_basic_user_file " + file + ";",
		"fastcgi_param HTTP_AUTHORIZATION $hostashi_authz_7770 if_not_empty;",
		"location = /.hostashi/health {\n        allow all;\n        auth_basic off;",
	} {
		if !strings.Contains(wp, want) {
			t.Errorf("wp.conf lacks %q:\n%s", want, wp)
		}
	}

	px := string(f.Content["sites/px.conf"])
	for _, want := range []string{"map $uri $hostashi_realm_7078 {\n    default \"Restricted\";\n}", "proxy_set_header Authorization $hostashi_authz_7078;"} {
		if !strings.Contains(px, want) {
			t.Errorf("px.conf lacks %q:\n%s", want, px)
		}
	}

	if strings.Contains(px, "geo ") || strings.Contains(px, "    allow all;\n    auth_basic") {
		t.Errorf("px.conf:\n%s", px)
	}

	if p := string(f.Content["sites/plain.conf"]); strings.Contains(p, "auth_basic") || strings.Contains(p, "allow ") {
		t.Errorf("plain.conf:\n%s", p)
	}

	// Without a directory for password files nothing is rendered.
	n.AuthDir = ""
	if _, err := n.Render(Desired{Sites: sites[1:2]}, 2); err == nil {
		t.Error("rendered a password without a password file directory")
	}

	n.AuthDir = "/var/lib/hostashi-web-auth"

	// A snippet with the same directives is refused.
	bad := sites[1]
	bad.Home = "/srv"
	bad.Snippets = Snippets{Server: "location /x {\n    auth_basic off;\n}\n"}

	if err := (Desired{Sites: []Site{bad}}).Validate(); err == nil || !strings.Contains(err.Error(), "access conflict") {
		t.Errorf("snippet with auth_basic: %v", err)
	}
}

func TestAuthFileContentAndName(t *testing.T) {
	users := accessUsers(t)
	s := Site{User: "wp", Access: agentapi.WebAccess{Auth: &agentapi.WebAuth{Users: users}}}

	if got := string(AuthFileContent(s)); !strings.HasSuffix(got, "\nkim:"+users[0].Hash+"\n") {
		t.Errorf("content %q", got)
	}

	a := AuthFileName(s)
	s.Access.Auth.Users = append(s.Access.Auth.Users, agentapi.WebAuthUser{Name: "two", Hash: users[0].Hash})

	if b := AuthFileName(s); a == b || !strings.HasPrefix(b, "wp-") || !strings.HasSuffix(b, ".htpasswd") {
		t.Errorf("names %q %q", a, b)
	}
}

func TestAuthFilesWriteAndPrune(t *testing.T) {
	users := accessUsers(t)
	dir := filepath.Join(t.TempDir(), "web-auth")
	a := AuthFiles{Dir: dir, GID: os.Getgid()}

	one := Site{User: "one", Access: agentapi.WebAccess{Auth: &agentapi.WebAuth{Users: users}}}
	two := Site{User: "two", Access: agentapi.WebAccess{Auth: &agentapi.WebAuth{Users: users}}}
	none := Site{User: "none"}

	if err := a.Write([]Site{one, two, none}); err != nil {
		t.Fatal(err)
	}

	fi, err := os.Stat(filepath.Join(dir, AuthFileName(one)))
	if err != nil {
		t.Fatal(err)
	}

	if fi.Mode().Perm() != 0o640 {
		t.Errorf("file mode %v", fi.Mode())
	}

	if di, _ := os.Stat(dir); di.Mode().Perm() != 0o750 {
		t.Errorf("dir mode %v", di.Mode())
	}

	if err := os.WriteFile(filepath.Join(dir, "stray"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := a.Prune([]Site{one, none}); err != nil {
		t.Fatal(err)
	}

	es, _ := os.ReadDir(dir)
	if len(es) != 1 || es[0].Name() != AuthFileName(one) {
		t.Errorf("left %v", es)
	}

	if err := (AuthFiles{Dir: filepath.Join(dir, "missing")}).Prune(nil); err != nil {
		t.Errorf("prune of a missing directory: %v", err)
	}
}

func TestAccessServed(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	ln, err := net.Listen("tcp", outsideAddr(t)+":0")
	if err != nil {
		t.Skip(err)
	}

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "authz=%q", r.Header.Get("Authorization"))
	})}

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { _ = srv.Close() })

	authDir := filepath.Join(e.dir, "web-auth")
	e.r.Driver.AuthDir = authDir
	files := AuthFiles{Dir: authDir, GID: os.Getgid()}

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	users := accessUsers(t)

	wp := e.site(t, "wp", "wp index", "wp.example.com")
	if err := os.MkdirAll(filepath.Join(wp.Root, "wp-admin"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(wp.Root, "wp-admin", "index.html"), []byte("admin"), 0o644); err != nil {
		t.Fatal(err)
	}

	wp.Access = agentapi.WebAccess{Auth: &agentapi.WebAuth{Paths: []string{"/wp-admin"}, Users: users}}

	blocked := e.site(t, "blocked", "blocked index", "blocked.example.com")
	blocked.Access = agentapi.WebAccess{IPMode: agentapi.AccessIPDeny, IPs: []string{"127.0.0.0/8"}}

	only := e.site(t, "only", "only index", "only.example.com")
	only.Access = agentapi.WebAccess{IPMode: agentapi.AccessIPAllow, IPs: []string{"127.0.0.1"}}

	px := Site{User: "px", Domains: []string{"px.example.com"}, Proxy: &agentapi.WebProxy{Target: "http://backend.example.net"}, ProxyAddrs: []string{ln.Addr().String()}, Access: agentapi.WebAccess{Auth: &agentapi.WebAuth{Paths: []string{"/private"}, Users: users}}}

	d := Desired{Sites: []Site{wp, blocked, only, px}}
	if err := files.Write(d.Sites); err != nil {
		t.Fatal(err)
	}

	// Apply also checks every site's health location, which must stay
	// open whatever the site's rule says.
	if _, err := e.r.Apply(ctx, d); err != nil {
		t.Fatal(err)
	}

	good := basic("kim", "correct horse battery")

	for _, c := range []struct {
		host, path string
		header     map[string]string
		code       int
		body       string
	}{
		{"wp.example.com", "/", nil, http.StatusOK, "wp index"},
		{"wp.example.com", "/wp-admin/", nil, http.StatusUnauthorized, ""},
		{"wp.example.com", "/WP-ADMIN/", nil, http.StatusUnauthorized, ""},
		{"wp.example.com", "/wp-admin", nil, http.StatusUnauthorized, ""},
		{"wp.example.com", "/x/../wp-admin/", nil, http.StatusUnauthorized, ""},
		{"wp.example.com", "/wp-admin/", basic("kim", "wrong password!"), http.StatusUnauthorized, ""},
		{"wp.example.com", "/wp-admin/", basic("nobody", "correct horse battery"), http.StatusUnauthorized, ""},
		{"wp.example.com", "/wp-admin/", good, http.StatusOK, "admin"},
		{"wp.example.com", "/wp-administrator", nil, http.StatusNotFound, ""},
		{"blocked.example.com", "/", nil, http.StatusForbidden, ""},
		{"only.example.com", "/", nil, http.StatusOK, "only index"},
		{"px.example.com", "/open", map[string]string{"Authorization": "Bearer app-token"}, http.StatusOK, `authz="Bearer app-token"`},
		{"px.example.com", "/private/x", good, http.StatusOK, `authz=""`},
		{"px.example.com", "/private/x", nil, http.StatusUnauthorized, ""},
	} {
		code, _, body := e.request(t, http.MethodGet, c.host, c.path, c.header)
		if code != c.code || (c.body != "" && body != c.body) {
			t.Errorf("%s%s: %d %q, want %d %q", c.host, c.path, code, body, c.code, c.body)
		}
	}

	if err := files.Prune(d.Sites); err != nil {
		t.Fatal(err)
	}

	if es, _ := os.ReadDir(authDir); len(es) != 2 {
		t.Errorf("password files %v", es)
	}
}

// PHP gets no Authorization on a protected path, and its own one elsewhere.
// A Go FastCGI server stands in for PHP-FPM.
func TestAccessServedFastCGI(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()

	sock := filepath.Join(e.dir, "php.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}

	go func() {
		_ = fcgi.Serve(ln, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = fmt.Fprintf(w, "authz=%q", r.Header.Get("Authorization"))
		}))
	}()

	t.Cleanup(func() { _ = ln.Close() })

	if err := os.Chmod(sock, 0o666); err != nil {
		t.Fatal(err)
	}

	authDir := filepath.Join(e.dir, "web-auth")
	e.r.Driver.AuthDir = authDir

	if _, err := e.r.Recover(ctx); err != nil {
		t.Fatal(err)
	}

	php := e.site(t, "php", "php index", "php.example.com")
	php.PHPSocket = sock

	for _, f := range []string{"index.php", "admin.php"} {
		if err := os.WriteFile(filepath.Join(php.Root, f), []byte("<?php"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	php.Access = agentapi.WebAccess{Auth: &agentapi.WebAuth{Paths: []string{"/admin.php"}, Users: accessUsers(t)}}

	d := Desired{Sites: []Site{php}}
	if err := (AuthFiles{Dir: authDir, GID: os.Getgid()}).Write(d.Sites); err != nil {
		t.Fatal(err)
	}

	if _, err := e.r.Apply(ctx, d); err != nil {
		t.Fatal(err)
	}

	if _, _, body := e.request(t, http.MethodGet, "php.example.com", "/index.php", map[string]string{"Authorization": "Bearer app-token"}); body != `authz="Bearer app-token"` {
		t.Errorf("open path: %q", body)
	}

	if code, _, body := e.request(t, http.MethodGet, "php.example.com", "/admin.php", basic("kim", "correct horse battery")); code != http.StatusOK || body != `authz=""` {
		t.Errorf("protected path: %d %q", code, body)
	}
}
