from iap_decode.decoder import Decoder
from iap_decode.iap1 import encode
from iap_decode.model import A2D, D2A
from iap_decode.usbmon import XFER_CTRL, XFER_INT, XFER_ISO, Transfer


def set_report(t: float, data: bytes) -> Transfer:
    setup = bytes([0x21, 0x09, data[0], 0x02, 0x02, 0x00, len(data), 0x00])
    return Transfer(t, t + 0.001, XFER_CTRL, 0x00, 3, 1, setup, data, 0, len(data))


def int_in(t: float, data: bytes) -> Transfer:
    return Transfer(t, t, XFER_INT, 0x83, 3, 1, None, data, 0, len(data))


def iso_in(t: float, n: int) -> Transfer:
    return Transfer(t, t, XFER_ISO, 0x81, 3, 1, None, b"", 0, 0, iso=[(0, bytes(176))] * n)


def test_iap_exchange():
    identify = encode(0x00, 0x13, bytes.fromhex("000004190000000200000200"))
    ack = encode(0x00, 0x02, b"\x00\x13")
    transfers = [
        set_report(1.0, b"\x10\x00" + identify.ljust(17, b"\0")),
        int_in(1.01, b"\x02\x00" + ack.ljust(8, b"\0")),
    ]
    dec = Decoder(None)
    entries = [e for e in dec.run(transfers) if e.layer == "iap1"]
    assert [(e.dir, e.name) for e in entries] == [(A2D, "General.IdentifyDeviceLingoes"), (D2A, "General.iPodAck")]
    assert entries[1].fields == {"status": "success", "for": "IdentifyDeviceLingoes"}
    assert not dec.unexplained


def test_audio_pause_is_filled_with_silence():
    transfers = [iso_in(1.0 + i * 0.008, 8) for i in range(10)] + [iso_in(2.0, 8)]
    dec = Decoder(None)
    dec.run(transfers)
    [s] = dec.audio.streams
    assert s.gaps == 1 and 0.9 < s.gap_s < 0.93
    assert abs(s.audio_s - (88 * 44 / 44100 + s.gap_s)) < 0.01  # 44 frames per 176-byte packet


def test_long_pause_splits_stream():
    dec = Decoder(None)
    dec.run([iso_in(1.0, 8), iso_in(10.0, 8)])
    assert len(dec.audio.streams) == 2
