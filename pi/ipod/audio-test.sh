#!/usr/bin/env bash
# Measure the USB audio path of the iPod gadget without Bluetooth. Runs on the Mac.
# The Pi 5 plays a 440 Hz tone into the gadget. The Pi 3 (the stereo) records it and counts the glitches.
# Usage: pi/ipod/audio-test.sh [seconds]    (default 20)
set -euo pipefail

SECS=${1:-20}
PI5=${PI5:-pi5-sniffer.local}
PI3=${PI3:-pi3-sink.local}
O=(-o BatchMode=yes -o ConnectTimeout=8)

ssh "${O[@]}" "root@$PI3" 'systemctl stop sink-audio; rm -f /tmp/cap.wav'
ssh "${O[@]}" "root@$PI5" 'systemctl stop ipod-audio; pkill aplay || true; python3 - <<"PY"
import math, struct, wave
w = wave.open("/tmp/tone.wav", "wb"); w.setnchannels(2); w.setsampwidth(2); w.setframerate(44100)
w.writeframes(b"".join(struct.pack("<hh", v, v) for v in (int(8000 * math.sin(2 * math.pi * 440 * i / 44100)) for i in range(44100 * 40))))
w.close()
PY'
# The Pi 3 starts streaming first, as a stereo does. Then the Pi 5 plays.
ssh "${O[@]}" "root@$PI3" "(nohup arecord -D hw:CARD=iPad,DEV=0 -f S16_LE -r 44100 -c 2 -d $SECS /tmp/cap.wav > /tmp/arecord.log 2>&1 &); sleep 1"
sleep 1
ssh "${O[@]}" "root@$PI5" '(nohup aplay -q -D plughw:CARD=UAC1Gadget,DEV=0 /tmp/tone.wav > /tmp/aplay.log 2>&1 &); sleep 1'
sleep $((SECS + 3))
ssh "${O[@]}" "root@$PI5" 'pkill aplay || true'
ssh "${O[@]}" "root@$PI3" 'python3 - <<"PY"
import math, struct, wave
w = wave.open("/tmp/cap.wav"); raw = w.readframes(w.getnframes()); n = len(raw) // 4
L = struct.unpack("<%dh" % (n * 2), raw[:n * 4])[0::2]
loud = [i for i, x in enumerate(L) if abs(x) > 1000]
if not loud:
    print("no tone recorded"); raise SystemExit(1)
start = loud[0]; seg = L[start:]
k = 2 * math.cos(2 * math.pi * 440 / 44100)
ev = []
for i in range(2, len(seg)):
    if abs(seg[i] - (k * seg[i - 1] - seg[i - 2])) > 600:
        if not ev or i - ev[-1] > 100:
            ev.append(i)
secs = len(seg) // 44100
per = [0] * (secs + 1)
for i in ev:
    per[i // 44100] += 1
gaps = 0; c = 0
for x in seg:
    if abs(x) < 30: c += 1
    else:
        gaps += c >= 30; c = 0
print("tone seconds %d, discontinuities %d, silent gaps %d" % (secs, len(ev), gaps))
print("discontinuities per second:", per[:secs])
PY'
