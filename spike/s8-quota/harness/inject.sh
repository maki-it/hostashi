#!/bin/bash
# usage: inject.sh image script [extra files...]  -> installs /opt/s8/run.sh + s8.service in image
img=$1; script=$2; shift 2
m=$(mktemp -d); mount -o loop $img $m
mkdir -p $m/opt/s8; cp $script $m/opt/s8/run.sh; chmod +x $m/opt/s8/run.sh
for f in "$@"; do cp $f $m/opt/s8/; done
cat > $m/etc/systemd/system/s8.service <<'U'
[Unit]
Description=s8 test runner
After=multi-user.target
[Service]
Type=oneshot
ExecStart=/opt/s8/run.sh
StandardOutput=journal+console
StandardError=journal+console
[Install]
WantedBy=multi-user.target
U
ln -sf /etc/systemd/system/s8.service $m/etc/systemd/system/multi-user.target.wants/s8.service
umount $m; rmdir $m
