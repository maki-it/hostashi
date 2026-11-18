#!/bin/bash
# Runs INSIDE the test server after the installer. Prints facts; exits 1 on a failed assertion.
rc=0; ok() { echo "  ok    $*"; }; bad() { echo "  FAIL  $*"; rc=1; }
v=$(nginx -v 2>&1); echo "  info  $v, package $(dpkg-query -W -f='${Version}' nginx)"
case "$(dpkg-query -W -f='${Version}' nginx)" in *~trixie|*~noble) ok "nginx comes from nginx.org";; *) bad "nginx not from nginx.org";; esac
pol=$(apt-cache policy nginx | sed -n '/\*\*\*/,+1p' | tr '\n' ' '); echo "  info  $pol"
[ "$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1/)" = 200 ] && ok "nginx answers on :80 (default page)" || bad "nginx on :80"
ps -o user= -C nginx | sort -u | tr '\n' ' ' | grep -q nginx && ok "nginx workers run as: $(ps -o user= -C nginx | sort -u | tr '\n' ' ')"
echo "  info  mariadb $(mariadb -NBe 'select version()')"
[ "$(mariadb -NBe 'select @@bind_address')" = 127.0.0.1 ] && ok "mariadb bind-address 127.0.0.1" || bad "mariadb bind"
echo "  info  mariadb users: $(mariadb -NBe "select concat(user,'@',host,':',plugin) from mysql.user" | tr '\n' ' ')"
su -s /bin/sh nobody -c 'mariadb -uroot -NBe "select 1"' >/dev/null 2>&1 && bad "unprivileged user can log in to MariaDB as root" || ok "MariaDB root login denied for unprivileged user"
echo "  info  mariadb databases: $(mariadb -NBe 'show databases' | tr '\n' ' ')"
ss -Hltn | awk '{print $4}' | sort | tr '\n' ' ' | sed 's/^/  info  listeners: /'; echo
ss -Hltn | grep -E ':3306\b' | grep -q -v '127.0.0.1' && bad "3306 is not loopback only" || ok "3306 loopback only"
for u in $(systemctl list-unit-files --no-legend 'php*-fpm.service' | awk '{print $1}'); do
  [ "$(systemctl is-enabled $u)" != enabled ] && [ "$(systemctl is-active $u)" != active ] && ok "$u disabled and stopped (per-site masters, ADR-0015)" || bad "$u"
done
php -v | head -1 | sed 's/^/  info  /'; php-fpm* -v 2>/dev/null | head -1
getent passwd hostashi | sed 's/^/  info  /'
stat -c '  info  %n %a %U:%G' /etc/hostashi /var/lib/hostashi /var/log/hostashi /var/lib/hostashi-installer
for u in hostashi-agent hostashi; do
  [ "$(systemctl is-active $u)" = active ] && ok "$u.service active as $(ps -o user= -p $(systemctl show -p MainPID --value $u))" || bad "$u.service"
done
systemd-analyze security hostashi.service --no-pager 2>/dev/null | tail -1 | sed 's/^/  info  /'
systemd-analyze verify /etc/systemd/system/hostashi.service /etc/systemd/system/hostashi-agent.service 2>&1 | sed 's/^/  info  verify: /'
echo "  info  allowed origins:"; unattended-upgrade --dry-run -d 2>&1 | grep -i -E "Allowed origins|Checking:|nginx" | head -8 | sed 's/^/        /'
apt-config dump | grep -E 'Periodic::(Update|Unatt)' | sed 's/^/  info  /'
nginx -t 2>&1 | sed 's/^/  info  /'
exit $rc
