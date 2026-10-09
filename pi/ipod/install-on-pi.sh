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
install -m 755 "$SRC/files/usr/local/sbin/ipod-audio" /usr/local/sbin/ipod-audio
install -d /etc/alsa/conf.d
install -m 644 "$SRC/files/etc/alsa/conf.d/60-ipod-gain.conf" /etc/alsa/conf.d/60-ipod-gain.conf
install -m 644 "$SRC"/files/etc/systemd/system/ipod-*.service /etc/systemd/system/
install -d /etc/systemd/system/bluealsa.service.d
install -m 644 "$SRC/files/etc/systemd/system/bluealsa.service.d/ipod.conf" /etc/systemd/system/bluealsa.service.d/ipod.conf
# Keep local edits of the settings file. Settings that came later are added with their comments.
if [ -e /etc/ipod-bridge.conf ]; then
  for v in IPOD_PAIR_AFTER IPOD_AUDIO_GAIN_DB IPOD_AUDIO_LATENCY_MS IPOD_EXTRA_DELAY_MS; do
    grep -q "^#* *$v=" /etc/ipod-bridge.conf ||
      awk -v v="$v" '/^#/ { c = c $0 "\n"; next } $0 ~ "^" v "=" { printf "\n%s%s\n", c, $0 } { c = "" }' \
        "$SRC/files/etc/ipod-bridge.conf" >> /etc/ipod-bridge.conf
  done
else
  install -m 644 "$SRC/files/etc/ipod-bridge.conf" /etc/ipod-bridge.conf
fi

# The phone shows the Pi as a car audio device: major class audio/video, minor class 8 (car audio). The older
# 0x240408 was minor class 2 (hands-free). iOS uses the class for the device type ("Car Stereo", no
# headphone volume limit), maybe only after a new pairing.
if ! grep -q '^Class = 0x240420' /etc/bluetooth/main.conf; then
  sed -i -E '/^\[General\]/,/^\[/ { /^#?Class *=/d }' /etc/bluetooth/main.conf
  sed -i '/^\[General\]/a Class = 0x240420' /etc/bluetooth/main.conf
  systemctl restart bluetooth.service
fi

systemctl daemon-reload
echo "installed"
