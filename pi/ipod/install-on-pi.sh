#!/usr/bin/env bash
# Runs on the Pi 5, sent over SSH by deploy.sh. Installs the Bluetooth iPod. Safe to run again.
# It does not switch the mode: run "ipod-mode bt" for that.
set -euo pipefail

SRC=/opt/ipod-bridge-src

export DEBIAN_FRONTEND=noninteractive
dpkg -s bluez-alsa-utils > /dev/null 2>&1 || apt-get install -y -qq bluez-alsa-utils

install -m 755 "$SRC/ipod-bridge" /usr/local/bin/ipod-bridge
install -m 755 "$SRC/files/usr/local/sbin/ipod-gadget" /usr/local/sbin/ipod-gadget
install -m 755 "$SRC/files/usr/local/sbin/ipod-mode" /usr/local/sbin/ipod-mode
install -m 644 "$SRC"/files/etc/systemd/system/ipod-*.service /etc/systemd/system/
install -d /etc/systemd/system/bluealsa.service.d
install -m 644 "$SRC/files/etc/systemd/system/bluealsa.service.d/ipod.conf" /etc/systemd/system/bluealsa.service.d/ipod.conf
# Keep local edits of the settings file.
[ -e /etc/ipod-bridge.conf ] || install -m 644 "$SRC/files/etc/ipod-bridge.conf" /etc/ipod-bridge.conf

# The phone shows the Pi as a car audio device.
if ! grep -q '^Class = 0x240408' /etc/bluetooth/main.conf; then
  sed -i -E '/^\[General\]/,/^\[/ { /^#?Class *=/d }' /etc/bluetooth/main.conf
  sed -i '/^\[General\]/a Class = 0x240408' /etc/bluetooth/main.conf
  systemctl restart bluetooth.service
fi

systemctl daemon-reload
echo "installed"
