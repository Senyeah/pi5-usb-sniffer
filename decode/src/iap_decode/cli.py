"""iap-decode SESSION_DIR|PCAPNG...: write timeline.txt, timeline.jsonl, summary.json and audio WAVs."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

from .decoder import Decoder
from .model import Entry
from .output import build_summary, write_outputs, write_summary_text
from .sessionfiles import read_marks, read_proxy_log, read_session_start, read_udc
from .usbmon import read_transfers


def main(argv: list[str] | None = None) -> int:
    ap = argparse.ArgumentParser(prog="iap-decode", description=__doc__)
    ap.add_argument("inputs", nargs="+", type=Path, help="session directory, or pcapng files")
    ap.add_argument("-o", "--out", type=Path, help="output directory (default: SESSION_DIR/decoded)")
    ap.add_argument("--no-audio", action="store_true", help="do not write WAV files")
    args = ap.parse_args(argv)

    session_dir: Path | None = None
    pcaps: list[Path] = []
    for p in args.inputs:
        if p.is_dir():
            session_dir = p
            pcaps += sorted(p.glob("*.pcapng"))
        else:
            pcaps.append(p)
    if not pcaps:
        print("No pcapng files found.", file=sys.stderr)
        return 1
    out = args.out or ((session_dir or pcaps[0].parent) / "decoded")
    out.mkdir(parents=True, exist_ok=True)

    transfers = read_transfers(pcaps)
    dec = Decoder(out, write_audio=not args.no_audio)
    entries = dec.run(transfers)

    side: list[Entry] = []
    if session_dir is not None:
        side += read_marks(session_dir / "marks.tsv") + read_udc(session_dir / "udc.log")
        start = read_session_start(session_dir / "session.txt")
        if start is not None:
            side += read_proxy_log(session_dir / "proxy.log", start.date())
    entries = sorted(entries + side, key=lambda e: e.t)

    summary = build_summary([p.name for p in pcaps], len(transfers), dec, side)
    write_outputs(out, entries, summary)
    write_summary_text(sys.stdout, summary)
    print(f"\nOutput: {out}")
    return 0
