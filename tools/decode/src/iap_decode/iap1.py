"""L2 and L3: protocol detection and iAP1 packet framing."""

from __future__ import annotations

from dataclasses import dataclass

IAP2_DETECT = b"\xff\x55\x02\x00\xee\x10"
LINGO_GENERAL = 0x00
LINGO_EXTENDED = 0x04
CMD_START_IDPS = 0x38


@dataclass
class Iap1Packet:
    lingo: int
    command: int
    txid: int | None
    data: bytes
    checksum_ok: bool
    large: bool
    raw: bytes


@dataclass
class Iap2Detect:
    raw: bytes


@dataclass
class Iap2Link:
    control: int
    seq: int
    ack: int
    session: int
    payload: bytes
    raw: bytes


@dataclass
class FrameError:
    reason: str
    raw: bytes


Frame = Iap1Packet | Iap2Detect | Iap2Link | FrameError


def checksum(body: bytes) -> int:
    """Two's complement of the byte sum, from the length byte(s) to the end of the data."""
    return (-sum(body)) & 0xFF


def encode(lingo: int, command: int, data: bytes = b"", txid: int | None = None) -> bytes:
    """Build an iAP1 packet without the 0xFF sync byte, as it travels over USB HID."""
    cmd = command.to_bytes(2 if lingo == LINGO_EXTENDED else 1, "big")
    body = bytes([lingo]) + cmd + (txid.to_bytes(2, "big") if txid is not None else b"") + data
    n = len(body)
    length = bytes([n]) if n <= 0xFF else b"\x00" + n.to_bytes(2, "big")
    return b"\x55" + length + body + bytes([checksum(length + body)])


class Parser:
    """Splits a reassembled HID message into frames. Tracks the transaction-ID state."""

    def __init__(self) -> None:
        self.txids = False

    def reset(self) -> None:
        self.txids = False

    def parse(self, buf: bytes) -> list[Frame]:
        frames: list[Frame] = []
        i = 0
        while i < len(buf):
            if buf[i:].startswith(IAP2_DETECT):
                frames.append(Iap2Detect(buf[i : i + len(IAP2_DETECT)]))
                i += len(IAP2_DETECT)
                continue
            if buf[i] == 0xFF and i + 1 < len(buf) and buf[i + 1] == 0x5A:
                frame, n = _parse_iap2_link(buf[i:])
                frames.append(frame)
                i += n
                continue
            if buf[i] == 0xFF and i + 1 < len(buf) and buf[i + 1] == 0x55:
                i += 1  # sync byte, not used over USB
            if buf[i] == 0x55:
                frame, n = self._parse_iap1(buf[i:])
                frames.append(frame)
                i += n
                continue
            rest = buf[i:]
            if any(rest):
                frames.append(FrameError("bytes outside a packet", rest))
            break  # zero padding to the end of the report
        return frames

    def _parse_iap1(self, buf: bytes) -> tuple[Frame, int]:
        if len(buf) < 2:
            return FrameError("truncated header", buf), len(buf)
        large = buf[1] == 0
        if large:
            if len(buf) < 4:
                return FrameError("truncated large header", buf), len(buf)
            n, hdr = int.from_bytes(buf[2:4], "big"), 4
        else:
            n, hdr = buf[1], 2
        end = hdr + n
        if len(buf) < end + 1:
            return FrameError(f"truncated: length {n}, have {len(buf) - hdr}", buf), len(buf)
        body = buf[hdr:end]
        ok = checksum(buf[1:end]) == buf[end]
        raw = buf[: end + 1]
        if n < 2:
            return FrameError("body shorter than lingo and command", raw), end + 1
        lingo = body[0]
        if lingo == LINGO_EXTENDED:
            if n < 3:
                return FrameError("Extended Interface body without a 2-byte command", raw), end + 1
            command, rest = int.from_bytes(body[1:3], "big"), body[3:]
        else:
            command, rest = body[1], body[2:]
        # StartIDPS carries the first transaction ID; from then on every packet has one.
        if lingo == LINGO_GENERAL and command == CMD_START_IDPS and len(rest) == 2:
            self.txids = True
        txid = None
        if self.txids and len(rest) >= 2:
            txid, rest = int.from_bytes(rest[:2], "big"), rest[2:]
        return Iap1Packet(lingo, command, txid, rest, ok, large, raw), end + 1


def _parse_iap2_link(buf: bytes) -> tuple[Frame, int]:
    if len(buf) < 9:
        return FrameError("truncated iAP2 link header", buf), len(buf)
    n = int.from_bytes(buf[2:4], "big")  # whole packet, header included
    if n < 9 or len(buf) < n:
        return FrameError(f"bad iAP2 link length {n}", buf), len(buf)
    payload = buf[9 : n - 1] if n > 9 else b""
    return Iap2Link(buf[4], buf[5], buf[6], buf[7], payload, buf[:n]), n
