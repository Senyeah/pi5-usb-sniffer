# /// script
# requires-python = ">=3.12"
# dependencies = ["numpy"]
# ///
"""Measure the Pi 5's audio delay from a latency-test.sh capture.

The delay runs from the arrival of a Bluetooth audio packet at the Pi 5 (btmon, Pi 5 clock) to the USB packet
that carries the same sound to the stereo (usbmon on the Pi 3 sink, Pi 3 clock). The HID reports of the iAP
session are on both sides (the ipod-bridge trace and usbmon) and give the offset between the two clocks.

Usage: uv run pi/ipod/latency/analyze.py <capture dir>   (needs ffmpeg with the SBC decoder)
"""

from __future__ import annotations

import json
import struct
import subprocess
import sys
import tempfile
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path

import numpy as np

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / "decode" / "src"))
from iap_decode.usbmon import LINKTYPE_USB_LINUX_MMAPPED, XFER_CTRL, XFER_INT, XFER_ISO, parse_record  # noqa: E402

RATE = 44100
BTSNOOP_EPOCH_US = 0x00DCDDB30F2F8000  # btsnoop counts microseconds from the year 0
MON_ACL_RX = 5  # btmon (datalink 2001) opcode of a received ACL packet


@dataclass
class Packets:
    """Audio in packets: the first sample index of each packet and its time."""

    pcm: np.ndarray  # mono float
    first: np.ndarray
    time: np.ndarray

    def index_of(self, sample: int) -> int:
        return max(int(np.searchsorted(self.first, sample, side="right")) - 1, 0)

    def time_of(self, sample: int) -> float:
        return float(self.time[self.index_of(sample)])


def btsnoop_records(path: Path):
    buf = path.read_bytes()
    if buf[:8] != b"btsnoop\0":
        raise SystemExit(f"{path}: not a btsnoop file")
    _, link = struct.unpack_from(">II", buf, 8)
    if link != 2001:
        raise SystemExit(f"{path}: datalink {link}, want 2001 (btmon -w)")
    off = 16
    while off + 24 <= len(buf):
        _, incl, flags, _, ts = struct.unpack_from(">IIIIq", buf, off)
        off += 24
        yield (ts - BTSNOOP_EPOCH_US) / 1e6, flags & 0xFFFF, buf[off : off + incl]
        off += incl


def l2cap_frames(records):
    """Received L2CAP frames, with the time of their last ACL fragment."""
    partial: dict[int, list] = {}
    for ts, op, d in records:
        if op != MON_ACL_RX or len(d) < 4:
            continue
        h, n = struct.unpack_from("<HH", d)
        handle, pb, payload = h & 0x0FFF, (h >> 12) & 3, d[4 : 4 + n]
        if pb in (0, 2):
            if len(payload) < 4:
                continue
            size, cid = struct.unpack_from("<HH", payload)
            partial[handle] = [size, cid, bytearray(payload[4:])]
        elif pb == 1 and handle in partial:
            partial[handle][2] += payload
        else:
            continue
        size, cid, data = partial[handle]
        if len(data) >= size:
            del partial[handle]
            yield ts, cid, bytes(data[:size])


def is_sbc_rtp(f: bytes) -> bool:
    return len(f) > 17 and f[0] & 0xC0 == 0x80 and f[13] == 0x9C


def sbc_frame_samples(h: bytes) -> int:
    blocks = (4, 8, 12, 16)[(h[1] >> 4) & 3]
    subbands = 8 if h[1] & 1 else 4
    return blocks * subbands


def bluetooth_in(path: Path) -> Packets:
    frames = [x for x in l2cap_frames(btsnoop_records(path))]
    media = [x for x in frames if is_sbc_rtp(x[2])]
    if not media:
        raise SystemExit("no SBC media packets in the Bluetooth capture: did the phone play?")
    cids = {}
    for _, cid, _ in media:
        cids[cid] = cids.get(cid, 0) + 1
    cid = max(cids, key=cids.get)
    media = [x for x in media if x[1] == cid]
    per_frame = sbc_frame_samples(media[0][2][13:17])
    first, times, payload, total, seq_gaps, last_seq = [], [], bytearray(), 0, 0, None
    for ts, _, f in media:
        seq = struct.unpack_from(">H", f, 2)[0]
        if last_seq is not None and seq != (last_seq + 1) & 0xFFFF:
            seq_gaps += 1
        last_seq = seq
        first.append(total)
        times.append(ts)
        total += (f[12] & 0x0F) * per_frame
        payload += f[13:]
    with tempfile.TemporaryDirectory() as tmp:
        src, dst = Path(tmp) / "in.sbc", Path(tmp) / "in.raw"
        src.write_bytes(payload)
        subprocess.run(["ffmpeg", "-v", "error", "-f", "sbc", "-i", str(src), "-f", "s16le", "-ac", "2", "-ar",
                        str(RATE), str(dst)], check=True)
        pcm = np.frombuffer(dst.read_bytes(), dtype="<i2").astype(np.float32).reshape(-1, 2).mean(axis=1)
    print(f"Bluetooth: {len(media)} SBC packets on L2CAP channel {cid:#x}, {total / RATE:.1f} s of audio, "
          f"{seq_gaps} RTP sequence gaps, decoded {len(pcm)} of {total} samples")
    return Packets(pcm, np.array(first), np.array(times))


def pcap_records(path: Path):
    buf = path.read_bytes()
    magic = struct.unpack_from("<I", buf)[0]
    endian = "<" if magic in (0xA1B2C3D4, 0xA1B23C4D) else ">"
    link = struct.unpack_from(endian + "I", buf, 20)[0]
    off = 24
    while off + 16 <= len(buf):
        _, _, incl, _ = struct.unpack_from(endian + "IIII", buf, off)
        off += 16
        ev = parse_record(buf[off : off + incl], link, endian)
        off += incl
        if ev is not None:
            yield ev


def usb_out(events) -> tuple[Packets, int]:
    iso = [e for e in events if e.xfer == XFER_ISO and e.ep & 0x80 and e.kind == "C" and e.iso]
    if not iso:
        raise SystemExit("no isochronous IN packets in the USB capture")
    dev = max({e.dev for e in iso}, key=lambda d: sum(1 for e in iso if e.dev == d))
    chunks, first, times, total = [], [], [], 0
    for e in iso:
        if e.dev != dev:
            continue
        n = len(e.iso)
        for j, d in enumerate(e.iso):
            data = e.data[d.offset : d.offset + d.length]
            frames = len(data) // 4
            if frames == 0:
                continue
            first.append(total)
            times.append(e.ts - (n - 1 - j) * 0.001)  # one packet per 1 ms frame; the URB completes after the last
            chunks.append(data[: frames * 4])
            total += frames
    pcm = np.frombuffer(b"".join(chunks), dtype="<i2").astype(np.float32).reshape(-1, 2).mean(axis=1)
    print(f"USB: device {dev}, {len(first)} audio packets, {total / RATE:.1f} s of audio")
    return Packets(pcm, np.array(first), np.array(times)), dev


def clock_offset(events, dev: int, trace: Path) -> tuple[float, float]:
    """Pi 5 clock minus Pi 3 clock, as (estimate, half the width of the possible range)."""
    entries = [json.loads(line) for line in trace.read_text().splitlines() if line.strip()]
    reports = []
    for e in entries:
        if e.get("name") != "hid-report":
            continue
        raw = bytes.fromhex(e["raw"])
        reports.append((datetime.fromisoformat(e["t"].replace("Z", "+00:00")).timestamp(), raw))
    out_ids = {5, 6, 7, 8, 9}  # reports from the stereo; 1 to 4 go to the stereo
    from_stereo = [(t, r) for t, r in reports if r and r[0] in out_ids]
    to_stereo = [(t, r) for t, r in reports if r and r[0] not in out_ids]

    submits = {}
    upper, lower = [], []
    for e in events:
        if e.dev != dev:
            continue
        if e.xfer == XFER_CTRL and e.kind == "S" and e.setup and e.setup[0] == 0x21 and e.setup[1] == 0x09:
            submits[e.urb_id] = (e.ts, e.data)
        elif e.xfer == XFER_CTRL and e.kind == "C" and e.urb_id in submits:
            s3, data = submits.pop(e.urb_id)
            for t5, r in from_stereo:
                if r == data and abs(t5 - s3) < 0.25:
                    upper.append(t5 - s3)  # the Pi 5 read it after the Pi 3 sent it
        elif e.xfer == XFER_INT and e.ep & 0x80 and e.kind == "C" and e.data:
            for t5, r in to_stereo:
                if r == e.data[: len(r)] and abs(t5 - e.ts) < 0.25:
                    lower.append(t5 - e.ts)  # the Pi 3 got it after the Pi 5 wrote it
    if not upper or not lower:
        raise SystemExit(f"cannot match HID reports for the clock offset ({len(upper)} to the Pi 5, {len(lower)} from it)")
    # Repeated polls match more than one report: keep the tightest consistent bounds.
    hi, lo = min(upper), max(lower)
    if lo > hi:
        hi, lo = float(np.percentile(upper, 5)), float(np.percentile(lower, 95))
    print(f"clock offset (Pi 5 - Pi 3): {(hi + lo) / 2 * 1000:+.2f} ms, possible range {lo * 1000:+.2f} to "
          f"{hi * 1000:+.2f} ms ({len(upper)} reports to the Pi 5, {len(lower)} from it)")
    return (hi + lo) / 2, abs(hi - lo) / 2


def match(inp: Packets, out: Packets, offset: float, window: float = 0.25, step: float = 0.5):
    """For windows of the input, find the same sound in the output. Returns (input time, delay of the first
    sample of the input packet, score). A packet's samples all arrive at once, so later samples wait less."""
    w = int(window * RATE)
    out_t5 = out.time + offset  # USB times on the Pi 5 clock
    results = []
    for start in range(0, len(inp.pcm) - w, int(step * RATE)):
        seg = inp.pcm[start : start + w]
        if np.sqrt(np.mean(seg**2)) < 100:  # silence matches anywhere
            continue
        t_in = inp.time_of(start)
        lo = int(np.searchsorted(out_t5, t_in)) - 1
        hi = int(np.searchsorted(out_t5, t_in + 2.5))
        if lo < 0 or hi >= len(out.first):
            continue
        a, b = int(out.first[lo]), int(out.first[hi])
        ref = out.pcm[a:b]
        if len(ref) < 2 * w:
            continue
        n = 1 << int(np.ceil(np.log2(len(ref) + w)))
        corr = np.fft.irfft(np.fft.rfft(ref, n) * np.conj(np.fft.rfft(seg, n)), n)[: len(ref) - w]
        energy = np.convolve(ref**2, np.ones(w), "valid")[: len(corr)]
        score = corr / np.sqrt(energy * np.sum(seg**2) + 1e-9)
        k = int(np.argmax(score))
        if score[k] < 0.9:
            continue
        o = a + k
        into = (start - int(inp.first[inp.index_of(start)])) / RATE  # the window starts this far into its packet
        results.append((t_in, out.time_of(o) + offset - t_in - into, float(score[k])))
    return results


def main() -> None:
    cap = Path(sys.argv[1]) if len(sys.argv) > 1 else sys.exit(__doc__)
    inp = bluetooth_in(cap / "bt.btsnoop")
    events = list(pcap_records(cap / "usb.pcap"))
    out, dev = usb_out(events)
    offset, width = clock_offset(events, dev, cap / "bridge-trace.jsonl")
    res = match(inp, out, offset)
    if not res:
        raise SystemExit("no matching sound found between Bluetooth and USB")
    d = np.array([r[1] for r in res]) * 1000
    t = np.array([r[0] for r in res])
    slope = np.polyfit(t - t[0], d, 1)[0] * 60 if len(t) > 2 else 0.0
    half = float(np.mean(np.diff(inp.first))) / RATE / 2 * 1000  # half a Bluetooth packet
    avg = float(np.median(d)) + half
    print(f"\nDelay, Bluetooth packet in to USB packet out, {len(d)} windows over {t[-1] - t[0]:.0f} s:")
    print(f"  first sample of a packet: median {np.median(d):.1f} ms, min {d.min():.1f}, max {d.max():.1f}, "
          f"std {d.std():.1f} ms, trend {slope:+.2f} ms per minute")
    print(f"  average sample (packets of {2 * half:.1f} ms): {avg:.1f} ms  <- the value to report")
    print(f"  clock offset uncertainty +/- {width * 1000:.2f} ms, USB packet times +/- 1 ms")
    with open(cap / "delay.csv", "w") as f:
        f.write("input_time,first_sample_delay_ms,score\n")
        for r in res:
            f.write(f"{r[0]:.6f},{r[1] * 1000:.3f},{r[2]:.4f}\n")
    (cap / "result.json").write_text(json.dumps({
        "windows": len(d), "average_sample_ms": round(avg, 2), "median_ms": round(float(np.median(d)), 2), "mean_ms": round(float(d.mean()), 2),
        "min_ms": round(float(d.min()), 2), "max_ms": round(float(d.max()), 2), "std_ms": round(float(d.std()), 2),
        "trend_ms_per_min": round(float(slope), 3), "clock_offset_ms": round(offset * 1000, 3),
        "clock_offset_uncertainty_ms": round(width * 1000, 3)}, indent=2) + "\n")


if __name__ == "__main__":
    main()
