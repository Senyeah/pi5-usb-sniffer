from iap_decode.lingoes import command_name, decode_fields, lingo_mask


def test_names():
    assert command_name(0x00, 0x13) == "IdentifyDeviceLingoes"
    assert command_name(0x04, 0x001D) == "ReturnPlayStatus"
    assert command_name(0x04, 0x7777) == "0x7777"
    assert lingo_mask(0x00000419) == ["General", "DisplayRemote", "ExtendedInterface", "DigitalAudio"]


def test_identify_device_lingoes():
    f = decode_fields(0x00, 0x13, bytes.fromhex("0000041900000002" + "00000200"))
    assert f["lingoes"] == ["General", "DisplayRemote", "ExtendedInterface", "DigitalAudio"]
    assert f["authentication"] == "immediate" and f["device_id"] == "0x00000200"


def test_play_status():
    f = decode_fields(0x04, 0x001D, (340373).to_bytes(4, "big") + (5274).to_bytes(4, "big") + b"\x02")
    assert f == {"length_ms": 340373, "position_ms": 5274, "state": "paused"}


def test_ack_pending():
    f = decode_fields(0x00, 0x02, b"\x06\x05" + (3000).to_bytes(4, "big"))
    assert f == {"status": "command pending", "for": "EnterExtendedInterfaceMode", "wait_ms": 3000}


def test_title_string():
    assert decode_fields(0x04, 0x0021, b"Cola" + b"\0") == {"value": "Cola"}


def test_unknown_command_is_hex():
    assert decode_fields(0x00, 0x99, b"\x01\x02") == {"data": "0102"}


def test_short_data_does_not_raise():
    assert decode_fields(0x00, 0x02, b"") == {}
