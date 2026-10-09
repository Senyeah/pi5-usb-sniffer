#!/usr/bin/env bash
# One-time Pi setup for the iAP USB relay. Safe to run again.
set -euo pipefail

SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
OPT=/opt/sniffer
STATE=/var/lib/sniffer

USB_PROXY_REPO=https://github.com/AristoChen/usb-proxy.git
USB_PROXY_COMMIT=a08301d21d6ba1036cddbcb1a4314578cbc4a274
RAW_GADGET_REPO=https://github.com/xairy/raw-gadget.git
RAW_GADGET_COMMIT=8c6de5448ef2b8e4fc37021208c86c3f4dd579dc

log() { printf '%s %s\n' "$(date -u +%FT%TZ)" "$*"; }

wait_for_network() {
  for _ in $(seq 60); do
    getent hosts deb.debian.org >/dev/null && getent hosts github.com >/dev/null && return 0
    sleep 5
  done
  log "no network after 5 minutes"
  return 1
}

wait_for_clock() {
  # apt rejects repository data if the clock is behind.
  for _ in $(seq 24); do
    [ "$(timedatectl show -p NTPSynchronized --value)" = yes ] && return 0
    sleep 5
  done
  log "clock not synchronised, continuing"
}

git_checkout() {
  local repo=$1 commit=$2 dir=$3
  [ -d "$dir/.git" ] || git clone --quiet "$repo" "$dir"
  git -C "$dir" fetch --quiet origin
  # -f also removes earlier patches, so they apply cleanly again.
  git -C "$dir" checkout --quiet -f "$commit"
}

ensure_config_txt() {
  local cfg=/boot/firmware/config.txt
  grep -q '^dtoverlay=dwc2,dr_mode=peripheral' "$cfg" && return 0
  printf '\n[all]\n# iAP USB relay: USB-C is a USB device. GPIO power gives no USB-PD data.\ndtoverlay=dwc2,dr_mode=peripheral\nusb_max_current_enable=1\n' >> "$cfg"
  log "added dwc2 peripheral mode to $cfg"
}

install_packages() {
  export DEBIAN_FRONTEND=noninteractive
  local opts=(-y -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold)
  # Kernel flavour from uname, e.g. "rpi-2712" for a Pi 5.
  local flavour
  flavour=$(uname -r | sed 's/.*+rpt-//')
  apt-get update
  apt-get "${opts[@]}" full-upgrade
  apt-get "${opts[@]}" install \
    git build-essential pkg-config dkms "linux-headers-$flavour" \
    libusb-1.0-0-dev libjsoncpp-dev tshark usbutils rsync
}

install_raw_gadget() {
  # Not in the Raspberry Pi kernel. DKMS rebuilds it on kernel upgrades.
  local ver="1.0+${RAW_GADGET_COMMIT:0:7}"
  local src="/usr/src/raw_gadget-$ver"
  git_checkout "$RAW_GADGET_REPO" "$RAW_GADGET_COMMIT" "$OPT/raw-gadget"
  mkdir -p "$src"
  cp "$OPT"/raw-gadget/raw_gadget/*.[ch] "$OPT"/raw-gadget/raw_gadget/Makefile "$src/"
  cat > "$src/dkms.conf" <<EOF
PACKAGE_NAME="raw_gadget"
PACKAGE_VERSION="$ver"
BUILT_MODULE_NAME[0]="raw_gadget"
DEST_MODULE_LOCATION[0]="/updates/dkms"
MAKE[0]="make -C \${kernel_source_dir} M=\${dkms_tree}/\${PACKAGE_NAME}/\${PACKAGE_VERSION}/build modules"
CLEAN="make -C \${kernel_source_dir} M=\${dkms_tree}/\${PACKAGE_NAME}/\${PACKAGE_VERSION}/build clean"
AUTOINSTALL="yes"
EOF
  dkms status -m raw_gadget -v "$ver" | grep -q . || dkms add -m raw_gadget -v "$ver"
  local kdir k
  for kdir in /lib/modules/*/build; do
    [ -e "$kdir" ] || continue
    k=$(basename "$(dirname "$kdir")")
    dkms install -m raw_gadget -v "$ver" -k "$k" || log "raw_gadget: dkms install returned an error for $k"
  done
  # dkms can return an error for "already installed", so check the files.
  compgen -G "/lib/modules/*/updates/dkms/raw_gadget.ko*" >/dev/null || { log "raw_gadget: no module built"; return 1; }
}

install_usb_proxy() {
  git_checkout "$USB_PROXY_REPO" "$USB_PROXY_COMMIT" "$OPT/usb-proxy"
  local p
  for p in "$SRC_DIR"/patches/usb-proxy-*.patch; do
    git -C "$OPT/usb-proxy" apply "$p"
    log "applied $(basename "$p")"
  done
  make -C "$OPT/usb-proxy" -j"$(nproc)"
}

configure_system() {
  cat > /etc/modules-load.d/sniffer.conf <<'EOF'
usbmon
raw_gadget
EOF
  # Only usb-proxy may talk to the iPhone. usbhid is built in, see the usb-proxy patch.
  cat > /etc/modprobe.d/sniffer-blacklist.conf <<'EOF'
blacklist ipheth
blacklist snd_usb_audio
blacklist cdc_ncm
blacklist cdc_ether
EOF
  systemctl mask usbmuxd.service
  install -m 755 "$SRC_DIR/capture.sh" /usr/local/bin/capture
  mkdir -p /root/sessions
}

record_versions() {
  {
    echo "setup_done_utc: $(date -u +%FT%TZ)"
    echo "kernel_at_setup: $(uname -r)"
    echo "usb_proxy_commit: $USB_PROXY_COMMIT"
    echo "raw_gadget_commit: $RAW_GADGET_COMMIT"
    dkms status raw_gadget
  } > "$STATE/versions.txt"
}

main() {
  [ "$(id -u)" = 0 ] || { echo "Run as root."; exit 1; }
  mkdir -p "$OPT" "$STATE"
  log "setup start"
  wait_for_network
  wait_for_clock
  ensure_config_txt
  install_packages
  install_raw_gadget
  install_usb_proxy
  configure_system
  record_versions
  touch "$STATE/setup-done"
  if [ -n "${INVOCATION_ID:-}" ]; then
    log "setup done, rebooting into the upgraded kernel"
    systemctl --no-block reboot
  else
    log "setup done. Reboot to use the upgraded kernel."
  fi
}

main "$@"
