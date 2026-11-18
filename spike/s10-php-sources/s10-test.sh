#!/bin/bash
# THROWAWAY SPIKE S10. Runs INSIDE a fresh debian:trixie or ubuntu:24.04 container (no systemd needed, nothing is started).
# Question: after adding sury.org, can the distribution's default PHP stay from the distribution while extra versions come from sury.org?
# usage: s10-test.sh <scenario: nopin|pin100|pinpkg>
set -u
SC=${1:-nopin}
. /etc/os-release; CODE=$VERSION_CODENAME
export DEBIAN_FRONTEND=noninteractive
APT="apt-get -y -qq --no-install-recommends -o Dpkg::Options::=--force-confold"
case $ID in debian) DEF=8.4; REPO=https://packages.sury.org/php;; ubuntu) DEF=8.3; REPO=https://packages.sury.org/php;; esac
echo "##### $ID $VERSION_ID scenario=$SC default=$DEF"
apt-get update -qq; $APT install ca-certificates curl gnupg >/dev/null 2>&1
echo "== step 1: fresh server with distribution PHP"
$APT install php-fpm php-cli php-mysql php-mbstring php-xml php-curl php-zip php-gd php-intl php-bcmath >/dev/null 2>&1
dpkg-query -W -f='${Package} ${Version}\n' php$DEF-fpm php-common | sed 's/^/  /'
BASE=$(dpkg-query -W -f='${Package} ${Version}\n' | grep -E '^php' | sort); echo "  installed php packages: $(echo "$BASE" | wc -l)"
echo "== step 2: add sury.org (keyring, sources)"
curl -fsSL https://packages.sury.org/php/apt.gpg | gpg --dearmor > /usr/share/keyrings/sury-php.gpg
echo "deb [signed-by=/usr/share/keyrings/sury-php.gpg] $REPO $CODE main" > /etc/apt/sources.list.d/sury-php.list
case $SC in
  nopin) : ;;
  pin100)   # all sury.org packages below the default priority 500: only used where nothing else offers the package
    printf 'Package: *\nPin: origin packages.sury.org\nPin-Priority: 100\n' > /etc/apt/preferences.d/99-sury ;;
  pinpkg)   # sury.org wins only for php-common and the versioned packages of every version EXCEPT the distribution default
    EXTRA=""; for v in 5.6 7.0 7.1 7.2 7.3 7.4 8.0 8.1 8.2 8.3 8.4 8.5; do [ "$v" = "$DEF" ] || EXTRA="$EXTRA php$v*"; done
    printf 'Package: *\nPin: origin packages.sury.org\nPin-Priority: 100\n\nPackage: php-common%s\nPin: origin packages.sury.org\nPin-Priority: 600\n' "$EXTRA" > /etc/apt/preferences.d/99-sury
    echo "  pin file:"; sed 's/^/    /' /etc/apt/preferences.d/99-sury ;;
esac
apt-get update -qq
echo "  candidate of php$DEF-fpm: $(apt-cache policy php$DEF-fpm | awk '/Candidate/{print $2}')"
echo "  candidate of php-common:  $(apt-cache policy php-common | awk '/Candidate/{print $2}')"
echo "== step 3: apt-get upgrade (simulated): would it replace the distribution PHP?"
apt-get -s upgrade 2>/dev/null | grep -E '^Inst php' | awk '{print "  "$1,$2,$3,$4}' | head -8; echo "  upgrade would touch $(apt-get -s upgrade 2>/dev/null | grep -c '^Inst php') php packages"
echo "== step 4: install extra versions 8.1 and 7.4 (+ mysql)"
$APT install php8.1-fpm php8.1-mysql php7.4-fpm php7.4-mysql 2>&1 | grep -E "^E:|^W:|Unable|Depends|Breaks" | head -8
echo "  exit of install: ${PIPESTATUS[0]}"
echo "== result: where do the packages come from?"
src_of() { # origin of the INSTALLED version, from apt's own policy output (version strings of sury.org do not always say "sury")
  apt-cache policy "$1" | awk '/^ \*\*\*/{getline; print ($0 ~ /sury/) ? "sury.org" : "distribution"; exit}'; }
for p in php$DEF-fpm php$DEF-cli php$DEF-mysql php-common php8.1-fpm php8.1-common php7.4-fpm php7.4-common; do
  v=$(dpkg-query -W -f='${Version}' $p 2>/dev/null) || { echo "  $p: not installed"; continue; }
  printf '  %-16s %-48s %s\n' $p "$v" "$(src_of $p)"
done
for v in 7.4 8.1 $DEF; do printf '  php-fpm%s -t: ' $v; php-fpm$v -t 2>&1 | grep -E 'successful|ERROR' | head -2 | tr '\n' ' '; echo; done
echo "== step 4b: consistency after the install"
apt-get check 2>&1 | tail -1 | sed 's/^/  apt-get check: /'; dpkg --audit | head -3 | sed 's/^/  dpkg --audit: /'; echo "  pending php upgrades: $(apt-get -s upgrade 2>/dev/null | grep -c '^Inst php')"
apt-get -s upgrade 2>/dev/null | grep '^Inst php' | head -5 | awk '{print "    "$1,$2,$3,$4}'
echo "== step 5: later upgrade of the default PHP (simulated)"
apt-get -s install --only-upgrade php$DEF-fpm 2>/dev/null | grep -E '^Inst' | awk '{print "  "$1,$2,$3,$4}' | head -3
echo "== step 6: apt-get -s dist-upgrade overall conflicts"; apt-get -s dist-upgrade 2>&1 | grep -E "^E:|broken" | head -3; echo "  done"
