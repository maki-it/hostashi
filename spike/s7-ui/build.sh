#!/usr/bin/env bash
# Builds both SPA candidates and one Go binary per candidate (SPA embedded).
set -euo pipefail
cd "$(dirname "$0")"
npm ci --silent
mkdir -p bin
for fw in react svelte; do
  npm -w "web-$fw" run build --silent
  rm -rf server/ui/dist && mkdir -p server/ui/dist
  cp -r "web-$fw/dist/." server/ui/dist/
  (cd server && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o "../bin/s7-$fw" .)
done
# Restore the committed placeholder so the tree stays clean.
rm -rf server/ui/dist && mkdir -p server/ui/dist
printf '<!doctype html><title>placeholder</title>' > server/ui/dist/index.html
(cd server && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o ../bin/s7-empty .)
ls -l bin
