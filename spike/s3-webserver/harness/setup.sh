#!/bin/sh
# Prepares /tmp/s3/work: certificate, docroots, php-fpm pool, mime types.
# Everything stays under /tmp/s3/work, nothing in /etc is touched.
set -eu
W=${W:-/tmp/s3/work}
R=$W/run
mkdir -p "$R/tmp" "$W/www"
cp /etc/nginx/mime.types /etc/nginx/fastcgi.conf "$R/"
[ -f "$R/cert.pem" ] || openssl req -x509 -newkey rsa:2048 -nodes -days 2 -keyout "$R/key.pem" -out "$R/cert.pem" \
  -subj "/CN=*.test" -addext "subjectAltName=DNS:*.test,DNS:test" 2>/dev/null
chmod 644 "$R/key.pem"
for i in $(seq -w 1 22); do
  d=$W/www/site$i
  mkdir -p "$d/assets"
  echo "static ok site$i" > "$d/hello.txt"
  echo "body{color:red}" > "$d/assets/app.css"
  # explicit Content-Length: without it nginx answers chunked, and ab (the load
  # tool) stalls on chunked responses over TLS, which would measure ab, not nginx
  printf '<?php ob_start(); echo "php ok site%s uri=" . $_SERVER["REQUEST_URI"] . " q=" . ($_GET["p"] ?? "-") . "\\n"; header("Content-Length: " . ob_get_length()); ob_end_flush();' "$i" > "$d/index.php"
  echo '<?php echo "secret";' > "$d/wp-config.php"
  echo "hidden" > "$d/.env"
done
chmod -R a+rX "$W/www"
cat > "$R/php-fpm.conf" <<EOF
[global]
pid = $R/php-fpm.pid
error_log = $R/php-fpm.log
daemonize = yes
[www]
user = www-data
group = www-data
listen = $R/php.sock
listen.owner = root
listen.mode = 0666
pm = static
pm.max_children = 8
EOF
