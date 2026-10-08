#!/usr/bin/env bash
# Customise a freshly written Raspberry Pi OS Lite (Trixie, cloud-init) boot partition for the Pi 3 sink. Runs on the Mac.
# Usage: WIFI_SSID=... WIFI_PSK_REF=op://<vault>/<item>/<field> SSH_KEY_REF=op://<vault>/<item>/<field> \
#   sink/prepare-sdcard.sh [/Volumes/bootfs]
# Same Wi-Fi, users and SSH rules as pi/prepare-sdcard.sh.
set -euo pipefail

BOOTFS=${1:-/Volumes/bootfs}
PI_HOSTNAME=${PI_HOSTNAME:-pi3-sink}
PI_USER=${PI_USER:-jack}
PI_TIMEZONE=${PI_TIMEZONE:-Pacific/Auckland}
PI_COUNTRY=${PI_COUNTRY:-NZ}
WIFI_SSID=${WIFI_SSID:?Set WIFI_SSID to the Wi-Fi network name.}
# "psk" is WPA2. The Pi 3 B has 2.4 GHz Wi-Fi only.
WIFI_KEY_MGMT=${WIFI_KEY_MGMT:-psk}
WIFI_PSK_REF=${WIFI_PSK_REF:?Set WIFI_PSK_REF to the 1Password secret reference of the Wi-Fi password.}
SSH_KEY_REF=${SSH_KEY_REF:?Set SSH_KEY_REF to the 1Password secret reference of the SSH public key.}

for f in config.txt cmdline.txt user-data network-config meta-data; do
  [ -f "$BOOTFS/$f" ] || { echo "$BOOTFS/$f is missing. This is not a cloud-init Raspberry Pi OS boot partition."; exit 1; }
done

# Do not overwrite the card of another Pi. A fresh card has the host name "raspberrypi".
cur_host=$(sed -n 's/^hostname: *//p' "$BOOTFS/user-data" | head -n 1)
case $cur_host in
  raspberrypi | "$PI_HOSTNAME") ;;
  *) echo "The card has host name \"$cur_host\", not a fresh card. Set FORCE=1 to write to it anyway."; [ "${FORCE:-}" = 1 ] || exit 1 ;;
esac

ssh_key=$(op read "$SSH_KEY_REF")
case $ssh_key in ssh-*) ;; *) echo "The SSH public key has an unexpected format."; exit 1 ;; esac

# A JSON string is also a valid YAML double-quoted string, so jq escapes any character.
wifi_psk_json=$(op read -n "$WIFI_PSK_REF" | jq -Rs .)
psk_len=$(jq -r 'length' <<<"$wifi_psk_json")
[ "$psk_len" -ge 8 ] && [ "$psk_len" -le 63 ] || { echo "The Wi-Fi password length ($psk_len) is not valid for WPA2 (8-63)."; exit 1; }
wifi_ssid_json=$(jq -Rn --arg s "$WIFI_SSID" '$s')

cat > "$BOOTFS/network-config" <<EOF
# Written by sink/prepare-sdcard.sh. cloud-init applies this on first boot only.
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
# Written by sink/prepare-sdcard.sh.
hostname: $PI_HOSTNAME
manage_etc_hosts: true
timezone: $PI_TIMEZONE

users:
  - name: $PI_USER
    groups: [users, adm, sudo, video, plugdev, dialout, audio]
    shell: /bin/bash
    lock_passwd: true
    sudo: "ALL=(ALL) NOPASSWD:ALL"
    ssh_authorized_keys:
      - $ssh_key

disable_root: false
ssh_pwauth: false

write_files:
  - path: /etc/ssh/sshd_config.d/10-sink.conf
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
EOF

# cmdline.txt must stay one line.
add_arg() {
  grep -q -- "$1" "$BOOTFS/cmdline.txt" || perl -0pi -e "s/\\s*\\z/ $2\\n/" "$BOOTFS/cmdline.txt"
}
add_arg 'ieee80211_regdom' "cfg80211.ieee80211_regdom=$PI_COUNTRY"
# Full speed on the host port, like the car stereo. "sink-speed high" undoes this.
add_arg 'dwc_otg.speed' 'dwc_otg.speed=1'
# HID_QUIRK_NO_INIT_REPORTS (0x20000000): the stereo sends no GET_REPORT after SET_CONFIGURATION.
add_arg 'usbhid.quirks' 'usbhid.quirks=0x05ac:0x12a8:0x20000000,0x05ac:0x12ab:0x20000000'

# cloud-init runs users, keys and runcmd once per instance ID. A card that booted before keeps its old ID and skips them.
if grep -q '^instance_id:' "$BOOTFS/meta-data"; then
  sed -i.bak "s/^instance_id:.*/instance_id: $PI_HOSTNAME-$(date +%s)/" "$BOOTFS/meta-data" && rm -f "$BOOTFS/meta-data.bak"
else
  printf 'instance_id: %s-%s\n' "$PI_HOSTNAME" "$(date +%s)" >> "$BOOTFS/meta-data"
fi

touch "$BOOTFS/ssh"
# macOS stores extended attributes as ._ files on FAT.
find "$BOOTFS" -maxdepth 3 -name '.Spotlight-V100' -prune -o -name '._*' -type f -delete 2>/dev/null || true

sync
echo "Boot partition ready: host $PI_HOSTNAME, Wi-Fi \"$WIFI_SSID\" ($PI_COUNTRY), users root + $PI_USER (key only)."
