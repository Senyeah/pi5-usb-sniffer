"""L4: iAP1 lingo and command names, and field decoders.

Apple's iAP spec is not public. These tables come from reverse engineering (Rockbox apps/iap,
oandrew/ipod). Check them against captures; unknown commands decode as hex.
"""

from __future__ import annotations

from collections.abc import Callable

LINGOES = {
    0x00: "General",
    0x01: "Microphone",
    0x02: "SimpleRemote",
    0x03: "DisplayRemote",
    0x04: "ExtendedInterface",
    0x05: "AccessoryPower",
    0x06: "USBHostMode",
    0x07: "RFTuner",
    0x08: "AccessoryEqualizer",
    0x09: "Sports",
    0x0A: "DigitalAudio",
    0x0C: "Storage",
    0x0D: "iPodOut",
    0x0E: "Location",
}

GENERAL = {
    0x00: "RequestIdentify",
    0x01: "Identify",
    0x02: "iPodAck",
    0x03: "RequestExtendedInterfaceMode",
    0x04: "ReturnExtendedInterfaceMode",
    0x05: "EnterExtendedInterfaceMode",
    0x06: "ExitExtendedInterfaceMode",
    0x07: "RequestiPodName",
    0x08: "ReturniPodName",
    0x09: "RequestiPodSoftwareVersion",
    0x0A: "ReturniPodSoftwareVersion",
    0x0B: "RequestiPodSerialNum",
    0x0C: "ReturniPodSerialNum",
    0x0D: "RequestiPodModelNum",
    0x0E: "ReturniPodModelNum",
    0x0F: "RequestLingoProtocolVersion",
    0x10: "ReturnLingoProtocolVersion",
    0x11: "RequestTransportMaxPayloadSize",
    0x12: "ReturnTransportMaxPayloadSize",
    0x13: "IdentifyDeviceLingoes",
    0x14: "GetDevAuthenticationInfo",
    0x15: "RetDevAuthenticationInfo",
    0x16: "AckDevAuthenticationInfo",
    0x17: "GetDevAuthenticationSignature",
    0x18: "RetDevAuthenticationSignature",
    0x19: "AckDevAuthenticationStatus",
    0x1A: "GetiPodAuthenticationInfo",
    0x1B: "RetiPodAuthenticationInfo",
    0x1C: "AckiPodAuthenticationInfo",
    0x1D: "GetiPodAuthenticationSignature",
    0x1E: "RetiPodAuthenticationSignature",
    0x1F: "AckiPodAuthenticationStatus",
    0x23: "NotifyiPodStateChange",
    0x24: "GetiPodOptions",
    0x25: "RetiPodOptions",
    0x27: "GetAccessoryInfo",
    0x28: "RetAccessoryInfo",
    0x29: "GetiPodPreferences",
    0x2A: "RetiPodPreferences",
    0x2B: "SetiPodPreferences",
    0x35: "GetUIMode",
    0x36: "RetUIMode",
    0x37: "SetUIMode",
    0x38: "StartIDPS",
    0x39: "SetFIDTokenValues",
    0x3A: "AckFIDTokenValues",
    0x3B: "EndIDPS",
    0x3C: "IDPSStatus",
    0x3F: "OpenDataSessionForProtocol",
    0x40: "CloseDataSession",
    0x41: "DevACK",
    0x42: "DevDataTransfer",
    0x43: "iPodDataTransfer",
    0x46: "SetAccStatusNotification",
    0x47: "RetAccStatusNotification",
    0x48: "AccessoryStatusNotification",
    0x49: "SetEventNotification",
    0x4A: "iPodNotification",
    0x4B: "GetiPodOptionsForLingo",
    0x4C: "RetiPodOptionsForLingo",
    0x4D: "GetEventNotification",
    0x4E: "RetEventNotification",
    0x4F: "GetSupportedEventNotification",
    0x50: "CancelCommand",
    0x51: "RetSupportedEventNotification",
}

SIMPLE_REMOTE = {
    0x00: "ContextButtonStatus",
    0x01: "iPodAck",
    0x0D: "ImageButtonStatus",
    0x0E: "VideoButtonStatus",
    0x0F: "AudioButtonStatus",
    0x10: "iPodOutButtonStatus",
    0x11: "RotationInputStatus",
    0x12: "RadioButtonStatus",
    0x13: "CameraButtonStatus",
}

DISPLAY_REMOTE = {
    0x00: "iPodAck",
    0x01: "GetCurrentEQProfileIndex",
    0x02: "RetCurrentEQProfileIndex",
    0x03: "SetCurrentEQProfileIndex",
    0x04: "GetNumEQProfiles",
    0x05: "RetNumEQProfiles",
    0x06: "GetIndexedEQProfileName",
    0x07: "RetIndexedEQProfileName",
    0x08: "SetRemoteEventNotification",
    0x09: "RemoteEventNotification",
    0x0A: "GetRemoteEventStatus",
    0x0B: "RetRemoteEventStatus",
    0x0C: "GetiPodStateInfo",
    0x0D: "RetiPodStateInfo",
    0x0E: "SetiPodStateInfo",
    0x0F: "GetPlayStatus",
    0x10: "RetPlayStatus",
    0x11: "SetCurrentPlayingTrack",
    0x12: "GetIndexedPlayingTrackInfo",
    0x13: "RetIndexedPlayingTrackInfo",
    0x14: "GetNumPlayingTracks",
    0x15: "RetNumPlayingTracks",
    0x16: "GetArtworkFormats",
    0x17: "RetArtworkFormats",
    0x18: "GetTrackArtworkData",
    0x19: "RetTrackArtworkData",
    0x1A: "GetPowerBatteryState",
    0x1B: "RetPowerBatteryState",
    0x1C: "GetSoundCheckState",
    0x1D: "RetSoundCheckState",
    0x1E: "SetSoundCheckState",
    0x1F: "GetTrackArtworkTimes",
    0x20: "RetTrackArtworkTimes",
}

EXTENDED = {
    0x0001: "iPodAck",
    0x0002: "GetCurrentPlayingTrackChapterInfo",
    0x0003: "ReturnCurrentPlayingTrackChapterInfo",
    0x0004: "SetCurrentPlayingTrackChapter",
    0x0005: "GetCurrentPlayingTrackChapterPlayStatus",
    0x0006: "ReturnCurrentPlayingTrackChapterPlayStatus",
    0x0007: "GetCurrentPlayingTrackChapterName",
    0x0008: "ReturnCurrentPlayingTrackChapterName",
    0x0009: "GetAudiobookSpeed",
    0x000A: "ReturnAudiobookSpeed",
    0x000B: "SetAudiobookSpeed",
    0x000C: "GetIndexedPlayingTrackInfo",
    0x000D: "ReturnIndexedPlayingTrackInfo",
    0x000E: "GetArtworkFormats",
    0x000F: "RetArtworkFormats",
    0x0010: "GetTrackArtworkData",
    0x0011: "RetTrackArtworkData",
    0x0012: "RequestProtocolVersion",
    0x0013: "ReturnProtocolVersion",
    0x0014: "RequestiPodName",
    0x0015: "ReturniPodName",
    0x0016: "ResetDBSelection",
    0x0017: "SelectDBRecord",
    0x0018: "GetNumberCategorizedDBRecords",
    0x0019: "ReturnNumberCategorizedDBRecords",
    0x001A: "RetrieveCategorizedDatabaseRecords",
    0x001B: "ReturnCategorizedDatabaseRecord",
    0x001C: "GetPlayStatus",
    0x001D: "ReturnPlayStatus",
    0x001E: "GetCurrentPlayingTrackIndex",
    0x001F: "ReturnCurrentPlayingTrackIndex",
    0x0020: "GetIndexedPlayingTrackTitle",
    0x0021: "ReturnIndexedPlayingTrackTitle",
    0x0022: "GetIndexedPlayingTrackArtistName",
    0x0023: "ReturnIndexedPlayingTrackArtistName",
    0x0024: "GetIndexedPlayingTrackAlbumName",
    0x0025: "ReturnIndexedPlayingTrackAlbumName",
    0x0026: "SetPlayStatusChangeNotification",
    0x0027: "PlayStatusChangeNotification",
    0x0028: "PlayCurrentSelection",
    0x0029: "PlayControl",
    0x002A: "GetTrackArtworkTimes",
    0x002B: "RetTrackArtworkTimes",
    0x002C: "GetShuffle",
    0x002D: "ReturnShuffle",
    0x002E: "SetShuffle",
    0x002F: "GetRepeat",
    0x0030: "ReturnRepeat",
    0x0031: "SetRepeat",
    0x0032: "SetDisplayImage",
    0x0033: "GetMonoDisplayImageLimits",
    0x0034: "ReturnMonoDisplayImageLimits",
    0x0035: "GetNumPlayingTracks",
    0x0036: "ReturnNumPlayingTracks",
    0x0037: "SetCurrentPlayingTrack",
    0x0038: "SelectSortDBRecord",
    0x0039: "GetColorDisplayImageLimits",
    0x003A: "ReturnColorDisplayImageLimits",
    0x003B: "ResetDBSelectionHierarchy",
}

DIGITAL_AUDIO = {
    0x00: "AccessoryAck",
    0x01: "iPodAck",
    0x02: "GetAccessorySampleRateCaps",
    0x03: "RetAccessorySampleRateCaps",
    0x04: "TrackNewAudioAttributes",
    0x05: "SetVideoDelay",
}

COMMANDS = {0x00: GENERAL, 0x02: SIMPLE_REMOTE, 0x03: DISPLAY_REMOTE, 0x04: EXTENDED, 0x0A: DIGITAL_AUDIO}

ACK_STATUS = {
    0x00: "success",
    0x01: "unknown database category",
    0x02: "command failed",
    0x03: "out of resources",
    0x04: "bad parameter",
    0x05: "unknown ID",
    0x06: "command pending",
    0x07: "not authenticated",
    0x08: "bad authentication version",
    0x09: "accessory power mode request failed",
    0x0A: "certificate invalid",
    0x0B: "certificate permissions invalid",
    0x0C: "file is in use",
    0x0D: "invalid file handle",
    0x0E: "directory not empty",
    0x0F: "operation timed out",
    0x10: "command unavailable in this iPod mode",
    0x11: "invalid accessory resistor ID",
    0x15: "maximum number of accessory connections reached",
}

DB_CATEGORIES = {
    0x00: "top level",
    0x01: "playlist",
    0x02: "artist",
    0x03: "album",
    0x04: "genre",
    0x05: "track",
    0x06: "composer",
    0x07: "audiobook",
    0x08: "podcast",
    0x09: "nested playlist",
    0x0A: "Genius mix",
    0x0B: "iTunes U",
}

PLAY_STATE = {0x00: "stopped", 0x01: "playing", 0x02: "paused", 0xFF: "error"}

PLAY_CONTROL = {
    0x01: "toggle play/pause",
    0x02: "stop",
    0x03: "next track",
    0x04: "previous track",
    0x05: "start fast forward",
    0x06: "start rewind",
    0x07: "end fast forward/rewind",
    0x08: "next",
    0x09: "previous",
    0x0A: "play",
    0x0B: "pause",
    0x0C: "next chapter",
    0x0D: "previous chapter",
}

PLAY_STATUS_EXT = {
    0x02: "stopped",
    0x03: "FF started",
    0x04: "REW started",
    0x05: "FF/REW stopped",
    0x0A: "playing",
    0x0B: "paused",
}

SHUFFLE = {0x00: "off", 0x01: "tracks", 0x02: "albums"}
REPEAT = {0x00: "off", 0x01: "one track", 0x02: "all tracks"}

BUTTONS = [
    "play/pause",
    "volume up",
    "volume down",
    "next track",
    "previous track",
    "next album",
    "previous album",
    "stop",
    "play",
    "pause",
    "mute",
    "next chapter",
    "previous chapter",
    "next playlist",
    "previous playlist",
    "shuffle advance",
    "repeat advance",
    "power on",
    "power off",
    "backlight 30 s",
    "begin fast forward",
    "begin rewind",
    "menu",
    "select",
    "up",
    "down",
    "backlight off",
]

TRACK_INFO = {
    0x00: "capabilities",
    0x01: "podcast name",
    0x02: "release date",
    0x03: "description",
    0x04: "lyrics",
    0x05: "genre",
    0x06: "composer",
    0x07: "artwork count",
}

ACCESSORY_INFO = {
    0x00: "capabilities",
    0x01: "name",
    0x02: "minimum iPod firmware",
    0x03: "minimum lingo version",
    0x04: "firmware version",
    0x05: "hardware version",
    0x06: "manufacturer",
    0x07: "model number",
    0x08: "serial number",
    0x09: "max payload size",
}


def lingo_name(lingo: int) -> str:
    return LINGOES.get(lingo, f"Lingo0x{lingo:02x}")


def command_name(lingo: int, command: int) -> str:
    table = COMMANDS.get(lingo, {})
    width = 4 if lingo == 0x04 else 2
    return table.get(command, f"0x{command:0{width}x}")


def lingo_mask(mask: int) -> list[str]:
    return [lingo_name(b) for b in range(32) if mask >> b & 1]


def u8(d: bytes, i: int = 0) -> int:
    return d[i]


def u16(d: bytes, i: int = 0) -> int:
    return int.from_bytes(d[i : i + 2], "big")


def u32(d: bytes, i: int = 0) -> int:
    return int.from_bytes(d[i : i + 4], "big")


def u64(d: bytes, i: int = 0) -> int:
    return int.from_bytes(d[i : i + 8], "big")


def cstr(d: bytes) -> str:
    return d.split(b"\0", 1)[0].decode("utf-8", errors="replace")


def _status(code: int) -> str:
    return ACK_STATUS.get(code, f"0x{code:02x}")


def _ack1(d: bytes, lingo: int) -> dict:
    f = {"status": _status(d[0]), "for": command_name(lingo, d[1])} if len(d) >= 2 else {}
    if len(d) >= 6 and d[0] == 0x06:
        f["wait_ms"] = u32(d, 2)
    return f


def _ext_ack(d: bytes) -> dict:
    f = {"status": _status(d[0]), "for": command_name(0x04, u16(d, 1))} if len(d) >= 3 else {}
    if len(d) >= 7 and d[0] == 0x06:
        f["wait_ms"] = u32(d, 3)
    return f


def _identify_lingoes(d: bytes) -> dict:
    f: dict = {"lingoes": lingo_mask(u32(d))}
    if len(d) >= 8:
        opts = u32(d, 4)
        f["options"] = f"0x{opts:08x}"
        f["authentication"] = {0: "none", 1: "deferred", 2: "immediate"}.get(opts & 3, "reserved")
    if len(d) >= 12:
        f["device_id"] = f"0x{u32(d, 8):08x}"
    return f


def _auth_info(d: bytes) -> dict:
    f: dict = {"version": f"{d[0]}.{d[1]}"} if len(d) >= 2 else {}
    if len(d) >= 4 and d[0] >= 2:
        f["section"] = f"{d[2]}/{d[3]}"
        f["cert_bytes"] = len(d) - 4
    return f


def _auth_challenge(d: bytes) -> dict:
    return {"challenge": d[:-1].hex(), "retry": d[-1]} if d else {}


def _accessory_info(d: bytes) -> dict:
    if not d:
        return {}
    kind = d[0]
    f: dict = {"info": ACCESSORY_INFO.get(kind, f"0x{kind:02x}")}
    v = d[1:]
    if kind in (0x01, 0x06, 0x07, 0x08):
        f["value"] = cstr(v)
    elif kind == 0x00 and len(v) >= 4:
        f["value"] = f"0x{u32(v):08x}"
    elif kind in (0x04, 0x05) and len(v) >= 3:
        f["value"] = f"{v[0]}.{v[1]}.{v[2]}"
    elif kind == 0x09 and len(v) >= 2:
        f["value"] = u16(v)
    elif v:
        f["data"] = v.hex()
    return f


def _version3(d: bytes) -> dict:
    return {"version": ".".join(str(b) for b in d[:3])}


def _lingo_version(d: bytes) -> dict:
    f: dict = {"lingo": lingo_name(d[0])} if d else {}
    if len(d) >= 3:
        f["version"] = f"{d[1]}.{d[2]}"
    return f


def _string(d: bytes) -> dict:
    return {"value": cstr(d)}


def _index_string(d: bytes) -> dict:
    return {"index": u32(d), "value": cstr(d[4:])} if len(d) >= 4 else {}


def _track_index(d: bytes) -> dict:
    return {"track": u32(d)} if len(d) >= 4 else {}


def _play_status(d: bytes) -> dict:
    if len(d) < 9:
        return {}
    return {"length_ms": u32(d), "position_ms": u32(d, 4), "state": PLAY_STATE.get(d[8], f"0x{d[8]:02x}")}


def _play_status_notify_set(d: bytes) -> dict:
    if len(d) == 1:
        return {"enable": bool(d[0])}
    if len(d) >= 4:
        return {"mask": f"0x{u32(d):08x}"}
    return {}


def _play_status_notify(d: bytes) -> dict:
    if not d:
        return {}
    kind, v = d[0], d[1:]
    names = {
        0x00: "playback stopped",
        0x01: "track index",
        0x02: "FF seek stop",
        0x03: "REW seek stop",
        0x04: "track time ms",
        0x05: "chapter index",
        0x06: "playback status",
        0x07: "track time s",
        0x08: "chapter time ms",
        0x09: "chapter time s",
        0x0A: "track UID",
        0x0B: "track playback mode",
        0x0C: "track lyrics ready",
    }
    f: dict = {"event": names.get(kind, f"0x{kind:02x}")}
    if kind in (0x01, 0x04, 0x05, 0x07, 0x08, 0x09) and len(v) >= 4:
        f["value"] = u32(v)
    elif kind == 0x06 and v:
        f["value"] = PLAY_STATUS_EXT.get(v[0], f"0x{v[0]:02x}")
    elif kind == 0x0A and len(v) >= 8:
        f["value"] = f"0x{u64(v):016x}"
    elif v:
        f["data"] = v.hex()
    return f


def _audiobook_speed(d: bytes) -> dict:
    if not d:
        return {}
    v = int.from_bytes(d[:1], "big", signed=True)
    return {"speed": {-1: "slower", 0: "normal", 1: "faster"}.get(v, v)}


def _chapter_info(d: bytes) -> dict:
    if len(d) < 8:
        return {}
    return {"chapter": int.from_bytes(d[:4], "big", signed=True), "chapters": u32(d, 4)}


def _play_control(d: bytes) -> dict:
    return {"action": PLAY_CONTROL.get(d[0], f"0x{d[0]:02x}")} if d else {}


def _category(d: bytes) -> dict:
    return {"category": DB_CATEGORIES.get(d[0], f"0x{d[0]:02x}")} if d else {}


def _select_record(d: bytes) -> dict:
    return {"category": DB_CATEGORIES.get(d[0], f"0x{d[0]:02x}"), "index": u32(d, 1)} if len(d) >= 5 else {}


def _retrieve_records(d: bytes) -> dict:
    if len(d) < 9:
        return {}
    return {"category": DB_CATEGORIES.get(d[0], f"0x{d[0]:02x}"), "start": u32(d, 1), "count": u32(d, 5)}


def _count(d: bytes) -> dict:
    return {"count": u32(d)} if len(d) >= 4 else {}


def _indexed_info_get(d: bytes) -> dict:
    if len(d) < 7:
        return {}
    return {"info": TRACK_INFO.get(d[0], d[0]), "track": u32(d, 1), "chapter": u16(d, 5)}


def _indexed_info_ret(d: bytes) -> dict:
    if not d:
        return {}
    kind, v = d[0], d[1:]
    f: dict = {"info": TRACK_INFO.get(kind, kind)}
    if kind == 0x00 and len(v) >= 10:
        f.update(capabilities=f"0x{u32(v):08x}", length_ms=u32(v, 4), chapters=u16(v, 8))
    elif kind in (0x01, 0x05, 0x06):
        f["value"] = cstr(v)
    elif kind in (0x03, 0x04) and len(v) >= 3:
        f.update(section=v[1], value=cstr(v[3:]))
    elif v:
        f["data"] = v.hex()
    return f


def _enum1(table: dict) -> Callable[[bytes], dict]:
    def dec(d: bytes) -> dict:
        return {"value": table.get(d[0], f"0x{d[0]:02x}")} if d else {}

    return dec


def _set_enum1(table: dict) -> Callable[[bytes], dict]:
    def dec(d: bytes) -> dict:
        f = {"value": table.get(d[0], f"0x{d[0]:02x}")} if d else {}
        if len(d) >= 2:
            f["restore_on_exit"] = bool(d[1])
        return f

    return dec


def _protocol_version(d: bytes) -> dict:
    return {"version": f"{d[0]}.{d[1]}"} if len(d) >= 2 else {}


def _mono_limits(d: bytes) -> dict:
    return {"width": u16(d), "height": u16(d, 2), "format": d[4]} if len(d) >= 5 else {}


def _color_limits(d: bytes) -> dict:
    return {
        "formats": [{"width": u16(d, i), "height": u16(d, i + 2), "format": d[i + 4]} for i in range(0, len(d) - 4, 5)]
    }


def _artwork_formats(d: bytes) -> dict:
    return {
        "formats": [
            {"id": u16(d, i), "pixel_format": d[i + 2], "width": u16(d, i + 3), "height": u16(d, i + 5)}
            for i in range(0, len(d) - 6, 7)
        ]
    }


def _buttons(d: bytes) -> dict:
    mask = int.from_bytes(d, "little")
    pressed = [BUTTONS[b] if b < len(BUTTONS) else f"bit{b}" for b in range(len(d) * 8) if mask >> b & 1]
    return {"buttons": pressed or ["released"]}


def _sample_rates(d: bytes) -> dict:
    return {"rates_hz": [u32(d, i) for i in range(0, len(d) - 3, 4)]}


def _audio_attributes(d: bytes) -> dict:
    if len(d) < 4:
        return {}
    f: dict = {"rate_hz": u32(d)}
    if len(d) >= 12:
        f["sound_check"] = u32(d, 4)
        f["volume_adjust"] = u32(d, 8)
    return f


def _u64_mask(d: bytes) -> dict:
    return {"mask": f"0x{u64(d):016x}"} if len(d) >= 8 else {}


def _lingo_options(d: bytes) -> dict:
    f: dict = {"lingo": lingo_name(d[0])} if d else {}
    if len(d) >= 9:
        f["options"] = f"0x{u64(d, 1):016x}"
    return f


def _status1(d: bytes) -> dict:
    return {"status": _status(d[0])} if d else {}


def _lingo1(d: bytes) -> dict:
    return {"lingo": lingo_name(d[0])} if d else {}


def _u16_value(d: bytes) -> dict:
    return {"value": u16(d)} if len(d) >= 2 else {}


def _u32_ms(d: bytes) -> dict:
    return {"delay_ms": u32(d)} if len(d) >= 4 else {}


def _preferences(d: bytes) -> dict:
    f: dict = {"class": d[0]} if d else {}
    if len(d) >= 2:
        f["setting"] = d[1]
    if len(d) >= 3:
        f["restore_on_exit"] = bool(d[2])
    return f


def _signature(d: bytes) -> dict:
    return {"signature_bytes": len(d)}


DECODERS: dict[tuple[int, int], Callable[[bytes], dict]] = {
    (0x00, 0x01): _lingo1,
    (0x00, 0x02): lambda d: _ack1(d, 0x00),
    (0x00, 0x04): lambda d: {"mode": d[0]} if d else {},
    (0x00, 0x08): _string,
    (0x00, 0x0A): _version3,
    (0x00, 0x0C): _string,
    (0x00, 0x0E): _string,
    (0x00, 0x0F): _lingo1,
    (0x00, 0x10): _lingo_version,
    (0x00, 0x12): _u16_value,
    (0x00, 0x13): _identify_lingoes,
    (0x00, 0x15): _auth_info,
    (0x00, 0x16): _status1,
    (0x00, 0x17): _auth_challenge,
    (0x00, 0x18): _signature,
    (0x00, 0x19): _status1,
    (0x00, 0x1B): _auth_info,
    (0x00, 0x1C): _status1,
    (0x00, 0x1D): _auth_challenge,
    (0x00, 0x1E): _signature,
    (0x00, 0x1F): _status1,
    (0x00, 0x23): lambda d: {"state": d[0]} if d else {},
    (0x00, 0x25): _u64_mask,
    (0x00, 0x27): lambda d: {"info": ACCESSORY_INFO.get(d[0], f"0x{d[0]:02x}")} if d else {},
    (0x00, 0x28): _accessory_info,
    (0x00, 0x29): lambda d: {"class": d[0]} if d else {},
    (0x00, 0x2A): _preferences,
    (0x00, 0x2B): _preferences,
    (0x00, 0x36): lambda d: {"mode": d[0]} if d else {},
    (0x00, 0x37): lambda d: {"mode": d[0]} if d else {},
    (0x00, 0x3B): _status1,
    (0x00, 0x3C): _status1,
    (0x00, 0x41): lambda d: _ack1(d, 0x00),
    (0x00, 0x49): _u64_mask,
    (0x00, 0x4B): _lingo1,
    (0x00, 0x4C): _lingo_options,
    (0x02, 0x00): _buttons,
    (0x02, 0x01): lambda d: _ack1(d, 0x02),
    (0x02, 0x0F): _buttons,
    (0x03, 0x00): lambda d: _ack1(d, 0x03),
    (0x04, 0x0001): _ext_ack,
    (0x04, 0x0003): _chapter_info,
    (0x04, 0x000A): _audiobook_speed,
    (0x04, 0x000B): _audiobook_speed,
    (0x04, 0x000C): _indexed_info_get,
    (0x04, 0x000D): _indexed_info_ret,
    (0x04, 0x000F): _artwork_formats,
    (0x04, 0x0013): _protocol_version,
    (0x04, 0x0015): _string,
    (0x04, 0x0017): _select_record,
    (0x04, 0x0018): _category,
    (0x04, 0x0019): _count,
    (0x04, 0x001A): _retrieve_records,
    (0x04, 0x001B): _index_string,
    (0x04, 0x001D): _play_status,
    (0x04, 0x001F): _track_index,
    (0x04, 0x0020): _track_index,
    (0x04, 0x0021): _string,
    (0x04, 0x0022): _track_index,
    (0x04, 0x0023): _string,
    (0x04, 0x0024): _track_index,
    (0x04, 0x0025): _string,
    (0x04, 0x0026): _play_status_notify_set,
    (0x04, 0x0027): _play_status_notify,
    (0x04, 0x0028): _track_index,
    (0x04, 0x0029): _play_control,
    (0x04, 0x002D): _enum1(SHUFFLE),
    (0x04, 0x002E): _set_enum1(SHUFFLE),
    (0x04, 0x0030): _enum1(REPEAT),
    (0x04, 0x0031): _set_enum1(REPEAT),
    (0x04, 0x0034): _mono_limits,
    (0x04, 0x0036): _count,
    (0x04, 0x0037): _track_index,
    (0x04, 0x0038): _select_record,
    (0x04, 0x003A): _color_limits,
    (0x0A, 0x00): lambda d: _ack1(d, 0x0A),
    (0x0A, 0x01): lambda d: _ack1(d, 0x0A),
    (0x0A, 0x03): _sample_rates,
    (0x0A, 0x04): _audio_attributes,
    (0x0A, 0x05): _u32_ms,
}


def decode_fields(lingo: int, command: int, data: bytes) -> dict:
    dec = DECODERS.get((lingo, command))
    if dec is None:
        return {"data": data.hex()} if data else {}
    try:
        return dec(data)
    except IndexError:
        return {"error": "too short", "data": data.hex()}
