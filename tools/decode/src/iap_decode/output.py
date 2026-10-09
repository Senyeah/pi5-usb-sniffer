"""Timeline text, JSON Lines and summary output."""

from __future__ import annotations

import json
from collections import Counter
from datetime import UTC, datetime
from pathlib import Path
from typing import TextIO
from zoneinfo import ZoneInfo

from .model import Entry

LOCAL_TZ = ZoneInfo("Pacific/Auckland")


def utc(t: float) -> datetime:
    return datetime.fromtimestamp(t, UTC)


def fmt_value(v: object) -> str:
    if isinstance(v, str):
        plain = v and all(c.isprintable() and c not in ' =,"[]' for c in v)
        return v if plain else json.dumps(v, ensure_ascii=False)
    if isinstance(v, bool):
        return str(v).lower()
    if isinstance(v, list) and all(isinstance(x, str) for x in v):
        return "[" + ", ".join(v) + "]"
    if isinstance(v, (list, dict)):
        return json.dumps(v, ensure_ascii=False, separators=(",", ":"))
    return str(v)


def fmt_fields(fields: dict) -> str:
    return " ".join(f"{k}={fmt_value(v)}" for k, v in fields.items() if v is not None)


def text_line(e: Entry, t0: float) -> str:
    ts = utc(e.t).strftime("%H:%M:%S.%f")[:-3]
    return f"{ts} {e.t - t0:+9.3f}  {e.dir:3}  {e.layer:5}  {e.name:42} {fmt_fields(e.fields)}".rstrip()


def json_line(e: Entry, t0: float) -> str:
    rec = {
        "t": utc(e.t).isoformat(timespec="microseconds").replace("+00:00", "Z"),
        "t_rel": round(e.t - t0, 6),
        "dir": e.dir or None,
        "layer": e.layer,
        "name": e.name,
        "fields": e.fields,
    }
    if e.raw:
        rec["raw"] = e.raw.hex()
    return json.dumps(rec, ensure_ascii=False)


def write_outputs(out_dir: Path, entries: list[Entry], summary: dict) -> None:
    t0 = entries[0].t if entries else 0.0
    with (out_dir / "timeline.jsonl").open("w") as fh:
        for e in entries:
            fh.write(json_line(e, t0) + "\n")
    with (out_dir / "timeline.txt").open("w") as fh:
        _header(fh, entries, summary)
        for e in entries:
            fh.write(text_line(e, t0) + "\n")
        fh.write("\n")
        write_summary_text(fh, summary)
    (out_dir / "summary.json").write_text(json.dumps(summary, indent=2, ensure_ascii=False) + "\n")


def _header(fh: TextIO, entries: list[Entry], summary: dict) -> None:
    if entries:
        start = utc(entries[0].t)
        local = start.astimezone(LOCAL_TZ)
        fh.write(f"# start {start:%d/%m/%Y %H:%M:%S} UTC ({local:%d/%m/%Y %H:%M:%S %Z})\n")
    fh.write(f"# inputs: {', '.join(summary['inputs'])}\n")
    fh.write("# columns: time (UTC), seconds from start, direction, layer, name, fields\n")
    fh.write("# direction: A>D = host side (stereo or relay) to the Apple device, D>A = Apple device to host side\n\n")


def write_summary_text(fh: TextIO, s: dict) -> None:
    fh.write("# Summary\n")
    fh.write(f"transfers: {s['transfers']}\n")
    fh.write("explained:\n")
    for k, v in s["explained"].items():
        fh.write(f"  {v:8}  {k}\n")
    fh.write("unexplained:\n" if s["unexplained"] else "unexplained: none\n")
    for k, v in s["unexplained"].items():
        fh.write(f"  {v:8}  {k}\n")
    fh.write("iAP1 commands:\n")
    for row in s["iap1_commands"]:
        fh.write(f"  {row['count']:8}  {row['dir']}  {row['name']}\n")
    fh.write("audio streams:\n" if s["audio"] else "audio streams: none\n")
    for a in s["audio"]:
        fh.write(f"  {fmt_fields(a)}\n")
    if s.get("relay"):
        fh.write("relay events:\n")
        for k, v in s["relay"].items():
            fh.write(f"  {v:8}  {k}\n")


def build_summary(inputs: list[str], transfers: int, dec, relay_entries: list[Entry]) -> dict:
    relay: Counter[str] = Counter()
    for e in relay_entries:
        if e.layer == "relay":
            relay[e.name] += e.fields.get("count", 1)
    return {
        "inputs": inputs,
        "transfers": transfers,
        "explained": dict(dec.explained.most_common()),
        "unexplained": dict(dec.unexplained.most_common()),
        "iap1_commands": [
            {"dir": d, "name": n, "count": c}
            for (d, n), c in sorted(dec.iap_counts.items(), key=lambda x: (-x[1], x[0]))
        ],
        "audio": [s.summary() for s in dec.audio.streams],
        "relay": dict(relay.most_common()),
    }
