#!/bin/bash
# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Runs the bootstrap script packaging/get/install.sh (ADR-0050) against a
# local copy of the apt repository, served over HTTP inside a throwaway
# container: it adds key, source and pin, installs the hostashi package
# from the repository and hands over to the installer's plan. The
# repository also offers a package that is not hostashi; the pin must keep
# apt from installing it. The copy of the script trusts a throwaway key
# instead of the real one (packaging/test/key-test.sh checks the real one).
#
#   docker run --rm -v "$PWD/dist:/dist:ro" -v "$PWD:/src:ro" IMAGE \
#     /src/packaging/test/get-test.sh /dist
set -euo pipefail

DIST=${1:?usage: get-test.sh DIST_DIR}
fail() { echo "FAIL: $*" >&2; exit 1; }
export DEBIAN_FRONTEND=noninteractive

apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends apt-utils gnupg xz-utils python3 >/dev/null

work=$(mktemp -d)
chmod 0755 "$work"
mkdir "$work/debs"
cp "$DIST"/hostashi_*.deb "$work/debs/"

# A package the repository must not be able to provide.
mkdir -p "$work/other/DEBIAN"
printf 'Package: hostashi-test-other\nVersion: 1.0\nArchitecture: all\nMaintainer: test <test@example.invalid>\nDescription: not hostashi\n' \
  >"$work/other/DEBIAN/control"
dpkg-deb -b "$work/other" "$work/hostashi-test-other_1.0_all.deb" >/dev/null

mkdir -m 0700 "$work/gpg"
export GNUPGHOME=$work/gpg
gpg --batch --quiet --trust-model always --passphrase '' \
  --quick-gen-key "Hostashi test <test@example.invalid>" ed25519 sign 1d 2>/dev/null
fpr=$(gpg --batch --with-colons --list-keys | awk -F: '$1 == "fpr" { print $10; exit }')

# build.sh takes hostashi packages only: add the other one to its output
# and index and sign again the same way.
/src/packaging/apt-repo/build.sh "$work/debs" "$work/www/apt" >/dev/null
cp "$work/hostashi-test-other_1.0_all.deb" "$work/www/apt/pool/main/h/hostashi/"
(
  cd "$work/www/apt"
  for a in amd64 arm64; do
    d=dists/stable/main/binary-$a
    apt-ftparchive --arch "$a" packages pool >"$d/Packages"
    gzip -9n <"$d/Packages" >"$d/Packages.gz"
    xz -9 <"$d/Packages" >"$d/Packages.xz"
  done
  apt-ftparchive -o APT::FTPArchive::Release::Origin=Hostashi -o APT::FTPArchive::Release::Suite=stable \
    -o APT::FTPArchive::Release::Codename=stable -o APT::FTPArchive::Release::Components=main \
    -o "APT::FTPArchive::Release::Architectures=amd64 arm64" release dists/stable >"$work/Release"
  mv "$work/Release" dists/stable/Release
  gpg --batch --yes --clearsign --digest-algo SHA512 -o dists/stable/InRelease dists/stable/Release
  rm -f dists/stable/Release.gpg
)
grep -q '^Package: hostashi-test-other$' "$work/www/apt/dists/stable/main/binary-amd64/Packages" || fail "test package not in the index"

(cd "$work/www" && exec python3 -m http.server 8000 --bind 127.0.0.1 >/dev/null 2>&1) &
for _ in $(seq 1 30); do
  python3 -c 'import urllib.request; urllib.request.urlopen("http://127.0.0.1:8000/apt/hostashi.asc")' 2>/dev/null && break
  sleep 0.5
done

# The script with the throwaway key in place of the real one.
key=$(gpg --batch --armor --export "$fpr")
awk -v key="$key" -v fpr="$fpr" '
  /^FINGERPRINT=/ { print "FINGERPRINT=" fpr; next }
  /^cat >"\$KEYRING.tmp" <<'\''KEY'\''$/ { print; print key; skip = 1; next }
  skip && /^KEY$/ { skip = 0 }
  !skip { print }
' /src/packaging/get/install.sh >"$work/get.sh"
grep -q "^FINGERPRINT=$fpr$" "$work/get.sh" || fail "could not prepare the script"

unset GNUPGHOME
if HOSTASHI_APT_URL=http://example.com/apt bash "$work/get.sh" >/dev/null 2>&1; then fail "accepted a plain-HTTP repository"; fi

out=$(HOSTASHI_APT_URL=http://127.0.0.1:8000/apt bash "$work/get.sh" --no-firewall 2>&1) && rc=0 || rc=$?
echo "$out" | tail -n 5
grep -q 'installed; setting up the server' <<<"$out" || fail "the package was not installed: $out"
grep -q 'preflight' <<<"$out" || fail "the installer's plan did not run"
[ "$(dpkg-query -W -f '${Status}' hostashi)" = "install ok installed" ] || fail "hostashi not installed"
arch=$(dpkg --print-architecture)
[ "$(dpkg-query -W -f '${Version}' hostashi)" = "$(dpkg-deb -f "$work"/debs/hostashi_*_"$arch".deb Version)" ] || fail "wrong version"
grep -qx "Signed-By: /usr/share/keyrings/hostashi-archive-keyring.asc" /etc/apt/sources.list.d/hostashi.sources || fail "source without Signed-By"
[ ! -e /etc/apt/trusted.gpg.d/hostashi.asc ] || fail "key trusted globally"
pol=$(apt-cache policy hostashi-test-other)
grep -q 'Candidate: (none)' <<<"$pol" || fail "the pin lets the repository provide another package: $pol"
if apt-get install -y -qq hostashi-test-other >/dev/null 2>&1; then fail "installed another package from the repository"; fi

echo "OK: bootstrap with hostashi on $(. /etc/os-release && echo "$PRETTY_NAME") ($arch), exit $rc of the plan without systemd"
