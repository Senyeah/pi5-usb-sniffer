from iap_decode.hid import HS_OUT, Reassembler, parse_report
from iap_decode.iap1 import Parser, encode


def split(packet: bytes, report_id: int, count: int) -> list[bytes]:
    """Split like the relay: full reports with LCB 02/03, last one LCB 01."""
    size = count - 1
    parts = [packet[i : i + size] for i in range(0, len(packet), size)]
    out = []
    for i, part in enumerate(parts):
        lcb = (0x01 if i else 0x00) | (0x02 if i < len(parts) - 1 else 0x00)
        out.append(bytes([report_id, lcb]) + part.ljust(size, b"\0"))
    return out


def test_packet_split_across_reports():
    pkt = encode(0x00, 0x15, b"\x02\x00\x00\x01" + bytes(range(256)) * 2)
    reports = split(pkt, 0x15, HS_OUT[0x15])
    assert [r[1] for r in reports] == [0x02, 0x03, 0x01]
    r = Reassembler()
    msgs = [m for i, rep in enumerate(reports) for m in r.feed(parse_report(float(i), rep))]
    assert len(msgs) == 1 and not msgs[0].problems
    assert msgs[0].report_ids == [0x15] * 3 and msgs[0].t_first == 0.0 and msgs[0].t_last == 2.0
    [p] = Parser().parse(msgs[0].data)
    assert p.checksum_ok and p.large and len(p.data) == 4 + 512


def test_single_report():
    r = Reassembler()
    [m] = r.feed(parse_report(0.0, bytes.fromhex("0200550400020013e700")))
    assert m.data.startswith(b"\x55\x04") and not m.problems


def test_lost_last_fragment():
    r = Reassembler()
    assert r.feed(parse_report(0.0, b"\x15\x02" + bytes(254))) == []
    [lost, ok] = r.feed(parse_report(1.0, b"\x0e\x00" + encode(0x04, 0x1C)))
    assert lost.problems == ["last fragment missing"] and not ok.problems


def test_lost_first_fragment():
    [m] = Reassembler().feed(parse_report(0.0, b"\x0d\x01abcd"))
    assert m.problems == ["first fragment missing"]


def test_reset_flushes_partial():
    r = Reassembler()
    r.feed(parse_report(0.0, b"\x15\x02" + bytes(254)))
    [m] = r.reset()
    assert m.problems == ["cut by reset"]
