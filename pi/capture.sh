#!/usr/bin/env bash
# Relay session: usb-proxy between the iPhone and the stereo, usbmon to pcapng.
set -euo pipefail

UDC=1000480000.usb
APPLE_VID=05ac
PROXY=/opt/sniffer/usb-proxy/usb-proxy
SESSIONS=${SESSIONS:-/root/sessions}
CURRENT="$SESSIONS/current"
PROXY_VERBOSE=${PROXY_VERBOSE:-1}
PROXY_ARGS=${PROXY_ARGS:---iso_batch_size=4}  # 4 measured best for audio drops (08/10/2026)
# Ignore the Apple charge request (0x40/0x40): the iPhone then trips the Pi's 1.6 A USB-A limit.
# Stall "set USB mode" (0xC0/0x52): the iPhone re-enumerates, and the relay loops.
INJECTION_FILE=${INJECTION_FILE-/opt/sniffer/pi/rules/apple-vendor-rules.json}
# usb-proxy reads config.json from its working directory. capture writes one into RUN_DIR from
# rules/config.json, with reset_device_before_proxy set from PROXY_RESET.
RULES_DIR=/opt/sniffer/pi/rules
RUN_DIR=/run/sniffer
# Default on: a reset clears stale iAP state on the device when the stereo reconnects.
# Set PROXY_RESET=0 for a Mac host, whose USB mode change makes a reset re-enumerate the device.
PROXY_RESET=${PROXY_RESET:-1}

usage() {
  cat <<EOF
Usage:
  capture start [label]   Wait for the iPhone, then start the relay and the capture.
  capture mark <text>     Add a timed note to the running session.
  capture stop            Stop the relay and the capture.
  capture status          Show the relay, UDC and iPhone state.

Environment:
  PROXY_VERBOSE=0..3      Number of -v flags for usb-proxy (default 1).
  PROXY_ARGS="..."        Extra usb-proxy options, e.g. --iso_batch_size=4.
  INJECTION_FILE=FILE     usb-proxy rules (default: Apple charge and USB mode requests). Empty to disable.
  PROXY_RESET=1           Let usb-proxy reset the iPhone at start (default: no reset).
EOF
}

now_utc() { date -u +%Y-%m-%dT%H:%M:%S.%3NZ; }
now_local() { date +'%d/%m/%Y %H:%M:%S %Z'; }
udc_attr() { cat "/sys/class/udc/$UDC/$1" 2>/dev/null || echo n/a; }

# Prints "busnum devnum product speed" for the first Apple device.
find_iphone() {
  local d
  for d in /sys/bus/usb/devices/*; do
    [ -f "$d/idVendor" ] && [ "$(cat "$d/idVendor")" = "$APPLE_VID" ] || continue
    echo "$(cat "$d/busnum") $(cat "$d/devnum") $(cat "$d/idProduct") $(cat "$d/speed")"
    return 0
  done
  return 1
}

write_metadata() {
  local dir=$1 bus=$2 dev=$3 pid=$4 speed=$5
  {
    echo "started_utc: $(now_utc)"
    echo "started_local: $(now_local)"
    echo "hostname: $(hostname)"
    echo "kernel: $(uname -r)"
    echo "os: $(. /etc/os-release && echo "$PRETTY_NAME")"
    echo "usb_proxy_commit: $(git -C /opt/sniffer/usb-proxy rev-parse HEAD 2>/dev/null || echo unknown)"
    echo "raw_gadget: $(dkms status raw_gadget 2>/dev/null | head -1)"
    echo "iphone: $APPLE_VID:$pid bus=$bus dev=$dev speed_mbps=$speed"
    echo "proxy_verbose: $PROXY_VERBOSE"
    echo "proxy_args: $PROXY_ARGS"
    echo "injection_file: ${INJECTION_FILE:-none}"
    echo "throttled: $(vcgencmd get_throttled 2>/dev/null || echo n/a)"
  } > "$dir/session.txt"
  lsusb -v -d "$APPLE_VID:$pid" > "$dir/iphone-lsusb-v.txt" 2>&1 || true
  lsusb -t > "$dir/lsusb-t.txt" 2>&1 || true
}

cmd_start() {
  local label=${1:-}
  if systemctl is-active --quiet sniffer-proxy sniffer-dumpcap sniffer-udcwatch; then
    echo "A session is still running ($(readlink -f "$CURRENT" 2>/dev/null || echo unknown)). Run: capture stop"
    exit 1
  fi
  modprobe usbmon
  modprobe raw_gadget
  [ -e "/sys/class/udc/$UDC" ] || { echo "UDC $UDC not found. Check dtoverlay=dwc2,dr_mode=peripheral in config.txt."; exit 1; }

  echo "Waiting for the iPhone on a USB-A port..."
  local info bus dev pid speed
  until info=$(find_iphone); do sleep 1; done
  read -r bus dev pid speed <<<"$info"
  [ "$speed" = 480 ] || echo "Warning: the iPhone runs at $speed Mbit/s. Use a black USB 2.0 port for 480 Mbit/s."

  local dir
  dir="$SESSIONS/$(date +%Y%m%d-%H%M%S)${label:+-$label}"
  mkdir -p "$dir"
  ln -sfn "$dir" "$CURRENT"
  # Before the capture starts, so lsusb's own requests stay out of it.
  write_metadata "$dir" "$bus" "$dev" "$pid" "$speed"

  systemd-run --quiet --collect -p TimeoutStopSec=5 --unit=sniffer-dumpcap \
    dumpcap -q -i "usbmon$bus" -b filesize:500000 -w "$dir/usb.pcapng"
  systemd-run --quiet --collect -p TimeoutStopSec=5 --unit=sniffer-udcwatch \
    /usr/local/bin/capture _udcwatch "$dir/udc.log"
  sleep 1
  # stdbuf: without line buffering, the log loses the last requests before a failure.
  mkdir -p "$RUN_DIR"  # systemd needs WorkingDirectory to exist before the unit starts
  systemd-run --quiet --collect -p TimeoutStopSec=5 --unit=sniffer-proxy \
    -p StandardOutput="append:$dir/proxy.log" -p StandardError="append:$dir/proxy.log" \
    --setenv=PROXY_VERBOSE="$PROXY_VERBOSE" --setenv=INJECTION_FILE="$INJECTION_FILE" \
    --setenv=PROXY_ARGS="$PROXY_ARGS" --setenv=SESSIONS="$SESSIONS" --setenv=PROXY_RESET="$PROXY_RESET" \
    -p WorkingDirectory="$RUN_DIR" \
    stdbuf -oL -eL /usr/local/bin/capture _proxyloop

  cmd_mark "session start: iPhone $APPLE_VID:$pid, bus $bus, $speed Mbit/s"
  echo "Session: $dir"
  echo "Connect the Pi's USB-C cable to the stereo now."
}

cmd_mark() {
  [ -L "$CURRENT" ] || { echo "No session is running."; exit 1; }
  printf '%s\t%s\t%s\n' "$(now_utc)" "$(now_local)" "$*" >> "$CURRENT/marks.tsv"
}

cmd_stop() {
  local dir=""
  if [ -L "$CURRENT" ]; then
    dir=$(readlink -f "$CURRENT")
    cmd_mark "session stop"
  fi
  systemctl stop sniffer-proxy sniffer-udcwatch 2>/dev/null || true
  sleep 1
  # SIGTERM lets dumpcap close the file correctly.
  systemctl stop sniffer-dumpcap 2>/dev/null || true
  rm -f "$CURRENT"
  if [ -n "$dir" ]; then
    du -sh "$dir"
    echo "Copy to the Mac with: rsync -av root@$(hostname).local:$dir ./sessions/"
  fi
}

cmd_status() {
  local u
  for u in sniffer-proxy sniffer-dumpcap sniffer-udcwatch; do
    printf '%-17s %s\n' "$u" "$(systemctl is-active "$u" 2>/dev/null || true)"
  done
  echo "UDC: state=$(udc_attr state) speed=$(udc_attr current_speed)"
  local info
  if info=$(find_iphone); then echo "iPhone (bus dev pid Mbit/s): $info"; else echo "iPhone: not connected"; fi
  [ -L "$CURRENT" ] && echo "Session: $(readlink -f "$CURRENT")"
  return 0
}

# A host's Apple "set USB mode" request (0xC0/0x52) makes the iPhone re-enumerate.
# usb-proxy then hangs, so restart it on the new device. The host sees an unplug and re-plug.
cmd_proxyloop() {
  local verbose=() injection=() info bus dev pid speed cur child
  for _ in $(seq "$PROXY_VERBOSE"); do verbose+=(-v); done
  [ -n "$INJECTION_FILE" ] && injection=(--injection_file="$INJECTION_FILE")
  mkdir -p "$RUN_DIR"
  local reset=false; [ "$PROXY_RESET" = 1 ] && reset=true
  sed -E "s/\"reset_device_before_proxy\": *(true|false)/\"reset_device_before_proxy\": $reset/" "$RULES_DIR/config.json" > "$RUN_DIR/config.json"
  injection+=(--enable_customized_config)
  while :; do
    until info=$(find_iphone); do sleep 0.2; done
    read -r bus dev pid speed <<<"$info"
    echo "[capture] $(now_utc) start usb-proxy: iPhone $APPLE_VID:$pid bus $bus dev $dev"
    # setsid: on iPhone disconnect, usb-proxy does kill(0, SIGINT), which would also stop this loop.
    # The perl stage prefixes each line with a UTC timestamp, to line up with udc.log and the pcap.
    # shellcheck disable=SC2086 # PROXY_ARGS is a list of options.
    setsid "$PROXY" "${verbose[@]}" --device="$UDC" --driver="$UDC" \
      --vendor_id="$APPLE_VID" --product_id="$pid" "${injection[@]}" $PROXY_ARGS \
      > >(perl -MTime::HiRes=time -MPOSIX=strftime -pe 'BEGIN { $| = 1 } $t=time; $_=strftime("%H:%M:%S", gmtime($t)).sprintf(".%03dZ ", ($t-int($t))*1000).$_') 2>&1 &
    child=$!
    while kill -0 "$child" 2>/dev/null; do
      cur=$( { find_iphone || true; } | awk '{print $2}')
      # devnum reads 0 for a moment during usb-proxy's own reset. Only a new number means re-enumeration.
      if [ -n "$cur" ] && [ "$cur" != 0 ] && [ "$cur" != "$dev" ]; then
        echo "[capture] $(now_utc) iPhone re-enumerated (dev $dev -> $cur), restarting usb-proxy"
        [ -L "$CURRENT" ] && cmd_mark "iPhone re-enumerated (dev $dev -> $cur), relay restarted"
        kill -INT "$child" 2>/dev/null || true
        sleep 1
        kill -KILL "$child" 2>/dev/null || true
        break
      fi
      sleep 0.2
    done
    wait "$child" 2>/dev/null || echo "[capture] $(now_utc) usb-proxy exited with status $?"
    sleep 0.5
  done
}

# Logs UDC state and speed changes, e.g. "configured" and "full-speed" (risk R11).
cmd_udcwatch() {
  local log=$1 last="" s
  while :; do
    s="state=$(udc_attr state) speed=$(udc_attr current_speed)"
    if [ "$s" != "$last" ]; then
      echo "$(now_utc) $s" >> "$log"
      last=$s
    fi
    sleep 0.2
  done
}

[ "$(id -u)" = 0 ] || { echo "Run as root."; exit 1; }
case "${1:-}" in
  start) shift; cmd_start "$@" ;;
  mark) shift; cmd_mark "$@" ;;
  stop) cmd_stop ;;
  status) cmd_status ;;
  _udcwatch) shift; cmd_udcwatch "$@" ;;
  _proxyloop) cmd_proxyloop ;;
  *) usage; exit 1 ;;
esac
