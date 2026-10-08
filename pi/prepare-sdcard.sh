#!/usr/bin/env bash
# Customise a freshly written Raspberry Pi OS (Trixie, cloud-init) boot partition. Runs on the Mac.
# Usage: WIFI_SSID=... WIFI_PSK_REF=op://<vault>/<item>/<field> SSH_KEY_REF=op://<vault>/<item>/<field> \
#   pi/prepare-sdcard.sh [/Volumes/bootfs]
set -euo pipefail

BOOTFS=${1:-/Volumes/bootfs}
PI_HOSTNAME=${PI_HOSTNAME:-pi5-sniffer}
PI_USER=${PI_USER:-jack}
PI_TIMEZONE=${PI_TIMEZONE:-Pacific/Auckland}
PI_COUNTRY=${PI_COUNTRY:-NZ}
WIFI_SSID=${WIFI_SSID:?Set WIFI_SSID to the Wi-Fi network name.}
# "psk" is WPA2. Use "sae" for a WPA3-only network.
WIFI_KEY_MGMT=${WIFI_KEY_MGMT:-psk}
WIFI_PSK_REF=${WIFI_PSK_REF:?Set WIFI_PSK_REF to the 1Password secret reference of the Wi-Fi password.}
SSH_KEY_REF=${SSH_KEY_REF:?Set SSH_KEY_REF to the 1Password secret reference of the SSH public key.}

HERE="$(cd "$(dirname "$0")" && pwd)"

for f in config.txt cmdline.txt user-data network-config meta-data; do
  [ -f "$BOOTFS/$f" ] || { echo "$BOOTFS/$f is missing. This is not a cloud-init Raspberry Pi OS boot partition."; exit 1; }
done

ssh_key=$(op read "$SSH_KEY_REF")
case $ssh_key in ssh-*) ;; *) echo "The SSH public key has an unexpected format."; exit 1 ;; esac

# A JSON string is also a valid YAML double-quoted string, so jq escapes any character.
wifi_psk_json=$(op read -n "$WIFI_PSK_REF" | jq -Rs .)
psk_len=$(jq -r 'length' <<<"$wifi_psk_json")
[ "$psk_len" -ge 8 ] && [ "$psk_len" -le 63 ] || { echo "The Wi-Fi password length ($psk_len) is not valid for WPA2 (8-63)."; exit 1; }
wifi_ssid_json=$(jq -Rn --arg s "$WIFI_SSID" '$s')

cat > "$BOOTFS/network-config" <<EOF
# Written by pi/prepare-sdcard.sh. cloud-init applies this on first boot only.
network:
  version: 2
  renderer: NetworkManager
  wifis:
    wlan0:
      dhcp4: true
      optional: true
      regulatory-domain: $PI_COUNTRY
      access-points:
        $wifi_ssid_json:
          auth:
            key-management: $WIFI_KEY_MGMT
            password: $wifi_psk_json
EOF
unset wifi_psk_json

cat > "$BOOTFS/user-data" <<EOF
#cloud-config
# Written by pi/prepare-sdcard.sh.
hostname: $PI_HOSTNAME
manage_etc_hosts: true
timezone: $PI_TIMEZONE

users:
  - name: $PI_USER
    groups: [users, adm, sudo, video, plugdev, dialout]
    shell: /bin/bash
    lock_passwd: true
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    ssh_authorized_keys:
      - $ssh_key

disable_root: false
ssh_pwauth: false

write_files:
  - path: /etc/ssh/sshd_config.d/10-sniffer.conf
    permissions: "0644"
    content: |
      PermitRootLogin prohibit-password
      PasswordAuthentication no

# The image ships with Wi-Fi soft-blocked until a country is set.
bootcmd:
  - |
    rfkill unblock wifi || true
    for f in /var/lib/systemd/rfkill/*:wlan; do [ -e "\$f" ] && echo 0 > "\$f"; done

runcmd:
  - [install, -d, -m, "0700", /root/.ssh]
  - [sh, -c, "echo '$ssh_key' > /root/.ssh/authorized_keys && chmod 600 /root/.ssh/authorized_keys"]
  - [sh, -c, "raspi-config nonint do_wifi_country $PI_COUNTRY || true"]
  - [systemctl, enable, --now, ssh]
  - [mkdir, -p, /opt/sniffer]
  - [cp, -rT, /boot/firmware/sniffer, /opt/sniffer/pi]
  - [chmod, "755", /opt/sniffer/pi/setup.sh, /opt/sniffer/pi/capture.sh]
  - [cp, /opt/sniffer/pi/sniffer-setup.service, /etc/systemd/system/]
  - [systemctl, daemon-reload]
  - [systemctl, enable, sniffer-setup.service]
  - [systemctl, start, --no-block, sniffer-setup.service]
EOF

if ! grep -q '^dtoverlay=dwc2,dr_mode=peripheral' "$BOOTFS/config.txt"; then
  printf '\n[all]\n# iAP USB relay: USB-C is a USB device. GPIO power gives no USB-PD data.\ndtoverlay=dwc2,dr_mode=peripheral\nusb_max_current_enable=1\n' >> "$BOOTFS/config.txt"
fi

# cmdline.txt must stay one line.
grep -q 'ieee80211_regdom' "$BOOTFS/cmdline.txt" ||
  perl -0pi -e "s/\\s*\\z/ cfg80211.ieee80211_regdom=$PI_COUNTRY\\n/" "$BOOTFS/cmdline.txt"
# Keep the kernel's HID driver off the iPhone (12a8) and iPad (12ab): HID_QUIRK_IGNORE = 0x4.
grep -q 'usbhid.quirks' "$BOOTFS/cmdline.txt" ||
  perl -0pi -e 's/\s*\z/ usbhid.quirks=0x05ac:0x12a8:0x4,0x05ac:0x12ab:0x4\n/' "$BOOTFS/cmdline.txt"

touch "$BOOTFS/ssh"

rm -rf "$BOOTFS/sniffer"
mkdir -p "$BOOTFS/sniffer/patches" "$BOOTFS/sniffer/rules"
cp "$HERE/setup.sh" "$HERE/capture.sh" "$HERE/sniffer-setup.service" "$BOOTFS/sniffer/"
cp "$HERE"/patches/*.patch "$BOOTFS/sniffer/patches/"
cp "$HERE"/rules/*.json "$BOOTFS/sniffer/rules/"
# macOS stores extended attributes as ._ files on FAT.
find "$BOOTFS" -maxdepth 3 -name '.Spotlight-V100' -prune -o -name '._*' -type f -delete 2>/dev/null || true

sync
echo "Boot partition ready: host $PI_HOSTNAME, Wi-Fi \"$WIFI_SSID\" ($PI_COUNTRY), users root + $PI_USER (key only)."
