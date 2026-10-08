#!/usr/bin/env bash
# Build iap-sink and install it on the Pi 3 over SSH. Runs on the Mac.
# Usage: [ACCESSORY_CERT_FILE=path/to/accessory-cert-N.p7b] sink/deploy.sh [host]   (default host: pi3-sink.local)
# The certificate is private. It goes to /etc/iap-sink on the Pi and never into git.
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
HOST=${1:-pi3-sink.local}
TARGET="${SSH_USER:-root}@$HOST"
SSH=(ssh -o StrictHostKeyChecking=accept-new)

"$HERE/build.sh"

"${SSH[@]}" "$TARGET" 'rm -rf /opt/iap-sink-src && mkdir -p /opt/iap-sink-src/files'
rsync -rt --exclude .DS_Store -e "ssh -o StrictHostKeyChecking=accept-new" \
  "$HERE/files/" "$TARGET:/opt/iap-sink-src/files/"
rsync -t -e "ssh -o StrictHostKeyChecking=accept-new" \
  "$HERE/out/iap-sink" "$TARGET:/opt/iap-sink-src/iap-sink"

if [ -n "${ACCESSORY_CERT_FILE:-}" ]; then
  "${SSH[@]}" "$TARGET" 'install -d -m 750 /etc/iap-sink && cat > /etc/iap-sink/accessory-cert.p7b && chmod 640 /etc/iap-sink/accessory-cert.p7b' \
    < "$ACCESSORY_CERT_FILE"
  echo "Certificate installed ($(wc -c < "$ACCESSORY_CERT_FILE" | tr -d ' ') bytes)."
fi

"${SSH[@]}" "$TARGET" 'bash -s' < "$HERE/install-on-pi.sh"
