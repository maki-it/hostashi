#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Builds the package hostashi-runtimes (ADR-0077 part 2): the runtimes data
# file with the Node.js, CPython and WP-CLI pins of main, nothing else. The binary
# reads it at start (internal/runtimepins) and takes newer patch releases
# of the lines it knows from it; postinst restarts agent and panel so they
# do. The package holds no code.
#
#   build-deb.sh RUNTIMES_JSON VERSION OUT_DIR
#
# VERSION must grow with every change of the pins; the workflow uses the
# UTC time of the last commit that changed them (YYYYMMDDHHMMSS). The
# build is reproducible: same file and version, same .deb.
set -euo pipefail

JSON=${1:?usage: build-deb.sh RUNTIMES_JSON VERSION OUT_DIR}
VERSION=${2:?usage: build-deb.sh RUNTIMES_JSON VERSION OUT_DIR}
OUT=${3:?usage: build-deb.sh RUNTIMES_JSON VERSION OUT_DIR}
HERE=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

die() { echo "runtimes-deb: $*" >&2; exit 1; }
[[ "$VERSION" =~ ^[0-9]{14}$ ]] || die "VERSION must be YYYYMMDDHHMMSS, not '$VERSION'"
[ -s "$JSON" ] || die "$JSON is missing or empty"
[ "$(head -c 1 "$JSON")" = '{' ] || die "$JSON is not a JSON object"
command -v dpkg-deb >/dev/null || die "dpkg-deb is missing"

root=$(mktemp -d)
chmod 0755 "$root"
trap 'rm -rf "$root"' EXIT
install -d -m 0755 "$root/DEBIAN" "$root/usr/share/hostashi" "$root/usr/share/doc/hostashi-runtimes"
install -m 0644 "$JSON" "$root/usr/share/hostashi/runtimes.json"
install -m 0644 "$HERE/../../LICENSE" "$root/usr/share/doc/hostashi-runtimes/copyright"
install -m 0755 "$HERE/postinst" "$root/DEBIAN/postinst"
cat >"$root/DEBIAN/control" <<CONTROL
Package: hostashi-runtimes
Version: $VERSION
Architecture: all
Maintainer: Maki IT <kontakt@maki-it.de>
Section: admin
Priority: optional
Homepage: https://github.com/maki-it/hostashi
Description: Node.js, CPython and WP-CLI pins for Hostashi
 Newer patch releases of the Node.js and CPython lines Hostashi installs,
 and the newest WP-CLI release, with their checksums. Hostashi takes a pin
 from this package only when it is newer than its own (for Node.js and
 CPython only of a line it knows); the downloads still come from their
 publishers or Hostashi's mirror and must match the checksum.
CONTROL

# Reproducible: fixed times and owners.
epoch=$(date -u -d "${VERSION:0:8} ${VERSION:8:2}:${VERSION:10:2}:${VERSION:12:2}" +%s)
find "$root" -exec touch -h -d "@$epoch" {} +
mkdir -p "$OUT"
deb=$OUT/hostashi-runtimes_${VERSION}_all.deb
SOURCE_DATE_EPOCH=$epoch dpkg-deb --root-owner-group -Zxz --build "$root" "$deb" >/dev/null
echo "$deb"
