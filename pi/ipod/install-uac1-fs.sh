#!/usr/bin/env bash
# Runs on the Pi 5, sent over SSH by deploy.sh. Builds the USB audio gadget function with the full-speed fix (DKMS).
# The kernel's f_uac1 uses bInterval 4 at full speed. A full-speed host (the car stereo) then gets 0 or 200 bytes per frame.
# Sources come from the Raspberry Pi kernel at a pinned commit, checked by SHA-256. Safe to run again.
set -euo pipefail

SHA=4103a989a46196239b830bd07693277e42b8f992
VER="1.0+${SHA:0:7}"
SRC=/usr/src/usb_f_uac1_fs-$VER
HERE=/opt/ipod-bridge-src/uac1-fs
BASE=https://raw.githubusercontent.com/raspberrypi/linux/$SHA/drivers/usb/gadget/function

if dkms status usb_f_uac1_fs/"$VER" 2> /dev/null | grep -q installed && modinfo -n usb_f_uac1 | grep -q /updates/dkms; then
  echo "usb_f_uac1_fs $VER is installed already"
  exit 0
fi

mkdir -p "$SRC"
for f in f_uac1.c u_audio.c u_audio.h u_uac1.h uac_common.h; do
  curl -fsSL -o "$SRC/$f" "$BASE/$f"
done
(cd "$SRC" && sha256sum -c --quiet <<'SUMS'
9d6ea3b079a097f574f0fba1ccec8474b823e7ce7b29b548c45642d313515968  f_uac1.c
effa3bb5414e660532c21f4e44082c2b9311054f401760be0ef677671c4a2c5b  u_audio.c
a40d04b8e8d9573303af8d1b0dbbb13c141f15b0b64bfd3666273cbd60293c11  u_audio.h
4f86f4c56d058c5ab46d52fbef30ecd19bf514aaba9c6abc6050814fb56ebe4b  u_uac1.h
0d268a8c31c61302bfdf2a9708b657a097371d22c0ec3aad3d2fc03f6bf2fab4  uac_common.h
SUMS
) || { echo "The downloaded kernel sources do not match the pinned checksums." >&2; exit 1; }

(cd "$SRC" && patch -p1 --forward --silent < "$HERE/f_uac1-fullspeed.patch") || true
grep -q fs_as_in_ep_desc "$SRC/f_uac1.c" || { echo "The full-speed patch is not in f_uac1.c" >&2; exit 1; }
install -m 644 "$HERE/Makefile" "$HERE/dkms.conf" "$SRC/"

for old in $(dkms status usb_f_uac1_fs 2> /dev/null | sed -n 's|^usb_f_uac1_fs[/,] *\([^,:]*\).*|\1|p' | sort -u); do
  [ "$old" = "$VER" ] || dkms remove usb_f_uac1_fs/"$old" --all || true
done
dkms status usb_f_uac1_fs/"$VER" 2> /dev/null | grep -q . || dkms add -m usb_f_uac1_fs -v "$VER"
dkms install -m usb_f_uac1_fs -v "$VER" --force
depmod -a
echo "installed: $(modinfo -n usb_f_uac1)"
