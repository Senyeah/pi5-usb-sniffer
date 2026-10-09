# /// script
# requires-python = ">=3.12"
# dependencies = ["numpy"]
# ///
"""Check analyze.py with a made-up capture of known delay and clock offset. Needs ffmpeg (SBC encoder).

Usage: uv run latency/selftest.py
"""

from __future__ import annotations

import json
import struct
import subprocess
import sys
import tempfile
from datetime import UTC, datetime
from pathlib import Path

import numpy as np

HERE = Path(__file__).resolve().parent
RATE = 44100
DELAY = 0.5234  # Bluetooth sample in to USB sample out, the value to find
OFFSET = 0.0123  # Pi 5 clock minus Pi 3 clock
EPOCH_US = 0x00DCDDB30F2F8000
T0 = 1_760_000_000.0  # start of the capture, Pi 5 clock


def sbc_frames(raw: bytes) -> list[bytes]:
    frames, off = [], 0
    while off + 4 <= len(raw) and raw[off] == 0x9C:
        h = raw[off : off + 4]
        blocks, sub = (4, 8, 12, 16)[(h[1] >> 4) & 3], 8 if h[1] & 1 else 4
        mode, bitpool = (h[1] >> 2) & 3, h[2]
        ch = 1 if mode == 0 else 2
        n = 4 + (4 * sub * ch) // 8
        n += -(-(sub + blocks * bitpool) // 8) if mode == 3 else -(-(blocks * bitpool * (ch if mode < 2 else 1)) // 8)
        frames.append(raw[off : off + n])
        off += n
    return frames


def main() -> None:
    rng = np.random.default_rng(1)
    pcm = (rng.standard_normal((RATE * 12, 2)) * 3000).astype("<i2")
    with tempfile.TemporaryDirectory() as tmp:
        tmp = Path(tmp)
        (tmp / "in.raw").write_bytes(pcm.tobytes())
        subprocess.run(["ffmpeg", "-v", "error", "-f", "s16le", "-ar", str(RATE), "-ac", "2", "-i", str(tmp / "in.raw"),
                        "-c:a", "sbc", "-b:a", "328k", "-f", "sbc", str(tmp / "in.sbc")], check=True)
        frames = sbc_frames((tmp / "in.sbc").read_bytes())
        subprocess.run(["ffmpeg", "-v", "error", "-f", "sbc", "-i", str(tmp / "in.sbc"), "-f", "s16le", "-ac", "2",
                        str(tmp / "dec.raw")], check=True)
        decoded = np.frombuffer((tmp / "dec.raw").read_bytes(), dtype="<i2").reshape(-1, 2)

        cap = tmp / "cap"
        cap.mkdir()
        per = 128  # samples per SBC frame at 16 blocks, 8 subbands
        # Bluetooth: 8 frames per RTP packet; a packet arrives when its first sample is due, plus 0 to 5 ms.
        bt = bytearray(b"btsnoop\0" + struct.pack(">II", 1, 2001))
        for k in range(0, len(frames), 8):
            group = frames[k : k + 8]
            rtp = struct.pack(">BBHII", 0x80, 0x60, k // 8, k * per, 1) + bytes([len(group)]) + b"".join(group)
            l2 = struct.pack("<HH", len(rtp), 0x0041) + rtp
            t = T0 + k * per / RATE + rng.uniform(0, 0.005)
            for i in range(0, len(l2), 1021):
                frag = l2[i : i + 1021]
                acl = struct.pack("<HH", 0x000B | ((2 if i == 0 else 1) << 12), len(frag)) + frag
                bt += struct.pack(">IIIIq", len(acl), len(acl), 5, 0, int(t * 1e6) + EPOCH_US) + acl
        (cap / "bt.btsnoop").write_bytes(bt)

        # USB: the same sound at half level, sample n out at T0 + DELAY + n / RATE (Pi 5 clock); 8 packets per URB.
        out = (decoded.astype(np.int32) // 2).astype("<i2")
        pc = bytearray(struct.pack("<IHHiIII", 0xA1B2C3D4, 2, 4, 0, 0, 262144, 220))

        def record(kind, xfer, ep, ts, data=b"", setup=b"\0" * 8, iso=(), urb=1):
            descs = b"".join(struct.pack("<iIII", 0, o, n, 0) for o, n in iso)
            hdr = struct.pack("<QBBBBHbbqiiII8siiII", urb, ord(kind), xfer, ep, 3, 1, 0 if kind == "S" else 0x2D, 0,
                              int(ts), int(round((ts % 1) * 1e6)), 0, len(data), len(descs) + len(data), setup, 1, 0, 0,
                              len(iso))
            body = hdr + descs + data
            return struct.pack("<IIII", int(ts), int(round((ts % 1) * 1e6)), len(body), len(body)) + body

        events = []
        n, m, acc = 0, 0, 0
        urb, iso = bytearray(), []
        while n + 45 < len(out):
            acc += 44100
            size = acc // 1000
            acc -= size * 1000
            t3 = T0 + DELAY + n / RATE - OFFSET
            iso.append((len(urb), size * 4))
            urb += out[n : n + size].tobytes()
            n += size
            m += 1
            if m % 8 == 0:
                events.append((t3, record("C", 0, 0x81, t3, bytes(urb), iso=iso, urb=m)))
                urb, iso = bytearray(), []

        # HID: a poll every 0.6 s. The Pi 5 reads it 0.4 ms after the Pi 3 sends it, and its reply reaches the
        # Pi 3 0.5 ms after the write.
        trace = []
        for i in range(int(12 / 0.6)):
            s3 = T0 + 0.1 + i * 0.6
            req = bytes([0x05, 0x55, 0x03, 0x04, 0x00, 0x1C, i & 0xFF, 0, 0])
            setup = struct.pack("<BBHHH", 0x21, 0x09, 0x0205, 2, len(req))
            events.append((s3, record("S", 2, 0x00, s3, req, setup=setup, urb=100000 + i)))
            events.append((s3 + 0.001, record("C", 2, 0x00, s3 + 0.001, urb=100000 + i)))
            trace.append((s3 + OFFSET + 0.0004, req))
            w5 = s3 + OFFSET + 0.003
            rep = bytes([0x01, 0x55, 0x0C, 0x04, 0x00, 0x1D, i & 0xFF]) + bytes(6)
            events.append((w5 - OFFSET + 0.0005, record("C", 1, 0x82, w5 - OFFSET + 0.0005, rep, urb=200000 + i)))
            trace.append((w5, rep))
        for _, rec in sorted(events, key=lambda e: e[0]):
            pc += rec
        (cap / "usb.pcap").write_bytes(pc)
        with open(cap / "bridge-trace.jsonl", "w") as f:
            for t, raw in trace:
                ts = datetime.fromtimestamp(t, UTC).isoformat().replace("+00:00", "Z")
                f.write(json.dumps({"dir": "?", "name": "hid-report", "raw": raw.hex(), "t": ts}) + "\n")

        subprocess.run([sys.executable, str(HERE / "analyze.py"), str(cap)], check=True)
        res = json.loads((cap / "result.json").read_text())

    packet_ms = 8 * per / RATE * 1000
    ok = True
    if abs(res["clock_offset_ms"] - OFFSET * 1000) > res["clock_offset_uncertainty_ms"] + 0.05:
        print(f"FAIL clock offset {res['clock_offset_ms']} ms, want {OFFSET * 1000}")
        ok = False
    # The first sample of a packet waits DELAY, less the 0 to 5 ms that the packet came late.
    lo, hi = DELAY * 1000 - 5 - 1.5, DELAY * 1000 + 1.5
    if not (lo <= res["min_ms"] and res["max_ms"] <= hi):
        print(f"FAIL delays {res['min_ms']} to {res['max_ms']} ms, want inside {lo:.1f} to {hi:.1f}")
        ok = False
    if abs(res["trend_ms_per_min"]) > 20:
        print(f"FAIL trend {res['trend_ms_per_min']} ms per minute, want about 0")
        ok = False
    print("selftest", "passed" if ok else "FAILED", f"(true delay {DELAY * 1000} ms, median {res['median_ms']} ms, "
          f"packets {packet_ms:.1f} ms)")
    sys.exit(0 if ok else 1)


if __name__ == "__main__":
    main()
