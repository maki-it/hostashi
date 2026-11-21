#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Installs Hostashi from its apt repository (ADR-0050) on a fresh Debian 13
# or Ubuntu 26.04 server. Served at https://hostashi.com/install.sh:
#
#   curl -fsSLO https://hostashi.com/install.sh
#   less install.sh
#   sudo bash install.sh --yes --panel-domain panel.example.com
#
# It trusts the Hostashi signing key (built into this script) for the
# Hostashi repository only, adds the repository with a pin that lets it
# provide the hostashi package and nothing else, installs the package with
# apt, and runs the installer from the package (/usr/share/hostashi/
# install.sh) with the arguments given here. Without --yes the installer
# only shows its plan. Updates then come with apt upgrade.
#
# HOSTASHI_APT_URL overrides the repository address (tests, mirrors).
set -euo pipefail

APT_URL=${HOSTASHI_APT_URL:-https://hostashi.com/apt}
FINGERPRINT=64F6AE8DC40D1299E68A008F735A7A2D741149DB
KEYRING=/usr/share/keyrings/hostashi-archive-keyring.asc
SOURCES=/etc/apt/sources.list.d/hostashi.sources
PIN=/etc/apt/preferences.d/hostashi

die() { echo "hostashi: $*" >&2; exit 1; }

[ "$(id -u)" = 0 ] || die "run as root (sudo bash install.sh ...)"
# shellcheck disable=SC1091
. /etc/os-release
case "${ID:-}-${VERSION_ID:-}" in
  debian-13 | ubuntu-26.04) ;; # ADR-0020
  *) [[ " $* " == *" --allow-untested "* ]] ||
       die "unsupported system '${PRETTY_NAME:-unknown}' (supported: Debian 13, Ubuntu 26.04)";;
esac
case "$APT_URL" in
  https://* | http://127.0.0.1[:/]* | http://localhost[:/]* | file:/*) ;;
  *) die "HOSTASHI_APT_URL must be https://";;
esac
host=${APT_URL#*://}
host=${host%%[/:]*}

# The key, for this repository only (Signed-By), never trusted globally.
umask 022
cat >"$KEYRING.tmp" <<'KEY'
-----BEGIN PGP PUBLIC KEY BLOCK-----

mDMEawGwZhYJKwYBBAHaRw8BAQdAsMdxMPstFVqS4aO/YkqIhb7RS9GUmYvMSVET
6hq1MJK0MUhvc3Rhc2hpIGFyY2hpdmUgc2lnbmluZyBrZXkgPGtvbnRha3RAbWFr
aS1pdC5kZT6ImQQTFgoAQRYhBGT2ro3EDRKZ5ooAj3Naei10EUnbBQJrAbBmAhsD
BQkDwmcABQsJCAcCAiICBhUKCQgLAgQWAgMBAh4HAheAAAoJEHNaei10EUnbR2kA
/34kzUWCCe6Ddj/iKlODIifGv97gdMI/Xg8fFY8ju8HaAP9pcPxiW+/iCJmCfx1F
7XFEsnozWsWZcYrI+dydIPjRAg==
=en6q
-----END PGP PUBLIC KEY BLOCK-----
KEY
mv -f "$KEYRING.tmp" "$KEYRING"

cat >"$SOURCES" <<SRC
# Hostashi (ADR-0050). Signed by $FINGERPRINT.
Types: deb
URIs: $APT_URL
Suites: stable
Components: main
Signed-By: $KEYRING
SRC

# The repository may provide the hostashi package only.
cat >"$PIN" <<PREF
# Hostashi (ADR-0050): from this repository only the hostashi package.
Package: *
Pin: origin "$host"
Pin-Priority: -1

Package: hostashi
Pin: origin "$host"
Pin-Priority: 500
PREF

export DEBIAN_FRONTEND=noninteractive
apt-get update -o APT::Update::Error-Mode=any || die "apt-get update failed; see above"
apt-get install -y --no-install-recommends hostashi || die "installing the hostashi package failed; see above"

echo "hostashi: package $(dpkg-query -W -f '${Version}' hostashi) installed; setting up the server"
mode=plan
for a in "$@"; do [ "$a" != --yes ] || mode=apply; done
exec /usr/share/hostashi/install.sh "$mode" --binary /usr/bin/hostashi "$@"
