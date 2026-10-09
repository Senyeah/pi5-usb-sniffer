from __future__ import annotations

from dataclasses import dataclass, field

# A: the host side (the stereo, or the relay itself). D: the Apple device.
A2D, D2A = "A>D", "D>A"


@dataclass
class Entry:
    t: float
    layer: str  # usb, hid, iap1, iap2, audio, relay, udc, mark
    name: str
    dir: str = ""
    fields: dict = field(default_factory=dict)
    raw: bytes | None = None
