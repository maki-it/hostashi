#!/bin/sh
# Compares how nginx and Caddy report a config that is syntactically valid but
# cannot be activated (port already in use), plus an unknown directive.
# Needs: /tmp/s3/bin/{gen,caddy}, nginx from nginx.org at $NGINX, setup.sh run.
set -u
NGINX=${NGINX:-/tmp/s3/nginxorg/usr/sbin/nginx}
CADDY=${CADDY:-/tmp/s3/bin/caddy}
CFG=${CFG:-/tmp/s3/cfg}
RUN=/tmp/s3/work/run
export XDG_DATA_HOME=/tmp/s3/work/xdg-data XDG_CONFIG_HOME=/tmp/s3/work/xdg-config
cd "$CFG"
python3 -c "
import socket,time
s=socket.socket(); s.bind(('127.0.0.1',18999)); s.listen(); time.sleep(12)" &
sleep 0.5

echo "== nginx"
$NGINX -p $RUN -c $CFG/nginx.conf; sleep 1
{ sed '$d' nginx.conf; printf '    server { listen 127.0.0.1:18999; server_name busy.test; return 200 x; }\n}\n'; } > nginx.busy.conf
$NGINX -p $RUN -c $CFG/nginx.busy.conf -t >/dev/null 2>&1; echo "nginx -t on busy-port config: exit $?"
cp nginx.conf nginx.conf.keep; cp nginx.busy.conf nginx.conf
$NGINX -p $RUN -c $CFG/nginx.conf -s reload; echo "nginx -s reload: exit $? (no error shown to the caller)"
sleep 1; grep emerg $RUN/nginx-error.log | tail -1 | cut -c1-160
cp nginx.conf.keep nginx.conf; $NGINX -p $RUN -c $CFG/nginx.conf -s quit; sleep 1

echo "== caddy"
(setsid nohup $CADDY run --config Caddyfile --adapter caddyfile >/dev/null 2>&1 </dev/null &); sleep 3
{ cat Caddyfile; printf 'http://busy.test:18999 {\n respond "x"\n}\n'; } > Caddyfile.busy
$CADDY validate --config Caddyfile.busy --adapter caddyfile >/dev/null 2>&1; echo "caddy validate on busy-port config: exit $?"
$CADDY reload --config Caddyfile.busy --adapter caddyfile 2>&1 | grep -o 'address already in use' | head -1
$CADDY reload --config Caddyfile.busy --adapter caddyfile >/dev/null 2>&1; echo "caddy reload: exit $? (error returned to the caller, old config kept)"
{ cat Caddyfile; printf 'https://bad.test:18443 {\n nosuchdirective\n}\n'; } > Caddyfile.bad
$CADDY reload --config Caddyfile.bad --adapter caddyfile 2>&1 | grep -o 'unrecognized directive: nosuchdirective' | head -1
pkill -f "caddy run"; wait
