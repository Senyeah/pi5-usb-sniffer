"""Readers for the text files that capture writes next to the pcapng: marks, UDC state, proxy log."""

from __future__ import annotations

import re
from datetime import UTC, date, datetime, timedelta
from pathlib import Path

from .model import Entry

ISO_BURST_S = 0.05  # iso timing errors closer than this become one entry


def _iso(ts: str) -> float:
    return datetime.fromisoformat(ts.replace("Z", "+00:00")).timestamp()


def read_session_start(path: Path) -> datetime | None:
    if not path.exists():
        return None
    for line in path.read_text().splitlines():
        if line.startswith("started_utc:"):
            return datetime.fromisoformat(line.split(":", 1)[1].strip().replace("Z", "+00:00"))
    return None


def read_marks(path: Path) -> list[Entry]:
    if not path.exists():
        return []
    out = []
    for line in path.read_text().splitlines():
        parts = line.split("\t")
        if len(parts) >= 3:
            out.append(Entry(_iso(parts[0]), "mark", parts[2]))
    return out


_UDC = re.compile(r"^(\S+) state=(.*) speed=(\S+)$")


def read_udc(path: Path) -> list[Entry]:
    if not path.exists():
        return []
    out = []
    for line in path.read_text().splitlines():
        m = _UDC.match(line)
        if m:
            out.append(Entry(_iso(m.group(1)), "udc", f"gadget {m.group(2)}", fields={"speed": m.group(3)}))
    return out


_LINE = re.compile(r"^(\d\d):(\d\d):(\d\d)\.(\d{3})Z (.*)$")
_CAPTURE = re.compile(r"^\[capture\] (\S+Z) (.*)$")
_RULE = re.compile(r"Matched injection rule: (\w+), index: (\d+) \((.*)\)")
_EVENTS = {"connect", "disconnect", "suspend", "resume", "reset"}


def read_proxy_log(path: Path, day: date) -> list[Entry]:
    """Relay events that usbmon cannot see: the stereo side of the gadget."""
    if not path.exists():
        return []
    out: list[Entry] = []
    base = datetime(day.year, day.month, day.day, tzinfo=UTC)
    last = 0.0
    burst: Entry | None = None
    with path.open(errors="replace") as fh:
        for line in fh:
            line = line.rstrip("\n")
            if c := _CAPTURE.match(line):
                out.append(Entry(_iso(c.group(1)), "relay", c.group(2)))
                continue
            m = _LINE.match(line)
            if not m:
                continue
            h, mi, s, ms, text = m.groups()
            t = (base + timedelta(hours=int(h), minutes=int(mi), seconds=int(s), milliseconds=int(ms))).timestamp()
            if t < last - 3600:
                base += timedelta(days=1)  # midnight
                t += 86400
            last = t
            e = _classify(t, text)
            if e is None:
                continue
            if e.name == "iso timing error":
                if burst is not None and t - burst.fields["last"] <= ISO_BURST_S:
                    burst.fields["count"] += 1
                    burst.fields["last"] = t
                    continue
                burst = e
                e.fields = {"count": 1, "last": t}
            out.append(e)
    for e in out:
        e.fields.pop("last", None)
    return out


def _classify(t: float, text: str) -> Entry | None:
    if text.startswith("event: "):
        ev = text[7:].split(",")[0]
        if ev in _EVENTS:
            return Entry(t, "relay", f"stereo {ev}")
        return None
    if text.startswith("Resetting device"):
        return Entry(t, "relay", "relay resets device")
    if "isochronous timing error" in text:
        return Entry(t, "relay", "iso timing error")
    if m := _RULE.search(text):
        return Entry(t, "relay", f"rule {m.group(1)}", fields={"request": m.group(3)})
    if "device stalled this OUT request before" in text:
        return Entry(t, "relay", "learned stall", fields={"request": text.split("(", 1)[-1].rstrip(")")})
    if "device stalled OUT request with data" in text:
        return Entry(t, "relay", "stall learned", fields={"request": text.split("(", 1)[-1].rstrip(")")})
    if "served the full-speed HID report descriptor" in text:
        return Entry(t, "relay", "served FS HID report descriptor")
    return None
