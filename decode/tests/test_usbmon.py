import struct

from iap_decode.usbmon import LINKTYPE_USB_LINUX_MMAPPED, XFER_CTRL, XFER_ISO, pair, read_events


def block(btype: int, body: bytes) -> bytes:
    body += bytes(-len(body) % 4)
    n = len(body) + 12
    return struct.pack("<II", btype, n) + body + struct.pack("<I", n)


def usbmon(urb, kind, xfer, ep, dev, ts, status, length, data=b"", setup=None, iso=()):
    payload = b"".join(struct.pack("<iIII", s, o, n, 0) for s, o, n in iso) + data
    setup_raw = setup if setup is not None else (struct.pack("<ii", 0, len(iso)) if iso else bytes(8))
    hdr = struct.pack(
        "<QBBBBHbbqiiII8siiII",
        urb,
        ord(kind),
        xfer,
        ep,
        dev,
        1,
        0 if setup is not None else ord("-"),
        0 if data else ord("<"),
        int(ts),
        int(round(ts % 1 * 1e6)),
        status,
        length,
        len(payload),
        setup_raw,
        0,
        0,
        0,
        len(iso),
    )
    return hdr + payload


def pcapng(records: list[bytes]) -> bytes:
    shb = block(0x0A0D0D0A, struct.pack("<IHHq", 0x1A2B3C4D, 1, 0, -1))
    idb = block(1, struct.pack("<HHI", LINKTYPE_USB_LINUX_MMAPPED, 0, 262144))
    epbs = b"".join(block(6, struct.pack("<IIIII", 0, 0, 0, len(r), len(r)) + r) for r in records)
    return shb + idb + epbs


def test_control_out_and_iso_in(tmp_path):
    setup = bytes.fromhex("21090e0202000a00")
    report = bytes.fromhex("0e005503000f00ee0000")
    audio = bytes(range(176)) * 2
    recs = [
        usbmon(1, "S", XFER_CTRL, 0x00, 3, 100.0, -115, 10, report, setup),
        usbmon(1, "C", XFER_CTRL, 0x00, 3, 100.001, 0, 10),
        usbmon(2, "S", XFER_ISO, 0x81, 3, 100.002, -115, 384, iso=[(-18, 0, 192), (-18, 192, 192)]),
        usbmon(
            2,
            "C",
            XFER_ISO,
            0x81,
            3,
            100.004,
            0,
            352,
            audio[:176] + bytes(16) + audio[176:352],
            iso=[(0, 0, 176), (0, 192, 176)],
        ),
    ]
    path = tmp_path / "t.pcapng"
    path.write_bytes(pcapng(recs))
    events = list(read_events(path))
    assert [e.kind for e in events] == ["S", "C", "S", "C"]
    assert events[0].setup == setup and events[0].data == report
    ctrl, iso = list(pair(events))
    assert ctrl.data == report and ctrl.status == 0 and not ctrl.is_in and ctrl.t == 100.0
    assert iso.is_in and iso.t == 100.004
    assert iso.iso == [(0, audio[:176]), (0, audio[176:352])]


def test_unfinished_urb_is_kept(tmp_path):
    path = tmp_path / "t.pcapng"
    path.write_bytes(pcapng([usbmon(9, "S", 1, 0x83, 3, 5.0, -115, 64)]))
    [tr] = list(pair(read_events(path)))
    assert tr.status is None and tr.t_done is None
