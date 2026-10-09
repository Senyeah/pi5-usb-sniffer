"""L0: read usbmon records from pcapng files and pair them into transfers."""

from __future__ import annotations

import struct
from collections.abc import Iterable, Iterator
from dataclasses import dataclass, field
from pathlib import Path

LINKTYPE_USB_LINUX = 189  # 48-byte header
LINKTYPE_USB_LINUX_MMAPPED = 220  # 64-byte header

XFER_ISO, XFER_INT, XFER_CTRL, XFER_BULK = 0, 1, 2, 3
XFER_NAMES = {XFER_ISO: "iso", XFER_INT: "int", XFER_CTRL: "ctrl", XFER_BULK: "bulk"}

EINPROGRESS = -115

ERRNO_NAMES = {
    0: "ok",
    -2: "ENOENT",
    -18: "EXDEV",
    -32: "EPIPE",
    -61: "ENODATA",
    -71: "EPROTO",
    -75: "EOVERFLOW",
    -104: "ECONNRESET",
    -108: "ESHUTDOWN",
    -110: "ETIMEDOUT",
    -115: "EINPROGRESS",
    -121: "EREMOTEIO",
}


def errno_name(status: int | None) -> str:
    if status is None:
        return "pending"
    return ERRNO_NAMES.get(status, str(status))


@dataclass(frozen=True)
class IsoDesc:
    status: int
    offset: int
    length: int


@dataclass(frozen=True)
class Event:
    """One usbmon record: a submit ('S'), complete ('C') or submit error ('E')."""

    ts: float
    urb_id: int
    kind: str
    xfer: int
    ep: int  # bit 7 set for IN
    dev: int
    bus: int
    status: int
    length: int
    setup: bytes | None
    data: bytes  # after the iso descriptors
    iso: tuple[IsoDesc, ...] = ()
    truncated: bool = False


def _blocks(buf: bytes) -> Iterator[tuple[int, bytes, str]]:
    off = 0
    endian = "<"
    while off + 12 <= len(buf):
        btype_le = struct.unpack_from("<I", buf, off)[0]
        if btype_le == 0x0A0D0D0A:
            magic = buf[off + 8 : off + 12]
            endian = "<" if magic == b"\x4d\x3c\x2b\x1a" else ">"
        btype, blen = struct.unpack_from(endian + "II", buf, off)
        if blen < 12 or off + blen > len(buf):
            break  # dumpcap killed mid-write
        yield btype, buf[off + 8 : off + blen - 4], endian
        off += blen


def read_events(path: Path) -> Iterator[Event]:
    buf = path.read_bytes()
    links: list[int] = []
    for btype, body, endian in _blocks(buf):
        if btype == 0x0A0D0D0A:
            links = []
        elif btype == 1:
            links.append(struct.unpack_from(endian + "H", body)[0])
        elif btype == 6:
            iface, _, _, caplen, _ = struct.unpack_from(endian + "IIIII", body)
            link = links[iface] if iface < len(links) else None
            ev = parse_record(body[20 : 20 + caplen], link, endian)
            if ev is not None:
                yield ev


_HDR48 = "QBBBBHbbqiiII8s"
_HDR64 = _HDR48 + "iiII"


def parse_record(pkt: bytes, link: int | None, endian: str = "<") -> Event | None:
    if link == LINKTYPE_USB_LINUX_MMAPPED:
        fmt, hlen = endian + _HDR64, 64
    elif link == LINKTYPE_USB_LINUX:
        fmt, hlen = endian + _HDR48, 48
    else:
        return None
    if len(pkt) < hlen:
        return None
    f = struct.unpack_from(fmt, pkt)
    urb_id, kind, xfer, ep, dev, bus, flag_setup, flag_data, sec, usec, status, length, len_cap, setup = f[:14]
    ndesc = f[17] if hlen == 64 else 0
    rest = pkt[hlen:]
    iso: tuple[IsoDesc, ...] = ()
    if xfer == XFER_ISO and ndesc:
        iso = tuple(
            IsoDesc(*struct.unpack_from(endian + "iII", rest, i * 16))
            for i in range(ndesc)
            if (i + 1) * 16 <= len(rest)
        )
        rest = rest[ndesc * 16 :]
    has_setup = flag_setup == 0 and xfer == XFER_CTRL and chr(kind) == "S"
    data = rest if flag_data == 0 else b""
    return Event(
        ts=sec + usec / 1e6,
        urb_id=urb_id,
        kind=chr(kind),
        xfer=xfer,
        ep=ep,
        dev=dev,
        bus=bus,
        status=status,
        length=length,
        setup=setup if has_setup else None,
        data=data,
        iso=iso,
        truncated=len(pkt) - hlen < len_cap or (flag_data == 0 and xfer != XFER_ISO and len(data) < length),
    )


@dataclass
class Transfer:
    """A submit paired with its completion. 't' is when the data crossed the bus side we saw."""

    t_submit: float
    t_done: float | None
    xfer: int
    ep: int
    dev: int
    bus: int
    setup: bytes | None
    data: bytes
    status: int | None
    length: int
    iso: list[tuple[int, bytes]] = field(default_factory=list)  # (status, packet data)
    truncated: bool = False

    @property
    def is_in(self) -> bool:
        if self.setup is not None:
            return bool(self.setup[0] & 0x80)
        return bool(self.ep & 0x80)

    @property
    def t(self) -> float:
        # OUT data leaves at submit; IN data arrives at completion.
        if self.is_in and self.t_done is not None:
            return self.t_done
        return self.t_submit


def _iso_packets(ev: Event) -> list[tuple[int, bytes]]:
    return [(d.status, ev.data[d.offset : d.offset + d.length]) for d in ev.iso]


def pair(events: Iterable[Event]) -> Iterator[Transfer]:
    """Yield transfers in completion order. Unfinished URBs come last with status None."""
    pending: dict[int, Transfer] = {}
    for ev in events:
        if ev.kind == "S":
            tr = Transfer(
                t_submit=ev.ts,
                t_done=None,
                xfer=ev.xfer,
                ep=ev.ep,
                dev=ev.dev,
                bus=ev.bus,
                setup=ev.setup,
                data=ev.data,
                status=None,
                length=ev.length,
                truncated=ev.truncated,
            )
            old = pending.pop(ev.urb_id, None)
            if old is not None:
                yield old  # id reused without a completion: should not happen
            pending[ev.urb_id] = tr
            continue
        tr = pending.pop(ev.urb_id, None)
        if tr is None:
            # Completion of a URB submitted before the capture started.
            tr = Transfer(ev.ts, None, ev.xfer, ev.ep, ev.dev, ev.bus, None, b"", None, 0)
        tr.t_done = ev.ts
        tr.status = ev.status
        if tr.is_in:
            tr.data = ev.data
            tr.length = ev.length
            tr.truncated = ev.truncated
            if ev.xfer == XFER_ISO:
                tr.iso = _iso_packets(ev)
        elif ev.kind == "E":
            tr.length = 0
        yield tr
    yield from pending.values()


def read_transfers(paths: Iterable[Path]) -> list[Transfer]:
    def events() -> Iterator[Event]:
        for p in paths:
            yield from read_events(p)

    transfers = list(pair(events()))
    transfers.sort(key=lambda tr: tr.t)
    return transfers
