#!/usr/bin/env bash
# Build ipod-bridge and install the Bluetooth iPod on the Pi 5 over SSH. Runs on the Mac.
# Usage: ./deploy.sh [host]   (default host: pi5-sniffer.local). Then on the Pi: ipod-mode bt
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
HOST=${1:-pi5-sniffer.local}
TARGET="${SSH_USER:-root}@$HOST"
SSH=(ssh -o StrictHostKeyChecking=accept-new)
RSYNC_SSH="ssh -o StrictHostKeyChecking=accept-new"

"$HERE/build.sh"

"${SSH[@]}" "$TARGET" 'rm -rf /opt/ipod-bridge-src && mkdir -p /opt/ipod-bridge-src/files'
rsync -rt --exclude .DS_Store -e "$RSYNC_SSH" "$HERE/files/" "$TARGET:/opt/ipod-bridge-src/files/"
rsync -t -e "$RSYNC_SSH" "$HERE/out/ipod-bridge" "$TARGET:/opt/ipod-bridge-src/ipod-bridge"
rsync -rt -e "$RSYNC_SSH" "$HERE/uac1-fs/" "$TARGET:/opt/ipod-bridge-src/uac1-fs/"
"${SSH[@]}" "$TARGET" 'bash -s' < "$HERE/install-uac1-fs.sh"
"${SSH[@]}" "$TARGET" 'bash -s' < "$HERE/install-on-pi.sh"
