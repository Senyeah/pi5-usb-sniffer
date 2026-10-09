#!/usr/bin/env bash
# Measures the Pi 5's audio delay: Bluetooth packet in (btmon on the Pi 5) to USB packet out (usbmon on the
# Pi 3 sink). Runs on the Mac. Needs the Pi 5 on the Pi 3 by USB with an iap-sink session, and the phone
# connected to the Pi 5. It starts the phone's player over AVRCP and pauses it at the end.
# Usage: ./latency-test.sh [seconds]   (default 60). Output: out/latency-<time>/
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SECS=${1:-60}
P5=${P5:-root@pi5-sniffer.local}
P3=${P3:-root@pi3-sink.local}
SSH=(ssh -o BatchMode=yes -o ConnectTimeout=8)
OUT="$HERE/out/latency-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$OUT"

"${SSH[@]}" "$P3" 'modprobe usbmon && { command -v tcpdump > /dev/null || DEBIAN_FRONTEND=noninteractive apt-get install -y -qq tcpdump; }'
bus=$("${SSH[@]}" "$P3" "lsusb -d 05ac: | awk '{print \$2+0; exit}'")
[ -n "$bus" ] || { echo "No Apple device on the Pi 3. Connect the Pi 5 to the Pi 3 and run ipod-mode bt."; exit 1; }
"${SSH[@]}" "$P3" 'iap-sink ctl status' | grep -q '"init_done": true' || { echo "The Pi 3 has no iAP session yet."; exit 1; }

echo "Starting the phone's player, then capturing $SECS s (USB bus $bus on the Pi 3)."
"${SSH[@]}" "$P5" 'ipod-bridge ctl play'
sleep 4
"${SSH[@]}" "$P3" "rm -f /tmp/lat-usb.pcap; timeout -s INT $SECS tcpdump -q -i usbmon$bus -s 0 -w /tmp/lat-usb.pcap 2> /dev/null; true" &
"${SSH[@]}" "$P5" "rm -f /tmp/lat-bt.btsnoop; timeout -s INT $SECS btmon -w /tmp/lat-bt.btsnoop > /dev/null; true" &
wait
"${SSH[@]}" "$P5" 'ipod-bridge ctl pause' > /dev/null || true

scp -q -o BatchMode=yes "$P3:/tmp/lat-usb.pcap" "$OUT/usb.pcap"
scp -q -o BatchMode=yes "$P5:/tmp/lat-bt.btsnoop" "$OUT/bt.btsnoop"
trace=$("${SSH[@]}" "$P5" 'ls -t /var/lib/ipod-bridge/traces/session-*.jsonl | head -n 1')
scp -q -o BatchMode=yes "$P5:$trace" "$OUT/bridge-trace.jsonl"
"${SSH[@]}" "$P5" 'grep -E "^IPOD_(AUDIO|EXTRA|DELAY)" /etc/ipod-bridge.conf; journalctl -u ipod-audio -n 20 --no-pager -o cat' > "$OUT/pi5-audio.txt" || true
"${SSH[@]}" "$P3" 'rm -f /tmp/lat-usb.pcap'
"${SSH[@]}" "$P5" 'rm -f /tmp/lat-bt.btsnoop'

uv run "$HERE/latency/analyze.py" "$OUT" | tee "$OUT/result.txt"
