#!/usr/bin/env bash
# Runs on the Pi 3, sent over SSH by deploy.sh. Installs iap-sink and its services. Safe to run again.
set -euo pipefail

SRC=/opt/iap-sink-src

install -d -m 755 /usr/local/bin /usr/local/sbin /usr/local/lib/iap-sink
install -m 755 "$SRC/iap-sink" /usr/local/bin/iap-sink
install -m 755 "$SRC/files/usr/local/lib/iap-sink/audio-route" /usr/local/lib/iap-sink/audio-route
install -m 755 "$SRC/files/usr/local/sbin/sink-speed" /usr/local/sbin/sink-speed
install -m 755 "$SRC/files/usr/local/bin/stereo" /usr/local/bin/stereo
install -m 644 "$SRC"/files/etc/systemd/system/*.service /etc/systemd/system/
install -m 644 "$SRC/files/etc/modprobe.d/iap-sink.conf" /etc/modprobe.d/iap-sink.conf
install -d -m 750 /etc/iap-sink
# Keep local edits of the settings file.
[ -e /etc/iap-sink/sink.conf ] || install -m 644 "$SRC/files/etc/iap-sink/sink.conf" /etc/iap-sink/sink.conf

systemctl daemon-reload
systemctl enable iap-sink.service sink-audio.service
systemctl restart iap-sink.service sink-audio.service
systemctl --no-pager --lines=0 status iap-sink.service sink-audio.service | grep -E "●|Active:" || true
