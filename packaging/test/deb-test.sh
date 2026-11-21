#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Checks the .deb that GoReleaser builds (ADR-0050) inside a throwaway
# container of a supported distribution: its dependencies cover every
# package the installer installs and all exist in the distribution, it
# installs with apt, the binary runs and reports the package version, the
# frontend is embedded, the installer and its data files sit where the
# installer looks for them, an install over itself changes nothing, and
# removal leaves no file behind.
#
#   docker run --rm -v "$PWD/dist:/dist:ro" -v "$PWD:/src:ro" IMAGE \
#     /src/packaging/test/deb-test.sh /dist
set -euo pipefail

DIST=${1:?usage: deb-test.sh DIST_DIR}
fail() { echo "FAIL: $*" >&2; exit 1; }

arch=$(dpkg --print-architecture)
debs=("$DIST"/hostashi_*_"$arch".deb)
[ ${#debs[@]} -eq 1 ] && [ -f "${debs[0]}" ] || fail "expected exactly one hostashi_*_$arch.deb in $DIST"
deb=${debs[0]}
echo "package: $deb"

# Metadata.
[ "$(dpkg-deb -f "$deb" Package)" = hostashi ] || fail "package name"
[ "$(dpkg-deb -f "$deb" Architecture)" = "$arch" ] || fail "architecture"
version=$(dpkg-deb -f "$deb" Version)
[ -n "$version" ] || fail "no version"

# Every package the installer installs (the *_PKGS lists and the literal
# apt_install calls) is a dependency, so a run with --no-apt from the
# package's maintainer scripts finds them. quota is only recommended
# (--no-quota); phpredis is versioned and installed by the installer.
deps=$(dpkg-deb -f "$deb" Depends | tr ',' '\n' | sed 's/^ *//; s/ .*//')
recs=$(dpkg-deb -f "$deb" Recommends | tr ',' '\n' | sed 's/^ *//; s/ .*//')
want=$({
  sed -n 's/^[A-Z0-9_]*_PKGS=(\(.*\))$/\1/p' /src/packaging/install.sh | tr ' ' '\n'
  grep -oE '^[[:space:]]*apt_install [a-z0-9][a-z0-9.+-]*' /src/packaging/install.sh | awk '{print $2}'
} | sort -u)
[ -n "$want" ] || fail "found no package lists in install.sh"
for p in $want; do
  grep -qx "$p" <<<"$deps" || grep -qx "$p" <<<"$recs" || fail "install.sh installs $p, the package does not depend on it"
done
grep -qx quota <<<"$recs" || fail "quota should be recommended"

# Install with apt, as the install script does: dependencies from the
# distribution, no recommends.
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq >/dev/null || fail "apt-get update"
apt-get install -y -qq --no-install-recommends "$deb" >/dev/null || fail "apt-get install"
for p in $deps; do
  [ "$(dpkg-query -W -f '${Status}' "$p" 2>/dev/null)" = "install ok installed" ] || fail "dependency $p not installed"
done
[ "$(dpkg-query -W -f '${Status}' hostashi)" = "install ok installed" ] || fail "not installed"

# The binary runs and carries the version. nFPM writes a pre-release as
# "~pre" into the .deb; the binary keeps GoReleaser's "-pre" spelling.
got=$(/usr/bin/hostashi version)
[ "$got" = "${version/\~/-}" ] || fail "hostashi version: got '$got', package is '$version'"
[ "$(stat -c '%a %U:%G' /usr/bin/hostashi)" = "755 root:root" ] || fail "binary owner or mode"

# The frontend build is embedded (a binary without it serves a notice only).
grep -q 'assets/index-' /usr/bin/hostashi || fail "frontend not embedded"

# The installer and every data directory it reads ($HERE/<dir> in install.sh).
share=/usr/share/hostashi
[ -x "$share/install.sh" ] || fail "install.sh missing or not executable"
for d in adminer apt fail2ban firewall mariadb php ssh sysctl systemd; do
  [ -d "$share/$d" ] || fail "$share/$d missing"
  # Same files as in the source tree, byte for byte.
  diff -r "/src/packaging/$d" "$share/$d" >/dev/null || fail "$share/$d differs from packaging/$d"
done
cmp -s /src/packaging/install.sh "$share/install.sh" || fail "install.sh differs"
[ -x "$share/php/php-install" ] || fail "php-install lost its mode"
[ -x "$share/apt/php-restart" ] || fail "php-restart lost its mode"
[ ! -e "$share/test" ] || fail "test scripts must not ship"
[ -f /usr/share/doc/hostashi/copyright ] || fail "copyright missing"

# Nothing outside /usr, and no file writable by others.
bad=$(dpkg -L hostashi | grep -vE '^/(\.|usr(/.*)?)$' || true)
[ -z "$bad" ] || fail "files outside /usr: $bad"
ww=$(dpkg -L hostashi | while read -r f; do [ -e "$f" ] && [ ! -L "$f" ] && find "$f" -maxdepth 0 -perm -o+w; done)
[ -z "$ww" ] || fail "world-writable: $ww"

# Installing the same package again works and changes nothing.
sum() { dpkg -L hostashi | while read -r f; do [ -f "$f" ] && sha256sum "$f"; done | sort; }
before=$(sum)
apt-get install -y -qq --no-install-recommends --reinstall "$deb" >/dev/null || fail "reinstall"
[ "$(sum)" = "$before" ] || fail "reinstall changed files"

# Purge leaves nothing of the package behind (dependencies stay, as apt does).
files=$(dpkg -L hostashi | grep -vE '^/(\.|usr|usr/bin|usr/share|usr/share/doc)$')
apt-get purge -y -qq hostashi >/dev/null || fail "purge"
for f in $files; do [ ! -e "$f" ] || fail "left behind: $f"; done

echo "OK: hostashi $version on $(. /etc/os-release && echo "$PRETTY_NAME") ($arch)"
