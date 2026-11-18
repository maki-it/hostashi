#!/bin/bash
# Runs inside a fresh container: install the distribution nginx and its brotli
# modules, serve a text file over TLS with HTTP/2 and HTTP/3, and fetch it with
# different Accept-Encoding values.
set -u
export DEBIAN_FRONTEND=noninteractive
. /etc/os-release; echo "== $PRETTY_NAME $(uname -m)"
apt-get update -qq >/dev/null 2>&1
apt-get install -y -qq nginx libnginx-mod-http-brotli-filter libnginx-mod-http-brotli-static openssl brotli >/dev/null 2>/tmp/apt.err || { echo "APT FAILED"; tail -5 /tmp/apt.err; exit 1; }
echo "nginx: $(nginx -v 2>&1)"
echo "built with http_v3: $(nginx -V 2>&1 | grep -c with-http_v3_module)"
echo "modules-enabled: $(ls /etc/nginx/modules-enabled | tr '\n' ' ')"
rm -f /etc/nginx/sites-enabled/default
mkdir -p /srv/site /etc/nginx/tls
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj /CN=site.test -keyout /etc/nginx/tls/k.pem -out /etc/nginx/tls/c.pem 2>/dev/null
for i in $(seq 1 3000); do echo "<p class=\"entry\">Hostashi test line $i, repeated text compresses well.</p>"; done > /srv/site/index.html
cp /srv/site/index.html /srv/site/static.html
brotli -q 11 -k /srv/site/static.html
head -c 200000 /dev/urandom > /srv/site/random.bin
cat > /etc/nginx/conf.d/site.conf <<'CONF'
server {
    listen 8443 ssl;
    listen 8443 quic reuseport;
    http2 on;
    http3 on;
    server_name site.test;
    ssl_certificate /etc/nginx/tls/c.pem;
    ssl_certificate_key /etc/nginx/tls/k.pem;
    add_header Alt-Svc 'h3=":8443"; ma=86400' always;
    root /srv/site;
    brotli on;
    brotli_comp_level 5;
    brotli_types text/css application/javascript application/json image/svg+xml;
    brotli_static on;
    gzip on;
    gzip_vary on;
    gzip_comp_level 5;
    gzip_types text/css application/javascript application/json image/svg+xml;
}
CONF
nginx -t 2>&1 | tail -2
nginx
sleep 1
sizes() { echo "index.html: $(stat -c %s /srv/site/index.html) bytes, static.html.br: $(stat -c %s /srv/site/static.html.br) bytes, gzip -5 of index: $(gzip -5 -c /srv/site/index.html | wc -c) bytes"; }
sizes
t() { printf '%-34s ' "$1"; shift; /client "$@"; }
t "h1  no encoding"            -proto h1 -path /index.html
t "h1  gzip"                   -proto h1 -path /index.html -enc gzip
t "h2  br,gzip"                -proto h2 -path /index.html -enc "br, gzip"
t "h2  gzip only"              -proto h2 -path /index.html -enc gzip
t "h3  br,gzip"                -proto h3 -path /index.html -enc "br, gzip"
t "h3  gzip only"              -proto h3 -path /index.html -enc gzip
t "h3  no encoding"            -proto h3 -path /index.html
t "h3  br, static file (.br)"  -proto h3 -path /static.html -enc br
t "h3  br, random.bin"         -proto h3 -path /random.bin -enc br
echo "-- error log:"; grep -c -E "emerg|crit|alert" /var/log/nginx/error.log
