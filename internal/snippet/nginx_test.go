// SPDX-License-Identifier: AGPL-3.0-or-later

package snippet

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestRenderedSnippetsPassNginx renders accepted snippets into a minimal
// server block and runs nginx -t on it. Skips without nginx on PATH.
func TestRenderedSnippetsPassNginx(t *testing.T) {
	bin, err := exec.LookPath("nginx")
	if err != nil {
		t.Skip("nginx not on PATH")
	}

	if os.Geteuid() != 0 {
		t.Skip("nginx -t needs root for its default paths")
	}

	src := `add_header X-Frame-Options "SAMEORIGIN" always;
add_header Content-Security-Policy "default-src 'self'" always;
if ($host = www.example.com) { return 301 https://example.com$request_uri; }
rewrite ^/old/(.*)$ /new/$1 permanent;
location ~* \.(css|js|png)$ { expires 30d; access_log off; log_not_found off; }
location /admin/ { auth_basic "Restricted"; auth_basic_user_file /home/shop/.htpasswd; }
location = /xmlrpc.php { deny all; return 403; }
location /media/ { alias /home/shop/media/; }
location /app/ { proxy_pass http://127.0.0.1:3000; proxy_http_version 1.1; proxy_set_header Host $host; }
location /upload { limit_except GET POST { deny all; } }
location ~ "^/a\"b{2}$" { return 404; }
gzip on;
gzip_types text/plain application/json;
`

	ds, err := Check(src, SlotServer, site)
	if err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	snip := filepath.Join(dir, "snippet.conf")

	if err := os.WriteFile(snip, []byte(Render(ds, "")), 0o600); err != nil {
		t.Fatal(err)
	}

	conf := `pid ` + filepath.Join(dir, "nginx.pid") + `;
error_log ` + filepath.Join(dir, "error.log") + `;
events {}
http {
    access_log off;
    server {
        listen 127.0.0.1:18089;
        server_name example.com;
        location / { try_files $uri =404; }
        include ` + snip + `;
    }
}
`

	confPath := filepath.Join(dir, "nginx.conf")
	if err := os.WriteFile(confPath, []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}

	out, err := exec.CommandContext(t.Context(), bin, "-t", "-q", "-c", confPath).CombinedOutput()
	if err != nil {
		t.Fatalf("nginx -t: %v\n%s\nrendered:\n%s", err, out, Render(ds, ""))
	}

	if strings.Contains(string(out), "emerg") {
		t.Fatalf("nginx -t: %s", out)
	}
}
