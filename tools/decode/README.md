# Decoder (`tools/decode`)

A Python tool (uv project, no runtime dependencies) that decodes the relay's usbmon captures offline: USB, HID, iAP1 and the audio. It explained all 27,174 transfers of `car-10`, and that decode is the reference for the Bluetooth bridge and the Pi 3 test stereo. `latency/analyze.py` in the repository root reuses its usbmon reader. This folder was `decode/` before 09/10/2026.

## 1. Use

```bash
uv run --project tools/decode iap-decode sessions/<session-dir>
```

It accepts a session folder or pcapng files. Options: `-o DIR` sets the output folder, and `--no-audio` skips the WAV files. A run on `car-10` (49 MB) takes about 2 s.

Output goes to `<session-dir>/decoded/`:

| File | Content |
|---|---|
| `timeline.txt` | One line per event: UTC time, seconds from start, direction, layer, name, fields. A summary follows the events. |
| `timeline.jsonl` | The same events, with raw bytes in hex |
| `summary.json` | Explained and unexplained transfer counts, iAP1 command counts per direction, audio streams, relay events |
| `audio-NN.wav` | One file per audio stream |
| `accessory-cert-N.p7b` | The stereo's MFi certificate, PKCS#7 DER, joined from its sections. **Private.** |

Directions: `A>D` is host side to Apple device, and `D>A` is the reverse. "Host side" is the stereo, or the relay itself: the relay sends its own descriptor requests at start. Layers: `usb`, `hid`, `iap1`, `iap2`, `audio`, `relay` (from `proxy.log`), `udc` (from `udc.log`), `mark`.

Tests and lint:

```bash
uv run --project tools/decode pytest tools/decode/tests
```

```bash
uvx ruff check tools/decode
```

## 2. Modules and limits

| Module | Layer | Notes |
|---|---|---|
| `usbmon.py` | L0 | Reads pcapng blocks and the 64-byte usbmon header (link type 220; 189 is also handled) without `tshark`. `tshark` is not installed on the Mac. Pairs submit and complete events by URB ID. For iso, it splits the data with the iso descriptors. A transfer's time is the submit time for OUT data and the completion time for IN data. |
| `usbdesc.py` | — | Parses configuration descriptors, string descriptors and setup packets. Names standard, HID, UAC1, hub and Apple vendor requests. |
| `hid.py` | L1 | Report = `[ID][LCB][payload]`. Reassembles by LCB per direction. Reports lost fragments. Report lengths come from the data, so no table is needed to decode. |
| `iap1.py` | L2, L3 | Finds iAP1 packets, the iAP2 detect sequence and iAP2 link packets. Checks checksums. Tracks the transaction-ID state: IDs start after `StartIDPS`. Ignores zero padding. |
| `lingoes.py` | L4 | Command names and field decoders for General, Simple Remote, Display Remote, Extended Interface and Digital Audio. Unknown commands show as hex and count as unexplained. |
| `audio.py` | L5 | WAV per stream. Sample rate from UAC1 `SET_CUR`, else from `TrackNewAudioAttributes`, else 44,100 Hz. A pause shorter than 5 s becomes silence, so the file keeps wall-clock time; a longer pause starts a new file. Reports pauses longer than 20 ms. |
| `decoder.py` | — | Runs the layers. Uses the captured configuration descriptors to find the HID and audio endpoints. A port reset or `SET_CONFIGURATION` flushes the HID state and ends the audio stream. Root hub requests are counted; port resets are shown. |
| `sessionfiles.py` | — | Reads `marks.tsv`, `udc.log` and `proxy.log`. Iso timing errors that come within 50 ms of each other become one event. |
| `output.py`, `cli.py` | — | Text, JSON Lines, summary, command line. |

Limits:

- The command tables come from public reverse engineering, because Apple's iAP spec is confidential. Each table needs checks against captures. The `car-10` decodes are self-consistent: replies match requests, and track-change notifications match the title requests.
- iAP2 is detected but not decoded. This stereo does not use it.
- One usbmon bus per capture. Device number 1 is taken to be the root hub.
- usbmon sees only the Pi to Apple device side. The relay re-packetises HID, so report boundaries in the capture differ from what the stereo sent and received. The iAP packets are the same.
- No golden-file tests yet.

## 3. Layers, references and tests

### 3.1 Layers

| Layer | Input | Output |
|---|---|---|
| L0 extract | pcapng | One record per transfer: time, direction, endpoint, type, setup packet, data. The decoder reads the pcapng blocks and the 64-byte usbmon header (link type 220) itself, and pairs each submit with its completion. |
| L1 HID transport | HID records | Byte streams per direction. Stereo to iPhone: `SET_REPORT` control transfers (bmRequestType 0x21, bRequest 0x09), or interrupt OUT if present. iPhone to stereo: interrupt IN reports. Each report has a report ID, then a link-control byte (more data follows / continuation), then data. Remove padding with the packet length from L2. The report length comes from the data, so L1 needs no report table. |
| L2 protocol detect | streams | `FF 55` is iAP1 framing. `FF 55 02 00 EE 10` is the iAP2 detect handshake; the iPhone echoes it. `FF 5A` is an iAP2 link packet. Expect iAP1 for the PA68L0. |
| L3 iAP1 packets (main path) | `FF 55` packets | `FF 55`, length (1 byte, or `00` plus 2 bytes for large packets), lingo ID, command ID, optional 2-byte transaction ID, data, checksum. The checksum is the two's complement of the byte sum from length to the end of the data. Transaction IDs appear only after the two sides agree to use them, so track that state. |
| L4 iAP1 lingos | lingo + command | Name the commands. Expected lingos: General (0x00, includes identification and authentication), Simple Remote (0x02), Display Remote (0x03), Extended Interface (0x04, browse and track info), Digital Audio (0x0A, sample rate). Show unknown commands as hex. |
| L5 audio | isochronous IN data | Write a WAV file. Read the sample rate from the Digital Audio lingo, or from the UAC1 `SET_CUR` sampling frequency request. |
| L3b–L5b iAP2 (build only if L2 finds iAP2) | `FF 5A` packets | Link header: `FF 5A`, 16-bit length, control byte (0x80 SYN, 0x40 ACK, 0x20 EAK, 0x10 RST, 0x08 SLP), seq, ack, session ID, header checksum, then payload and payload checksum. Control session messages start with `40 40`. File transfer sessions carry artwork. |

Output: a timeline text file and a JSON Lines file. Each line has the time, direction, layer, message name and decoded fields.

### 3.2 References

- Apple's iAP specs are confidential under the MFi programme. Public command tables come from reverse engineering and old leaked documents. Check each table against your captures.
- oandrew/ipod and oandrew/ipod-gadget (Go): the main reference. HID report framing, iAP1 packet parsing and lingo tables. ipod-gadget emulates an iPod to car stereos on a Pi Zero.
- Rockbox `apps/iap/`: an iPod-side iAP1 implementation with per-lingo command handlers.
- JJTech0130 `iap2.lua` (Wireshark dissector, 2026) and the wiomoc.de "mfi_iap" article: only for the iAP2 path.
- Linux `drivers/usb/misc/apple-mfi-fastcharge.c`: the Apple charge request.

### 3.3 Tests

- Unit tests with synthetic frames: a small iAP1 packet, a large-format iAP1 packet, a bad checksum, a packet split across several HID reports, the iAP2 detect handshake. Also: transaction IDs after `StartIDPS`, a synthetic pcapng file, field decoders, and the audio pause fill.
- Golden-file tests: after the Phase 5 action list, store a short trimmed capture and its expected timeline.
- Run with `uv run --project tools/decode pytest tools/decode/tests` (29 tests pass, 08/10/2026 and 09/10/2026). Lint with `uvx ruff check tools/decode`.
