#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Builds the signed apt repository of Hostashi (ADR-0050, section 2) from
# .deb files: one suite "stable", component "main", amd64 and arm64.
#
#   build.sh DEB_DIR OUT_DIR
#
# With SUITE=runtimes PACKAGE=hostashi-runtimes it builds the suite of the
# runtime pins instead (ADR-0077), signed by the runtimes key; the paths
# below then say runtimes and hostashi-runtimes. The Pages job puts both
# suites side by side.
#
# The signing key comes from gpg: the only secret key in GNUPGHOME (the
# release job imports APT_SIGNING_KEY into an empty one). OUT_DIR gets
#   dists/stable/{Release,InRelease,Release.gpg}
#   dists/stable/main/binary-<arch>/Packages{,.gz,.xz}
#   pool/main/h/hostashi/<the .deb files>
#   hostashi.asc        the public key, for the docs and the installer
# Needs apt-ftparchive (apt-utils), gpg, gzip and xz.
set -euo pipefail

DEBS=${1:?usage: build.sh DEB_DIR OUT_DIR}
OUT=${2:?usage: build.sh DEB_DIR OUT_DIR}
SUITE=${SUITE:-stable}
PACKAGE=${PACKAGE:-hostashi}
ARCHS=(amd64 arm64)
case "$SUITE/$PACKAGE" in
  stable/hostashi) DESCRIPTION="Hostashi hosting control panel";;
  runtimes/hostashi-runtimes) DESCRIPTION="Hostashi runtime pins (Node.js, CPython)";;
  *) echo "apt-repo: unknown suite $SUITE with package $PACKAGE" >&2; exit 64;;
esac

die() { echo "apt-repo: $*" >&2; exit 1; }
for t in apt-ftparchive gpg gzip xz; do command -v "$t" >/dev/null || die "$t is missing"; done

# A key lists its fingerprint once per (sub)key; the primary comes first.
fpr=$(gpg --batch --with-colons --list-secret-keys | awk -F: '$1 == "sec" { s = 1; next } s && $1 == "fpr" { print $10; exit }')
[ -n "$fpr" ] || die "no secret key in GNUPGHOME"
[ "$(gpg --batch --with-colons --list-secret-keys | grep -c '^sec:')" = 1 ] || die "more than one secret key in GNUPGHOME"

shopt -s nullglob
debs=("$DEBS"/*.deb)
[ ${#debs[@]} -gt 0 ] || die "no .deb in $DEBS"
[ ! -e "$OUT" ] || [ -z "$(ls -A "$OUT")" ] || die "$OUT is not empty"

pool=$OUT/pool/main/h/$PACKAGE
mkdir -p "$pool"
for d in "${debs[@]}"; do
  [ "$(dpkg-deb -f "$d" Package)" = "$PACKAGE" ] || die "$d is not the $PACKAGE package"
  cp "$d" "$pool/"
done

cd "$OUT"
for a in "${ARCHS[@]}"; do
  dir=dists/$SUITE/main/binary-$a
  mkdir -p "$dir"
  apt-ftparchive --arch "$a" packages pool >"$dir/Packages"
  gzip -9n <"$dir/Packages" >"$dir/Packages.gz"
  xz -9 <"$dir/Packages" >"$dir/Packages.xz"
done

apt-ftparchive \
  -o APT::FTPArchive::Release::Origin=Hostashi \
  -o APT::FTPArchive::Release::Label=Hostashi \
  -o APT::FTPArchive::Release::Suite="$SUITE" \
  -o APT::FTPArchive::Release::Codename="$SUITE" \
  -o APT::FTPArchive::Release::Architectures="${ARCHS[*]}" \
  -o APT::FTPArchive::Release::Components=main \
  -o APT::FTPArchive::Release::Description="$DESCRIPTION" \
  release "dists/$SUITE" >"dists/$SUITE/Release.tmp"
mv "dists/$SUITE/Release.tmp" "dists/$SUITE/Release"

sign=(gpg --batch --yes --pinentry-mode loopback --passphrase '' --local-user "$fpr" --digest-algo SHA512)
"${sign[@]}" --clearsign -o "dists/$SUITE/InRelease" "dists/$SUITE/Release"
"${sign[@]}" --armor --detach-sign -o "dists/$SUITE/Release.gpg" "dists/$SUITE/Release"
gpg --batch --armor --export "$fpr" >"$PACKAGE.asc"

echo "apt-repo: ${#debs[@]} package(s), signed by $fpr"
