// Command gen renders the same typed site list as an nginx config and as a
// Caddyfile, so that S3 compares like with like (and the size of the two
// templates). Throwaway spike code.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"text/template"
)

type Site struct {
	Name   string // site01
	Domain string // site01.test
	Kind   string // php | static | proxy
	Root   string
}

type Env struct {
	Sites     []Site
	RunDir    string
	HTTPPort  int
	HTTPSPort int
	Cert      string
	Key       string
	PHPSock   string
	ProxyPort int
	Cache     bool // nginx only: fastcgi_cache for php sites
	User      string
}

const nginxTmpl = `daemon on;
user {{.User}};
worker_processes auto;
pid {{.RunDir}}/nginx.pid;
error_log {{.RunDir}}/nginx-error.log warn;
events { worker_connections 1024; }
http {
    include {{.RunDir}}/mime.types;
    access_log off;
    sendfile on;
    gzip on;
    gzip_types text/plain text/css application/json application/javascript;
    client_body_temp_path {{.RunDir}}/tmp/body;
    proxy_temp_path {{.RunDir}}/tmp/proxy;
    fastcgi_temp_path {{.RunDir}}/tmp/fastcgi;
    uwsgi_temp_path {{.RunDir}}/tmp/uwsgi;
    scgi_temp_path {{.RunDir}}/tmp/scgi;
{{- if .Cache}}
    fastcgi_cache_path {{.RunDir}}/fcgi-cache levels=1:2 keys_zone=sites:10m max_size=100m inactive=10m;
{{- end}}
    map $http_upgrade $connection_upgrade { default upgrade; '' close; }
    ssl_certificate {{.Cert}};
    ssl_certificate_key {{.Key}};
    ssl_protocols TLSv1.2 TLSv1.3;
{{range .Sites}}
    server {
        listen 127.0.0.1:{{$.HTTPPort}};
        server_name {{.Domain}} www.{{.Domain}};
        return 301 https://{{.Domain}}:{{$.HTTPSPort}}$request_uri;
    }
    server {
        listen 127.0.0.1:{{$.HTTPSPort}} ssl;
        listen 127.0.0.1:{{$.HTTPSPort}} quic;
        http2 on;
        add_header Alt-Svc 'h3=":{{$.HTTPSPort}}"; ma=86400' always;
        server_name www.{{.Domain}};
        return 301 https://{{.Domain}}:{{$.HTTPSPort}}$request_uri;
    }
    server {
        listen 127.0.0.1:{{$.HTTPSPort}} ssl;
        listen 127.0.0.1:{{$.HTTPSPort}} quic;
        http2 on;
        add_header Alt-Svc 'h3=":{{$.HTTPSPort}}"; ma=86400' always;
        server_name {{.Domain}};
        root {{.Root}};
        index index.php index.html;
        location ~ /\.(?!well-known) { deny all; }
        location = /wp-config.php { deny all; }
{{- if eq .Kind "php"}}
        location / { try_files $uri $uri/ /index.php?$args; }
        location ~ \.php$ {
            include {{$.RunDir}}/fastcgi.conf;
            fastcgi_pass unix:{{$.PHPSock}};
{{- if $.Cache}}
            fastcgi_cache sites;
            fastcgi_cache_key $scheme$host$request_uri;
            fastcgi_cache_valid 200 1m;
            add_header X-Cache $upstream_cache_status;
{{- end}}
        }
{{- else if eq .Kind "static"}}
        location / { try_files $uri $uri/ =404; }
        location ~* \.(css|js|png|jpg|svg|woff2)$ { expires 30d; add_header Cache-Control "public, immutable"; }
{{- else}}
        location / {
            proxy_pass http://127.0.0.1:{{$.ProxyPort}};
            proxy_http_version 1.1;
            proxy_set_header Host $host;
            proxy_set_header Upgrade $http_upgrade;
            proxy_set_header Connection $connection_upgrade;
        }
{{- end}}
    }
{{end}}
}
`

const caddyTmpl = `{
    admin 127.0.0.1:{{.AdminPort}}
    auto_https off
    default_bind 127.0.0.1
    http_port {{.HTTPPort}}
    https_port {{.HTTPSPort}}
    servers {
        protocols h1 h2 h3
    }
    log {
        output file {{.RunDir}}/caddy-error.log
        level WARN
    }
}
(site_common) {
    tls {{.Cert}} {{.Key}}
    encode gzip zstd
    @dot {
        path_regexp dot ^/\.
        not path /.well-known/*
    }
    respond @dot 403
    @wpconfig path /wp-config.php
    respond @wpconfig 403
}
{{range .Sites}}
http://{{.Domain}}:{{$.HTTPPort}}, http://www.{{.Domain}}:{{$.HTTPPort}} {
    redir https://{{.Domain}}:{{$.HTTPSPort}}{uri} permanent
}
https://www.{{.Domain}}:{{$.HTTPSPort}} {
    import site_common
    redir https://{{.Domain}}:{{$.HTTPSPort}}{uri} permanent
}
https://{{.Domain}}:{{$.HTTPSPort}} {
    import site_common
    root * {{.Root}}
{{- if eq .Kind "php"}}
    php_fastcgi unix/{{$.PHPSock}}
    file_server
{{- else if eq .Kind "static"}}
    @assets path_regexp assets (?i)\.(css|js|png|jpg|svg|woff2)$
    header @assets Cache-Control "public, max-age=2592000, immutable"
    file_server
{{- else}}
    reverse_proxy 127.0.0.1:{{$.ProxyPort}}
{{- end}}
}
{{end}}
`

type full struct {
	Env
	AdminPort int
}

func main() {
	n := flag.Int("sites", 22, "number of sites")
	out := flag.String("out", ".", "output dir")
	cache := flag.Bool("cache", false, "fastcgi cache (nginx)")
	root := flag.String("root", "/tmp/s3/work/www", "docroot parent")
	run := flag.String("run", "/tmp/s3/work/run", "run dir")
	flag.Parse()
	e := full{Env: Env{
		RunDir: *run, HTTPPort: 18080, HTTPSPort: 18443,
		Cert: *run + "/cert.pem", Key: *run + "/key.pem", PHPSock: *run + "/php.sock",
		ProxyPort: 19100, Cache: *cache, User: "root",
	}, AdminPort: 12019}
	for i := 1; i <= *n; i++ {
		kind := "php"
		switch i % 4 {
		case 0:
			kind = "static"
		case 3:
			kind = "proxy"
		}
		name := fmt.Sprintf("site%02d", i)
		e.Sites = append(e.Sites, Site{Name: name, Domain: name + ".test", Kind: kind, Root: filepath.Join(*root, name)})
	}
	for name, t := range map[string]string{"nginx.conf": nginxTmpl, "Caddyfile": caddyTmpl} {
		f, err := os.Create(filepath.Join(*out, name))
		if err != nil {
			panic(err)
		}
		if err := template.Must(template.New(name).Parse(t)).Execute(f, e); err != nil {
			panic(err)
		}
		f.Close()
	}
}
