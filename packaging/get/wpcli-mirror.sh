#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Puts the pinned WP-CLI phars on get.hostashi.com (ADR-0069 addendum 2):
# GitHub, where WP-CLI publishes them, has no IPv6 address, so a server
# without IPv4 downloads them from the mirror. Each PIN_FILE is a copy of
# internal/agentapi/wpcli.go (of main and of the latest stable release);
# its version and SHA-512 are read from the file, the phar is downloaded
# from the GitHub release and kept only when it matches. The layout is
# GitHub's: OUT_DIR/wp-cli/v<version>/wp-cli-<version>.phar.
#
#   wpcli-mirror.sh OUT_DIR PIN_FILE...
#
# WPCLI_URL (optional, tests): the base instead of the GitHub releases.
set -euo pipefail

OUT=${1:?usage: wpcli-mirror.sh OUT_DIR PIN_FILE...}
shift
[ $# -gt 0 ] || { echo "usage: wpcli-mirror.sh OUT_DIR PIN_FILE..." >&2; exit 2; }
URL=${WPCLI_URL:-https://github.com/wp-cli/wp-cli/releases/download}

die() { echo "wpcli-mirror: $*" >&2; exit 1; }

for pin in "$@"; do
  [ -s "$pin" ] || die "$pin is missing or empty"
  version=$(sed -n 's/^[[:space:]]*Version:[[:space:]]*"\([^"]*\)",.*$/\1/p' "$pin")
  sum=$(sed -n 's/^[[:space:]]*SHA512:[[:space:]]*"\([^"]*\)",.*$/\1/p' "$pin")
  [ "$(wc -l <<<"$version")" = 1 ] && [ "$(wc -l <<<"$sum")" = 1 ] || die "$pin: not exactly one Version and SHA512"
  [[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "$pin: version '$version'"
  [[ "$sum" =~ ^[0-9a-f]{128}$ ]] || die "$pin: SHA-512 '$sum'"
  dir=$OUT/wp-cli/v$version
  file=$dir/wp-cli-$version.phar
  if [ -f "$file" ]; then
    echo "wp-cli $version: already there"
    continue
  fi
  mkdir -p "$dir"
  curl -fsSL --proto '=https,file' --max-filesize 67108864 -o "$file.tmp" "$URL/v$version/wp-cli-$version.phar" ||
    die "download of wp-cli $version failed"
  got=$(sha512sum "$file.tmp" | cut -d' ' -f1)
  if [ "$got" != "$sum" ]; then
    rm -f "$file.tmp"
    die "wp-cli $version: SHA-512 $got, pinned $sum"
  fi
  chmod 0644 "$file.tmp"
  mv "$file.tmp" "$file"
  echo "wp-cli $version: mirrored, SHA-512 matches the pin"
done
