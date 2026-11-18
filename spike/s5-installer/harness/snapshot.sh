#!/bin/bash
# Prints a stable fingerprint of everything the installer may touch (files, permissions, packages, unit state).
# Deliberately excludes /var/log, /var/lib/apt/lists, /var/cache, /var/lib/mysql (data), /var/lib/dpkg/info timestamps.
find /etc /usr/local /usr/share/keyrings /lib/systemd/system /var/lib/hostashi /var/lib/hostashi-installer /var/log/hostashi -xdev \
  \( -path /etc/ssl/certs -o -path /etc/mtab -o -path /etc/resolv.conf -o -path /etc/hosts -o -path /etc/hostname -o -path /etc/ld.so.cache -o -path '/etc/alternatives' \) -prune -o \
  \( -type f -o -type d -o -type l \) -printf '%p %y %m %U:%G %l\n' | sort
echo "--- sha256"
find /etc /usr/local /usr/share/keyrings -xdev -type f ! -path '/etc/ssl/certs/*' ! -path /etc/resolv.conf ! -path /etc/hosts ! -path /etc/hostname ! -path /etc/ld.so.cache -exec sha256sum {} + 2>/dev/null | sort -k2
echo "--- dpkg"
dpkg-query -W -f='${Package} ${Version} ${db:Status-Abbrev}\n' | sort
echo "--- units"
systemctl list-unit-files --no-legend --no-pager | awk '{print $1, $2}' | sort
systemctl list-units --no-legend --no-pager --state=active,failed --type=service | awk '{print $1, $3, $4}' | sort
echo "--- listeners"
ss -Hltn | awk '{print $4}' | sort
