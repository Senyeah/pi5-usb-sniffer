"""USB descriptors and control request names."""

from __future__ import annotations

import struct
from dataclasses import dataclass, field

DESC_DEVICE, DESC_CONFIG, DESC_STRING, DESC_INTERFACE, DESC_ENDPOINT = 1, 2, 3, 4, 5
DESC_QUALIFIER, DESC_OTHER_SPEED, DESC_BOS = 6, 7, 0x0F
DESC_HID, DESC_HID_REPORT = 0x21, 0x22
CS_INTERFACE = 0x24

DESC_NAMES = {
    DESC_DEVICE: "device",
    DESC_CONFIG: "configuration",
    DESC_STRING: "string",
    DESC_INTERFACE: "interface",
    DESC_ENDPOINT: "endpoint",
    DESC_QUALIFIER: "device qualifier",
    DESC_OTHER_SPEED: "other-speed configuration",
    DESC_BOS: "BOS",
    DESC_HID: "HID",
    DESC_HID_REPORT: "HID report",
}

STD_REQUESTS = {
    0x00: "GET_STATUS",
    0x01: "CLEAR_FEATURE",
    0x03: "SET_FEATURE",
    0x05: "SET_ADDRESS",
    0x06: "GET_DESCRIPTOR",
    0x07: "SET_DESCRIPTOR",
    0x08: "GET_CONFIGURATION",
    0x09: "SET_CONFIGURATION",
    0x0A: "GET_INTERFACE",
    0x0B: "SET_INTERFACE",
    0x0C: "SYNCH_FRAME",
}
HID_REQUESTS = {
    0x01: "GET_REPORT",
    0x02: "GET_IDLE",
    0x03: "GET_PROTOCOL",
    0x09: "SET_REPORT",
    0x0A: "SET_IDLE",
    0x0B: "SET_PROTOCOL",
}
UAC1_REQUESTS = {
    0x01: "SET_CUR",
    0x02: "SET_MIN",
    0x03: "SET_MAX",
    0x04: "SET_RES",
    0x81: "GET_CUR",
    0x82: "GET_MIN",
    0x83: "GET_MAX",
    0x84: "GET_RES",
}
UAC1_EP_SELECTORS = {0x01: "sampling frequency", 0x02: "pitch"}
HID_REPORT_TYPES = {1: "input", 2: "output", 3: "feature"}
# Apple vendor requests; names from apple-mfi-fastcharge.c and the Phase 3 captures.
APPLE_VENDOR = {0x40: "Apple set charge current", 0x45: "Apple get USB mode", 0x52: "Apple set USB mode"}
HUB_PORT_FEATURES = {
    0: "PORT_CONNECTION",
    1: "PORT_ENABLE",
    2: "PORT_SUSPEND",
    4: "PORT_RESET",
    8: "PORT_POWER",
    16: "C_PORT_CONNECTION",
    17: "C_PORT_ENABLE",
    18: "C_PORT_SUSPEND",
    20: "C_PORT_RESET",
    5: "PORT_LINK_STATE",
    29: "BH_PORT_RESET",
}


@dataclass
class Setup:
    bm_request_type: int
    b_request: int
    w_value: int
    w_index: int
    w_length: int

    @classmethod
    def parse(cls, raw: bytes) -> Setup:
        return cls(*struct.unpack("<BBHHH", raw))

    @property
    def is_in(self) -> bool:
        return bool(self.bm_request_type & 0x80)

    @property
    def type(self) -> int:  # 0 standard, 1 class, 2 vendor
        return (self.bm_request_type >> 5) & 3

    @property
    def recipient(self) -> int:  # 0 device, 1 interface, 2 endpoint, 3 other
        return self.bm_request_type & 0x1F


@dataclass
class Endpoint:
    address: int
    attributes: int
    max_packet: int
    interval: int

    @property
    def xfer(self) -> int:  # 0 control, 1 iso, 2 bulk, 3 interrupt (USB numbering)
        return self.attributes & 3


@dataclass
class AudioFormat:
    channels: int
    subframe: int
    bits: int
    rates: list[int]


@dataclass
class Interface:
    number: int
    alt: int
    cls: int
    subclass: int
    protocol: int
    endpoints: list[Endpoint] = field(default_factory=list)
    audio_format: AudioFormat | None = None
    hid_report_length: int | None = None


@dataclass
class Config:
    value: int
    interfaces: list[Interface]

    def interface(self, number: int, alt: int) -> Interface | None:
        for i in self.interfaces:
            if i.number == number and i.alt == alt:
                return i
        return None


def parse_config(raw: bytes) -> Config | None:
    if len(raw) < 9 or raw[1] not in (DESC_CONFIG, DESC_OTHER_SPEED):
        return None
    total = struct.unpack_from("<H", raw, 2)[0]
    if len(raw) < total:
        return None  # header-only read
    cfg = Config(value=raw[5], interfaces=[])
    cur: Interface | None = None
    off = raw[0]
    while off + 2 <= total:
        dlen, dtype = raw[off], raw[off + 1]
        if dlen < 2:
            break
        d = raw[off : off + dlen]
        if dtype == DESC_INTERFACE and dlen >= 9:
            cur = Interface(d[2], d[3], d[5], d[6], d[7])
            cfg.interfaces.append(cur)
        elif dtype == DESC_ENDPOINT and dlen >= 7 and cur is not None:
            cur.endpoints.append(Endpoint(d[2], d[3], struct.unpack_from("<H", d, 4)[0] & 0x7FF, d[6]))
        elif dtype == CS_INTERFACE and cur is not None and cur.cls == 1 and cur.subclass == 2:
            if dlen >= 8 and d[2] == 0x02 and d[3] == 1:  # FORMAT_TYPE, type I
                n = d[7]
                rates = [int.from_bytes(d[8 + 3 * i : 11 + 3 * i], "little") for i in range(n)] if n else []
                if n == 0 and dlen >= 14:
                    rates = [int.from_bytes(d[8:11], "little"), int.from_bytes(d[11:14], "little")]
                cur.audio_format = AudioFormat(d[4], d[5], d[6], rates)
        elif dtype == DESC_HID and cur is not None and dlen >= 9:
            cur.hid_report_length = struct.unpack_from("<H", d, 7)[0]
        off += dlen
    return cfg


def parse_string(raw: bytes) -> str | None:
    if len(raw) < 2 or raw[1] != DESC_STRING:
        return None
    return raw[2 : raw[0]].decode("utf-16-le", errors="replace").rstrip("\0")


def describe_request(s: Setup, data: bytes) -> tuple[str, dict]:
    """Name a control request and decode its main fields."""
    fields: dict = {}
    if s.type == 0:
        name = STD_REQUESTS.get(s.b_request, f"standard 0x{s.b_request:02x}")
        if s.b_request == 0x06:
            dtype, index = s.w_value >> 8, s.w_value & 0xFF
            fields["descriptor"] = DESC_NAMES.get(dtype, f"0x{dtype:02x}")
            fields["index"] = index
            if dtype == DESC_STRING and index:
                fields["lang"] = f"0x{s.w_index:04x}"
            if s.recipient == 1:
                fields["interface"] = s.w_index
            fields["w_length"] = s.w_length
        elif s.b_request == 0x09:
            fields["configuration"] = s.w_value
        elif s.b_request == 0x0B:
            fields["interface"] = s.w_index
            fields["alt"] = s.w_value
        elif s.b_request in (0x01, 0x03):
            fields["feature"] = s.w_value
            fields["index"] = s.w_index
        elif s.b_request == 0x05:
            fields["address"] = s.w_value
        return name, fields
    if s.type == 1:
        if s.b_request in (0x01, 0x09) and s.recipient == 1 and (s.w_value >> 8) in HID_REPORT_TYPES:
            name = HID_REQUESTS[s.b_request]
            fields["report_type"] = HID_REPORT_TYPES[s.w_value >> 8]
            fields["report_id"] = f"0x{s.w_value & 0xFF:02x}"
            fields["interface"] = s.w_index
            return name, fields
        if s.recipient == 2 and s.b_request in UAC1_REQUESTS:
            name = "UAC1 " + UAC1_REQUESTS[s.b_request]
            sel = s.w_value >> 8
            fields["control"] = UAC1_EP_SELECTORS.get(sel, f"0x{sel:02x}")
            fields["endpoint"] = f"0x{s.w_index & 0xFF:02x}"
            if sel == 0x01 and len(data) >= 3:
                fields["rate_hz"] = int.from_bytes(data[:3], "little")
            return name, fields
        if s.recipient == 3 and s.b_request in (0x00, 0x01, 0x03):  # hub port
            name = {0x00: "hub GET_PORT_STATUS", 0x01: "hub CLEAR_PORT_FEATURE", 0x03: "hub SET_PORT_FEATURE"}[
                s.b_request
            ]
            fields["port"] = s.w_index & 0xFF
            if s.b_request != 0x00:
                fields["feature"] = HUB_PORT_FEATURES.get(s.w_value, s.w_value)
            elif len(data) >= 4:
                status, change = struct.unpack_from("<HH", data)
                fields["status"] = f"0x{status:04x}"
                fields["change"] = f"0x{change:04x}"
            return name, fields
        if s.recipient == 0 and s.b_request in (0x00, 0x06):
            return ("hub GET_HUB_STATUS" if s.b_request == 0 else "hub GET_HUB_DESCRIPTOR"), fields
        name = f"class 0x{s.bm_request_type:02x}/0x{s.b_request:02x}"
        return name, _raw_fields(s)
    if s.type == 2:
        name = APPLE_VENDOR.get(s.b_request, f"vendor 0x{s.bm_request_type:02x}/0x{s.b_request:02x}")
        fields = _raw_fields(s)
        if s.b_request == 0x40:
            fields["extra_ma"] = s.w_value
            fields["extra_ma_sleep"] = s.w_index
        return name, fields
    return f"reserved 0x{s.bm_request_type:02x}/0x{s.b_request:02x}", _raw_fields(s)


def _raw_fields(s: Setup) -> dict:
    return {"w_value": f"0x{s.w_value:04x}", "w_index": f"0x{s.w_index:04x}", "w_length": s.w_length}
