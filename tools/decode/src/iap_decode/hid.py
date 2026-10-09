"""L1: Apple iAP-over-HID reports. Report = [ID][link control byte][payload, zero-padded]."""

from __future__ import annotations

from dataclasses import dataclass, field

LCB_CONTINUATION = 0x01  # this report continues the previous one
LCB_MORE = 0x02  # more reports follow

# Report counts (LCB + payload) per ID, from the HID report descriptors (PLAN.md R14).
HS_IN = dict(zip(range(0x01, 0x0D), (5, 9, 13, 17, 25, 49, 95, 193, 257, 385, 513, 767), strict=True))
HS_OUT = dict(zip(range(0x0D, 0x16), (5, 9, 13, 17, 25, 49, 95, 193, 255), strict=True))
FS_IN = dict(zip(range(0x01, 0x05), (12, 14, 20, 63), strict=True))
FS_OUT = dict(zip(range(0x05, 0x0A), (8, 10, 14, 20, 63), strict=True))


@dataclass
class Report:
    t: float
    report_id: int
    lcb: int
    payload: bytes


@dataclass
class Message:
    """The payloads of one LCB sequence, joined. May still hold padding at the end."""

    t_first: float
    t_last: float
    data: bytes
    report_ids: list[int] = field(default_factory=list)
    problems: list[str] = field(default_factory=list)


def parse_report(t: float, data: bytes) -> Report | None:
    if len(data) < 2:
        return None
    return Report(t, data[0], data[1], data[2:])


class Reassembler:
    """Joins reports of one direction into messages."""

    def __init__(self) -> None:
        self._cur: Message | None = None

    def feed(self, rep: Report) -> list[Message]:
        out: list[Message] = []
        cont = bool(rep.lcb & LCB_CONTINUATION)
        if not cont and self._cur is not None:
            self._cur.problems.append("last fragment missing")
            out.append(self._cur)
            self._cur = None
        if self._cur is None:
            self._cur = Message(rep.t, rep.t, b"")
            if cont:
                self._cur.problems.append("first fragment missing")
        self._cur.data += rep.payload
        self._cur.t_last = rep.t
        self._cur.report_ids.append(rep.report_id)
        if rep.lcb & ~(LCB_CONTINUATION | LCB_MORE):
            self._cur.problems.append(f"unknown LCB bits 0x{rep.lcb:02x}")
        if not rep.lcb & LCB_MORE:
            out.append(self._cur)
            self._cur = None
        return out

    def reset(self) -> list[Message]:
        """Flush a partial message, e.g. at a USB reset."""
        if self._cur is None:
            return []
        self._cur.problems.append("cut by reset")
        msg, self._cur = self._cur, None
        return [msg]
