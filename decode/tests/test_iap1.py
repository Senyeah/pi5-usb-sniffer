from iap_decode.iap1 import FrameError, Iap1Packet, Iap2Detect, Parser, checksum, encode

ACK_IDENTIFY = bytes.fromhex("550400020013e7")  # iPodAck for IdentifyDeviceLingoes, from car-10


def test_small_packet():
    [p] = Parser().parse(ACK_IDENTIFY)
    assert isinstance(p, Iap1Packet)
    assert (p.lingo, p.command, p.data, p.checksum_ok, p.large) == (0x00, 0x02, b"\x00\x13", True, False)


def test_encode_matches_capture():
    assert encode(0x00, 0x02, b"\x00\x13") == ACK_IDENTIFY


def test_large_packet():
    raw = encode(0x00, 0x15, bytes(500))
    assert raw[1] == 0 and int.from_bytes(raw[2:4], "big") == 502
    [p] = Parser().parse(raw)
    assert p.large and p.checksum_ok and len(p.data) == 500


def test_bad_checksum():
    raw = bytearray(ACK_IDENTIFY)
    raw[-1] ^= 0xFF
    [p] = Parser().parse(bytes(raw))
    assert not p.checksum_ok


def test_extended_lingo_has_2_byte_command():
    [p] = Parser().parse(encode(0x04, 0x001C))
    assert (p.lingo, p.command, p.data) == (0x04, 0x001C, b"")


def test_padding_and_two_packets():
    buf = encode(0x04, 0x001C) + encode(0x00, 0x14) + bytes(10)
    frames = Parser().parse(buf)
    assert [(f.lingo, f.command) for f in frames] == [(0x04, 0x001C), (0x00, 0x14)]


def test_sync_byte_is_skipped():
    [p] = Parser().parse(b"\xff" + ACK_IDENTIFY)
    assert isinstance(p, Iap1Packet) and p.checksum_ok


def test_truncated_packet():
    [f] = Parser().parse(ACK_IDENTIFY[:-2])
    assert isinstance(f, FrameError) and f.reason.startswith("truncated")


def test_garbage_after_packet():
    frames = Parser().parse(ACK_IDENTIFY + b"\x01\x02")
    assert isinstance(frames[-1], FrameError)


def test_iap2_detect():
    [f] = Parser().parse(bytes.fromhex("ff550200ee10"))
    assert isinstance(f, Iap2Detect)


def test_transaction_ids_after_start_idps():
    parser = Parser()
    [start] = parser.parse(encode(0x00, 0x38, b"\x00\x01"))
    assert start.txid == 1 and start.data == b""
    [ack] = parser.parse(encode(0x00, 0x02, b"\x00\x38", txid=1))
    assert ack.txid == 1 and ack.data == b"\x00\x38"
    parser.reset()
    [plain] = parser.parse(ACK_IDENTIFY)
    assert plain.txid is None


def test_checksum():
    assert checksum(bytes.fromhex("0400020013")) == 0xE7
