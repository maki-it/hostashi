#!/bin/bash
# usage: run.sh <image> <path to built client binary>
# Sandbox-only: proxy and CA bundle for the TLS-intercepting egress proxy.
IMG=$1; CLIENT=$2; N=s12-$(echo "$IMG" | tr ':/' '--'); HERE=$(cd "$(dirname "$0")" && pwd)
docker rm -f "$N" >/dev/null 2>&1; docker run -d --name "$N" --network host "$IMG" sleep 3000 >/dev/null
docker exec "$N" mkdir -p /etc/ssl/certs; docker cp /root/.ccr/ca-bundle.crt "$N":/etc/ssl/certs/ca-certificates.crt
docker exec "$N" bash -c "echo 'Acquire::https::Proxy \"$HTTPS_PROXY\";' > /etc/apt/apt.conf.d/99p"
docker cp "$HERE/s12-test.sh" "$N":/s12-test.sh; docker cp "$CLIENT" "$N":/client
docker exec "$N" /s12-test.sh
docker rm -f "$N" >/dev/null
