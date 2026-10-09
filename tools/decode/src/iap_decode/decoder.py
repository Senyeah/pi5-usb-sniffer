"""Runs the layers over the transfers of one capture and collects timeline entries."""

from __future__ import annotations

import hashlib
import struct
from collections import Counter
from pathlib import Path

from . import usbdesc as ud
from .audio import AudioStream, AudioWriter
from .hid import Message, Reassembler, parse_report
from .iap1 import FrameError, Iap1Packet, Iap2Detect, Iap2Link, Parser
from .lingoes import command_name, decode_fields, lingo_name
from .model import A2D, D2A, Entry
from .usbmon import XFER_CTRL, XFER_INT, XFER_ISO, Transfer, errno_name

ROOT_HUB = 1
CANCELLED = {-2, -104, -108}  # URB killed: unlink, reset or unplug
DEFAULT_RATE = 44100


class Decoder:
    def __init__(self, out_dir: Path | None = None, write_audio: bool = True) -> None:
        self.out_dir = out_dir
        self.entries: list[Entry] = []
        self.explained: Counter[str] = Counter()
        self.unexplained: Counter[str] = Counter()
        self.iap_counts: Counter[tuple[str, str]] = Counter()
        self.configs: dict[int, ud.Config] = {}
        self.other_speed: dict[int, ud.Config] = {}
        self.config_value: int | None = None
        self.alts: dict[int, int] = {}
        self.reasm = {A2D: Reassembler(), D2A: Reassembler()}
        self.parser = Parser()
        self.audio = AudioWriter(out_dir if write_audio else None)
        self.uac_rate: dict[int, int] = {}
        self.iap_rate: int | None = None
        self.cert_parts: dict[int, bytes] = {}
        self.certs: dict[str, str] = {}  # sha256 -> file name
        self.t_last = 0.0

    def run(self, transfers: list[Transfer]) -> list[Entry]:
        for tr in transfers:
            self._learn_descriptors(tr)
        for tr in transfers:
            self.t_last = max(self.t_last, tr.t)
            self._transfer(tr)
        self._reset(self.t_last, "capture end")
        return self.entries

    def add(self, e: Entry) -> None:
        self.entries.append(e)

    # Descriptors, read once up front so the first requests can already use them.

    def _learn_descriptors(self, tr: Transfer) -> None:
        if tr.xfer != XFER_CTRL or tr.setup is None or tr.status != 0 or tr.dev == ROOT_HUB:
            return
        s = ud.Setup.parse(tr.setup)
        if s.type != 0 or s.b_request != 0x06:
            return
        cfg = ud.parse_config(tr.data)
        if cfg is None:
            return
        (self.configs if tr.data[1] == ud.DESC_CONFIG else self.other_speed)[cfg.value] = cfg

    def _config(self) -> ud.Config | None:
        if self.config_value is None:
            return None
        return self.configs.get(self.config_value) or self.other_speed.get(self.config_value)

    def _active_interfaces(self) -> list[ud.Interface]:
        cfg = self._config()
        if cfg is None:
            return []
        return [i for i in cfg.interfaces if i.alt == self.alts.get(i.number, 0)]

    def _hid_endpoints(self) -> set[int]:
        cfg = self._config()
        if cfg is None:
            return set()
        return {ep.address for i in cfg.interfaces if i.cls == 3 for ep in i.endpoints}

    def _audio_alt(self, ep: int) -> ud.Interface | None:
        cfg = self._config()
        if cfg is None:
            return None
        for i in cfg.interfaces:
            if i.cls == 1 and i.subclass == 2 and any(e.address == ep for e in i.endpoints):
                return i
        return None

    def _audio_interface_numbers(self) -> set[int]:
        cfg = self._config()
        return {i.number for i in cfg.interfaces if i.cls == 1 and i.subclass == 2} if cfg else set()

    def _transfer(self, tr: Transfer) -> None:
        if tr.dev == ROOT_HUB:
            self._hub(tr)
        elif tr.xfer == XFER_CTRL:
            self._control(tr)
        elif tr.xfer == XFER_INT:
            self._interrupt(tr)
        elif tr.xfer == XFER_ISO:
            self._iso(tr)
        else:
            self.unexplained[f"bulk ep 0x{tr.ep:02x}"] += 1

    def _hub(self, tr: Transfer) -> None:
        if tr.xfer != XFER_CTRL or tr.setup is None:
            self.explained["root hub status"] += 1
            return
        s = ud.Setup.parse(tr.setup)
        name, fields = ud.describe_request(s, tr.data)
        if name == "hub SET_PORT_FEATURE" and fields.get("feature") in ("PORT_RESET", "BH_PORT_RESET"):
            self.explained["root hub port reset"] += 1
            self.add(Entry(tr.t, "usb", "port reset", A2D, {"port": fields["port"]}))
            self._reset(tr.t, "port reset")
            return
        if name == "hub SET_PORT_FEATURE" and fields.get("feature") in ("PORT_SUSPEND", "PORT_POWER"):
            self.add(Entry(tr.t, "usb", f"port {fields['feature']}", A2D, {"port": fields["port"]}))
        self.explained["root hub requests"] += 1

    def _control(self, tr: Transfer) -> None:
        if tr.setup is None:
            self.unexplained["control without setup"] += 1
            return
        s = ud.Setup.parse(tr.setup)
        if s.type == 1 and s.recipient == 1 and s.b_request == 0x09 and s.w_value >> 8 == 2:
            self.explained["HID SET_REPORT (iAP out)"] += 1
            if tr.status not in (0, None):
                self.add(
                    Entry(
                        tr.t,
                        "usb",
                        "SET_REPORT failed",
                        A2D,
                        {"report_id": f"0x{s.w_value & 0xFF:02x}", "status": errno_name(tr.status)},
                        tr.data,
                    )
                )
            self._report(A2D, tr.t, tr.data)
            return
        name, fields = ud.describe_request(s, tr.data)
        if tr.status != 0:
            fields["status"] = errno_name(tr.status)
        if s.type == 0 and s.b_request == 0x06 and tr.status == 0:
            fields.update(self._describe_descriptor(s, tr.data))
        if s.is_in and s.type != 0 and tr.data:
            fields["data"] = tr.data.hex()
        self.explained[f"control {name}"] += 1
        self.add(Entry(tr.t, "usb", name, D2A if s.is_in else A2D, fields, tr.data or None))
        if tr.status != 0:
            return
        if s.type == 0 and s.b_request == 0x09:
            self._reset(tr.t, f"SET_CONFIGURATION {s.w_value}")
            self.config_value = s.w_value
            self.alts.clear()
        elif s.type == 0 and s.b_request == 0x0B:
            self.alts[s.w_index] = s.w_value
            if s.w_index in self._audio_interface_numbers():
                self._audio_stop(tr.t, f"SET_INTERFACE {s.w_index} alt {s.w_value}")
        elif "rate_hz" in fields and s.recipient == 2:
            ep = s.w_index & 0xFF
            self.uac_rate[ep] = fields["rate_hz"]
            cur = self.audio.active
            if cur is not None and cur.rate != fields["rate_hz"]:
                self._audio_stop(tr.t, "sample rate change")

    def _describe_descriptor(self, s: ud.Setup, data: bytes) -> dict:
        dtype = s.w_value >> 8
        if dtype == ud.DESC_STRING and s.w_value & 0xFF:
            v = ud.parse_string(data)
            return {"value": v} if v is not None else {}
        if dtype == ud.DESC_DEVICE and len(data) >= 18:
            vid, pid, bcd = struct.unpack_from("<HHH", data, 8)
            return {"id": f"{vid:04x}:{pid:04x}", "bcdDevice": f"0x{bcd:04x}", "configurations": data[17]}
        if dtype in (ud.DESC_CONFIG, ud.DESC_OTHER_SPEED):
            cfg = ud.parse_config(data)
            if cfg is None:
                return {"bytes": len(data)}
            ifs = sorted({(i.number, i.cls) for i in cfg.interfaces})
            names = {1: "audio", 3: "HID", 6: "imaging", 0xFF: "vendor"}
            return {"bytes": len(data), "value": cfg.value, "interfaces": [f"{n}:{names.get(c, c)}" for n, c in ifs]}
        return {"bytes": len(data)}

    def _interrupt(self, tr: Transfer) -> None:
        hid = self._hid_endpoints()
        if hid and tr.ep not in hid:
            self.unexplained[f"interrupt ep 0x{tr.ep:02x} outside HID"] += 1
            return
        if tr.status is None:
            self.explained["interrupt URB pending at capture end"] += 1
        elif tr.status in CANCELLED:
            self.explained["interrupt URB cancelled"] += 1
        elif tr.status != 0:
            self.unexplained[f"interrupt {errno_name(tr.status)}"] += 1
            self.add(
                Entry(
                    tr.t,
                    "usb",
                    "interrupt error",
                    D2A if tr.is_in else A2D,
                    {"ep": f"0x{tr.ep:02x}", "status": errno_name(tr.status)},
                )
            )
        elif not tr.data:
            self.explained["interrupt zero-length"] += 1
        else:
            self.explained["HID input report (iAP in)" if tr.is_in else "HID interrupt OUT (iAP out)"] += 1
            self._report(D2A if tr.is_in else A2D, tr.t, tr.data)

    def _iso(self, tr: Transfer) -> None:
        if not tr.is_in:
            self.unexplained[f"iso OUT ep 0x{tr.ep:02x}"] += 1
            return
        if tr.status is None:
            self.explained["iso URB pending at capture end"] += 1
            return
        if tr.status != 0:
            self.explained[f"iso URB {errno_name(tr.status)}"] += 1
            return
        self.explained["iso audio URB"] += 1
        if self.audio.gap(tr.t):
            self._audio_stop(self.audio.active.t_end, "pause over 5 s in iso data")
        if self.audio.active is None:
            self._audio_start(tr.t, tr.ep)
        self.audio.feed(tr.t, tr.iso)

    def _audio_start(self, t: float, ep: int) -> None:
        alt = self._audio_alt(ep)
        fmt = alt.audio_format if alt else None
        channels, sub = (fmt.channels, fmt.subframe) if fmt else (2, 2)
        rate = self.uac_rate.get(ep) or self.iap_rate
        reason = (
            "UAC1 SET_CUR"
            if ep in self.uac_rate
            else ("iAP TrackNewAudioAttributes" if self.iap_rate else f"default {DEFAULT_RATE}")
        )
        s = self.audio.start(t, rate or DEFAULT_RATE, channels, sub, f"rate from {reason}")
        self.add(
            Entry(
                t,
                "audio",
                "stream start",
                D2A,
                {
                    "file": s.path.name if s.path else None,
                    "rate_hz": s.rate,
                    "channels": channels,
                    "bits": sub * 8,
                    "ep": f"0x{ep:02x}",
                },
            )
        )

    def _audio_stop(self, t: float, reason: str) -> None:
        s: AudioStream | None = self.audio.stop(t, reason)
        if s is not None:
            self.add(Entry(s.t_end, "audio", "stream end", D2A, s.summary()))

    def _reset(self, t: float, reason: str) -> None:
        for d, r in self.reasm.items():
            for msg in r.reset():
                self._message(d, msg)
        self.parser.reset()
        self._audio_stop(t, reason)

    def _report(self, d: str, t: float, data: bytes) -> None:
        rep = parse_report(t, data)
        if rep is None:
            self.unexplained["HID report shorter than 2 bytes"] += 1
            return
        for msg in self.reasm[d].feed(rep):
            self._message(d, msg)

    def _message(self, d: str, msg: Message) -> None:
        if msg.problems:
            ids = ",".join(f"0x{i:02x}" for i in msg.report_ids)
            self.add(
                Entry(msg.t_first, "hid", "reassembly problem", d, {"problems": msg.problems, "reports": ids}, msg.data)
            )
            self.unexplained["HID reassembly problem"] += 1
        frames = self.parser.parse(msg.data)
        if not frames:
            self.unexplained["HID message without a packet"] += 1
            self.add(Entry(msg.t_first, "hid", "message without a packet", d, {}, msg.data))
        for f in frames:
            if isinstance(f, Iap1Packet):
                self._iap1(d, msg, f)
            elif isinstance(f, Iap2Detect):
                self.add(Entry(msg.t_first, "iap2", "iAP2 detect", d, {}, f.raw))
            elif isinstance(f, Iap2Link):
                flags = [
                    n
                    for b, n in ((0x80, "SYN"), (0x40, "ACK"), (0x20, "EAK"), (0x10, "RST"), (0x08, "SLP"))
                    if f.control & b
                ]
                self.add(
                    Entry(
                        msg.t_first,
                        "iap2",
                        "link packet",
                        d,
                        {
                            "control": flags,
                            "seq": f.seq,
                            "ack": f.ack,
                            "session": f.session,
                            "payload_bytes": len(f.payload),
                        },
                        f.raw,
                    )
                )
                self.unexplained["iAP2 link packet (no iAP2 decoder yet)"] += 1
            elif isinstance(f, FrameError):
                self.add(Entry(msg.t_first, "iap1", "frame error", d, {"reason": f.reason}, f.raw))
                self.unexplained[f"iAP1 frame error: {f.reason.split(':')[0]}"] += 1

    def _iap1(self, d: str, msg: Message, p: Iap1Packet) -> None:
        lingo, cmd = lingo_name(p.lingo), command_name(p.lingo, p.command)
        name = f"{lingo}.{cmd}"
        fields = decode_fields(p.lingo, p.command, p.data)
        if p.txid is not None:
            fields = {"txid": p.txid, **fields}
        if not p.checksum_ok:
            fields["checksum"] = "BAD"
            self.unexplained["iAP1 bad checksum"] += 1
        if cmd.startswith("0x") or lingo.startswith("Lingo0x"):
            self.unexplained[f"iAP1 unknown command {name}"] += 1
        if len(msg.report_ids) > 1:
            fields["reports"] = len(msg.report_ids)
        self.iap_counts[(d, name)] += 1
        if (p.lingo, p.command) == (0x00, 0x15):
            self._cert_section(p.data, fields)
        elif (p.lingo, p.command) == (0x0A, 0x04) and "rate_hz" in fields:
            self.iap_rate = fields["rate_hz"]
        self.add(Entry(msg.t_first, "iap1", name, d, fields, p.raw))

    def _cert_section(self, data: bytes, fields: dict) -> None:
        if len(data) < 4 or data[0] < 2:
            return
        cur, last = data[2], data[3]
        if cur == 0:
            self.cert_parts.clear()
        self.cert_parts[cur] = data[4:]
        if cur != last or set(self.cert_parts) != set(range(last + 1)):
            return
        cert = b"".join(self.cert_parts[i] for i in range(last + 1))
        digest = hashlib.sha256(cert).hexdigest()
        fields["cert_sha256"] = digest[:16]
        if digest not in self.certs:
            name = f"accessory-cert-{len(self.certs) + 1}.p7b"
            self.certs[digest] = name
            if self.out_dir is not None:
                (self.out_dir / name).write_bytes(cert)
        fields["cert_file"] = self.certs[digest]
