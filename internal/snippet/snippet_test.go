// SPDX-License-Identifier: AGPL-3.0-or-later

package snippet

import (
	"errors"
	"strings"
	"testing"

	"github.com/maki-it/hostashi/internal/nginxconf"
)

var site = Site{Home: "/home/shop", PHPSocket: "/run/hostashi-php/shop.sock"}

func TestAcceptsTypicalSnippets(t *testing.T) {
	cases := map[string]string{
		"headers": `add_header X-Frame-Options "SAMEORIGIN" always;
add_header Referrer-Policy strict-origin-when-cross-origin;`,
		"redirect if": `if ($host = www.example.com) { return 301 https://example.com$request_uri; }`,
		"rewrite":     `rewrite ^/old/(.*)$ /new/$1 permanent;`,
		"static cache": `location ~* \.(css|js|png|jpg|woff2)$ {
    expires 30d;
    access_log off;
    log_not_found off;
}`,
		"basic auth": `location /admin/ {
    auth_basic "Restricted";
    auth_basic_user_file /home/shop/.htpasswd;
}`,
		"deny xmlrpc":    `location = /xmlrpc.php { deny all; return 403; }`,
		"error page":     `error_page 404 /404.html;`,
		"alias in home":  `location /media/ { alias /home/shop/media/; }`,
		"proxy remote":   `location /api/ { proxy_pass https://api.example.com/v1/; proxy_set_header Host api.example.com; }`,
		"proxy local":    `location /app/ { proxy_pass http://127.0.0.1:3000; proxy_http_version 1.1; }`,
		"proxy v6 local": `location /app/ { proxy_pass http://[::1]:3000; }`,
		"own php":        `location ~ ^/legacy/.+\.php$ { fastcgi_pass unix:/run/hostashi-php/shop.sock; fastcgi_param SCRIPT_FILENAME $document_root$fastcgi_script_name; }`,
		"gzip":           `gzip on; gzip_types text/plain application/json; brotli on;`,
		"limit except":   `location /upload { limit_except GET POST { deny all; } }`,
		"exact root":     `location = / { return 302 /start; }`,
		"empty":          ``,
		"comment only":   "# nothing\n",
	}

	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Check(src, SlotServer, site); err != nil {
				t.Fatalf("Check: %v", err)
			}
		})
	}
}

func TestRejectsDangerousSnippets(t *testing.T) {
	cases := map[string]struct{ src, want string }{
		"access log path":   {`access_log /etc/cron.d/x;`, `only "access_log off"`},
		"error log":         {`error_log /etc/passwd;`, `"error_log" is not allowed`},
		"listen":            {`listen 8443;`, `"listen" is not allowed`},
		"server_name":       {`server_name other.example;`, `"server_name" is not allowed`},
		"include":           {`include /etc/shadow;`, `"include" is not allowed`},
		"ssl key":           {`ssl_certificate_key /etc/hostashi/secret.key;`, `"ssl_certificate_key" is not allowed`},
		"temp path":         {`client_body_temp_path /etc;`, `"client_body_temp_path" is not allowed`},
		"other site php":    {`location ~ \.php$ { fastcgi_pass unix:/run/hostashi-php/other.sock; }`, "only the site's own PHP socket"},
		"panel socket":      {`location /x { proxy_pass http://unix:/run/hostashi-panel.sock:; }`, "unix sockets are not allowed"},
		"proxy variable":    {`location /x { proxy_pass http://$arg_h; }`, "variables are not allowed"},
		"proxy loopback":    {`location /x { proxy_pass http://127.0.0.1; }`, "explicit port"},
		"proxy localhost":   {`location /x { proxy_pass http://localhost/; }`, "explicit port"},
		"proxy any":         {`location /x { proxy_pass http://0.0.0.0; }`, "explicit port"},
		"proxy upstream":    {`location /x { proxy_pass http://backend; }`, "full domain name"},
		"proxy scheme":      {`location /x { proxy_pass grpc://a.example; }`, "http:// or https://"},
		"proxy bad port":    {`location /x { proxy_pass http://a.example:99999; }`, "invalid port"},
		"root other home":   {`root /home/other/htdocs;`, "inside the site's home"},
		"root prefix trick": {`root /home/shopper;`, "inside the site's home"},
		"root dotdot":       {`root /home/shop/../other;`, "plain absolute path"},
		"root variable":     {`root /home/shop/$host;`, "plain absolute path"},
		"alias etc":         {`location /x/ { alias /etc/; }`, "inside the site's home"},
		"auth file outside": {`auth_basic_user_file /etc/shadow;`, "inside the site's home"},
		"location root":     {`location / { return 200; }`, `"location /" belongs to Hostashi`},
		"location ^~ root":  {`location ^~ / { return 200; }`, `"location /" belongs to Hostashi`},
		"acme":              {`location ^~ /.well-known/acme-challenge/ { root /home/shop; }`, "belongs to Hostashi"},
		"health":            {`location = /.hostashi/health { return 200; }`, "belongs to Hostashi"},
		"nested bad":        {`location /a { if ($x) { access_log /tmp/x; } }`, `only "access_log off"`},
		"perl":              {`perl_set $x 'sub { 1 }';`, `"perl_set" is not allowed`},
		"js":                {`js_content main.f;`, `"js_content" is not allowed`},
		"lua":               {`content_by_lua 'os.execute("id")';`, `"content_by_lua" is not allowed`},
		"uwsgi":             {`uwsgi_pass unix:/x;`, `"uwsgi_pass" is not allowed`},
		"map":               {`map $a $b { default 1; }`, `"map" is not allowed`},
		"control char":      {"add_header X \"a\tb\";", "control characters"},
		"break out":         {"}\nserver { listen 80; }", "unexpected '}'"},
		"unterminated":      {`location /a { return 200;`, "missing '}'"},
		"block on simple":   {`return { }`, "takes no block"},
		"location no block": {`location /a;`, "needs a block"},
		"bad modifier":      {`location @@ /a { }`, "unknown modifier"},
		"bad method":        {`location /a { limit_except get { deny all; } }`, "invalid method"},
		"autoindex value":   {`autoindex yes;`, "on or off"},
		"too deep":          {`location /a { location /a/b { location /a/b/c { location /a/b/c/d { location /a/b/c/d/e { return 200; } } } } }`, "nested deeper"},
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Check(c.src, SlotServer, site)

			var se *Error
			if !errors.As(err, &se) {
				t.Fatalf("Check(%q) = %v, want *Error", c.src, err)
			}

			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Check(%q) = %v, want %q", c.src, err, c.want)
			}
		})
	}
}

func TestFastCGIWithoutPHP(t *testing.T) {
	_, err := Check(`fastcgi_pass unix:/run/x.sock;`, SlotServer, Site{Home: "/home/static"})
	if err == nil || !strings.Contains(err.Error(), "the site has no PHP") {
		t.Fatalf("err = %v", err)
	}
}

func TestReportsEveryProblemWithLine(t *testing.T) {
	src := "add_header X 1;\nlisten 80;\nreturn 200;\naccess_log /x;\n"

	_, err := Check(src, SlotRoot, site)

	var se *Error
	if !errors.As(err, &se) {
		t.Fatalf("err = %v", err)
	}

	if len(se.Problems) != 2 || se.Problems[0].Line != 2 || se.Problems[1].Line != 4 {
		t.Fatalf("problems = %+v", se.Problems)
	}
}

func TestLimits(t *testing.T) {
	big := strings.Repeat("#", MaxBytes+1)
	if _, err := Check(big, SlotServer, site); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("big: %v", err)
	}

	many := strings.Repeat("add_header X 1;\n", MaxDirectives+1)
	if _, err := Check(many, SlotServer, site); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("many: %v", err)
	}
}

func TestInvalidSite(t *testing.T) {
	for name, s := range map[string]Site{
		"no home":      {},
		"root home":    {Home: "/"},
		"dirty home":   {Home: "/home/a/../b"},
		"quoted home":  {Home: "/home/a;b"},
		"dirty socket": {Home: "/home/a", PHPSocket: "/run/x sock"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Check("", SlotServer, s); !errors.Is(err, ErrInvalidSite) {
				t.Fatalf("err = %v", err)
			}
		})
	}

	if _, err := Check("", Slot("http"), site); !errors.Is(err, ErrInvalidSite) {
		t.Fatalf("slot: %v", err)
	}
}

func TestRenderRoundTrip(t *testing.T) {
	src := `# comment is dropped
add_header Content-Security-Policy "default-src 'self'; img-src * data:" always;
location ~* \.(css|js)$ { expires 7d; }
rewrite "^/a\"b{2}$" /c last;
if ($request_uri ~ "^/x y") { return 404; }
`

	ds, err := Check(src, SlotServer, site)
	if err != nil {
		t.Fatal(err)
	}

	out := Render(ds, "    ")
	if strings.Contains(out, "comment") {
		t.Fatalf("comment kept:\n%s", out)
	}

	back, err := nginxconf.Parse(out)
	if err != nil {
		t.Fatalf("rendered text does not parse: %v\n%s", err, out)
	}

	if !sameTree(ds, back) {
		t.Fatalf("round trip changed the snippet:\n%s", out)
	}

	// nginx turns \\ into \ in quoted and bare words alike, and keeps
	// unknown escapes such as \. [V: nginx 1.24, 2026-11-19].
	want := "    location ~* \"\\\\.(css|js)$\" {\n        expires 7d;\n    }\n"
	if !strings.Contains(out, want) {
		t.Fatalf("rendered:\n%s\nwant to contain:\n%s", out, want)
	}
}

func sameTree(a, b []nginxconf.Directive) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i].Name != b[i].Name || a[i].IsBlock() != b[i].IsBlock() || strings.Join(a[i].Args, "\x00") != strings.Join(b[i].Args, "\x00") {
			return false
		}

		if !sameTree(a[i].Block, b[i].Block) {
			return false
		}
	}

	return true
}

func FuzzCheckRender(f *testing.F) {
	for _, s := range []string{
		`add_header X "a b";`,
		`location ~ \.php$ { return 403; }`,
		`if ($a) { set $b "c\"d"; }`,
		`proxy_pass http://127.0.0.1:3000/x;`,
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, src string) {
		ds, err := Check(src, SlotServer, site)
		if err != nil {
			return
		}

		out := Render(ds, "")

		back, err := Check(out, SlotServer, site)
		if err != nil {
			t.Fatalf("rendered snippet fails the check: %v\nsource %q\nrendered %q", err, src, out)
		}

		if !sameTree(ds, back) {
			t.Fatalf("round trip changed the snippet\nsource %q\nrendered %q", src, out)
		}
	})
}
