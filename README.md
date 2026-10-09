# Pi 5 iAP USB relay and sniffer

Handover notes for an agent that starts with no context. Last updated 08/10/2026.

Read this file first. Then read [PLAN.md](PLAN.md). It holds the full history: each phase with its result, and risks R1–R16 with evidence. The code is in `pi/` (the relay, on the Pi) and `decode/` (the decoder, on the Mac).

## 1. What this project is

A Raspberry Pi 5 sits between an Apple device and an old car stereo. The stereo thinks that the Pi is the Apple device. The Pi forwards all USB traffic in both directions and records it. A Python tool on the Mac decodes the recordings offline.

The stereo speaks **iAP1** (Apple's legacy iPod Accessory Protocol) over **USB HID**. Music goes over **USB Audio Class 1** (isochronous). No CarPlay, no iAP2.

| Part | State on 08/10/2026 |
|---|---|
| Relay in the car | **Works.** The stereo identifies the device, authenticates, plays audio and its buttons control playback. Proven in sessions `car-10` and `car-15`. |
| Audio through the relay | **Clean** with 4 CPU cores: 5 of 69,900 packets dropped in `car-15`. |
| Track titles on the stereo | Not shown. **This is the stereo's own behaviour**: it shows no title with a direct connection either. Not a relay fault. |
| Capture | Works. One folder per session: pcapng, relay log, UDC state log, marks. |
| Decoder | Works. Explains all 27,174 transfers in `car-10`. 29 unit tests pass. |
| Golden captures | **Not done.** The Phase 5 action list with marks still needs a car session. |
| Mac as host (Finder, usbmux pairing) | Image Capture works. Finder pairing does not complete. Not needed for the stereo, so not fixed. |

## 2. Hardware and wiring

```
Apple device (USB device)
   │  USB-A plug to USB-C plug cable
   ▼
Pi 5 BLACK USB 2.0 port (host side, xHCI, usbmon bus 1)
   │  usb-proxy: libusb on the host side, raw_gadget on the device side
   ▼
Pi 5 USB-C port (device side, dwc2 peripheral, UDC 1000480000.usb)
   │  USB-C plug to USB-A plug cable, VBUS (pin 1) TAPED
   ▼
Car stereo USB-A socket (USB host, full speed only)

Bench supply 5.1 V ──► GPIO pins 2 and 4 (5 V), pins 6 and 14 (GND)
```

| Item | Facts |
|---|---|
| Stereo | Suzuki PA68L0 = Panasonic CQ-JZ41F0AE, factory unit in a Suzuki Swift of about 2010. USB-A socket. Text display. Full-speed (12 Mbit/s) USB host only. |
| iPhone | iPhone 15 Pro, `05ac:12a8`, iOS 27. Used for the desk and Mac tests. |
| iPad | `05ac:12ab`, reports software version 26.6.1 over iAP. **Both working car sessions used the iPad.** The iPhone has not been re-tested in the car through the final relay. |
| Pi | Raspberry Pi 5, Raspberry Pi OS Lite 64-bit (Debian 13 Trixie), kernel `6.18.50+rpt-rpi-2712`. |

Rules that protect the hardware:

- **Keep VBUS taped** on pin 1 of the USB-A plug that goes to the stereo. Use the same taped cable for Mac tests, with a USB-C to USB-A adapter on the Mac. Never use a USB-C to USB-C cable to a host. Phase 1 measured 0 V on the Pi's USB-C VBUS, so the Pi does not drive it. Current from a host into the Pi's 5 V rail is still unknown.
- **Use a black USB 2.0 port** for the Apple device. A blue USB 3 port can connect an iPhone 15 Pro at SuperSpeed with different descriptors.
- **Power:** bench supply at 5.1 V, current limit 5 A, into the GPIO header. Never connect a USB-C power supply at the same time. Use short, thick leads. Thin leads dropped 0.19 V: the SD card then read garbage, and the Pi shut down at about 1 A. Check the voltage under load with `vcgencmd pmic_read_adc EXT5V_V`. It must stay at 5.0 V or more.
- `usb_max_current_enable=1` in `config.txt` raises the USB-A limit to 1.6 A in total. GPIO power gives no USB-PD data, so without it the limit is 600 mA.
- **Shut down before you cut power:** `ssh root@pi5-sniffer.local poweroff`. Hard power cuts during boot once left the Wi-Fi profile as a 0-byte file. The last session on 08/10/2026 ended with a hard cut.
- **In the garage:** key in ACC only. Never run the engine in a closed garage. A flat battery makes the stereo ask for its security code.

## 3. Access to the Pi

| Item | Value |
|---|---|
| Host name | `pi5-sniffer`, or `pi5-sniffer.local` over mDNS |
| Login | `root` with an SSH key. User `jack` exists too: key only, passwordless sudo. |
| SSH public key | The user's own key. `prepare-sdcard.sh` reads it with `op read` from the secret reference in `SSH_KEY_REF`. |
| Wi-Fi | One WPA2 network, set with `WIFI_SSID` and `WIFI_PSK_REF` when the card is prepared. The Pi has a NetworkManager keyfile for it. |
| Ethernet to the Mac | No DHCP. IPv6 link-local only. `pi5-sniffer.local` still works. |

Rules:

- **Use WPA2** (`WIFI_KEY_MGMT=psk`). WPA3 (`sae`) association failed on the Pi at a low clock speed.
- **Keep network details and secret references out of the repo.** The repo is public. Pass them to `prepare-sdcard.sh` as environment variables.
- **Never print secrets.** Pipe them into SSH from `op read`, through stdin. cloud-init writes the Wi-Fi password into its logs in plain text. Never print `/var/log/cloud-init*.log` or network configs without a mask that you have tested first. A leak of this type happened once, and the user had to rotate a password.

## 4. Repository map

This folder is not a git repository. macOS adds `.DS_Store` files; exclude them when you copy files to the Pi.

```
pi5-usb-sniffer/
├── README.md                 # this file
├── PLAN.md                   # full plan, phase results, risks R1–R16, evidence
├── pi/                       # everything that runs on the Pi
│   ├── prepare-sdcard.sh     # Mac: customises a freshly flashed boot partition (cloud-init)
│   ├── sniffer-setup.service # first-boot unit that runs setup.sh once
│   ├── setup.sh              # packages, raw_gadget (DKMS), usb-proxy build with patches, system config
│   ├── capture.sh            # installed as /usr/local/bin/capture
│   ├── patches/              # usb-proxy-01 … -09, applied in name order
│   └── rules/
│       ├── apple-vendor-rules.json  # usb-proxy injection rules: ignore and stall
│       └── config.json              # usb-proxy options file
├── sink/                     # Pi 3 B test sink: plays the car stereo for the relay, see sink/README.md
│   (pi/ipod/               # Bluetooth iPod: the Pi 5 as an iPod for the stereo, see pi/ipod/README.md)
├── decode/                   # Python decoder, a uv project
│   ├── pyproject.toml
│   ├── uv.lock
│   ├── src/iap_decode/
│   └── tests/
└── sessions/                 # captures copied from the Pi, and decoder output. PRIVATE.
```

## 5. Pi software

### 5.1 How the Pi was built

1. Flash Raspberry Pi OS Lite 64-bit (Trixie, with cloud-init) to an SD card.
2. On the Mac, run `pi/prepare-sdcard.sh /Volumes/bootfs` with `WIFI_SSID`, `WIFI_PSK_REF` and `SSH_KEY_REF` set. It reads the SSH key and the Wi-Fi password with `op read` (1Password CLI). It writes cloud-init `user-data` and `network-config`, adds lines to `config.txt` and `cmdline.txt`, and copies `pi/` to `bootfs/sniffer/`.
3. On first boot, cloud-init copies the files to `/opt/sniffer/pi/` and starts `sniffer-setup.service`.
4. `setup.sh` waits for the network and the clock, runs `apt full-upgrade`, and installs the packages. It builds `raw_gadget` through DKMS and builds `usb-proxy` with the patches. It configures the system, installs `capture`, and reboots.
   - Log: `/var/log/sniffer-setup.log`.
   - Done marker: `/var/lib/sniffer/setup-done`.
   - Versions: `/var/lib/sniffer/versions.txt`.

| Setting | Where | Why |
|---|---|---|
| `dtoverlay=dwc2,dr_mode=peripheral` | `config.txt`, `[all]` | USB-C port becomes a USB device. UDC name `1000480000.usb`. |
| `usb_max_current_enable=1` | `config.txt` | 1.6 A on USB-A with GPIO power. |
| `cfg80211.ieee80211_regdom=NZ` | `cmdline.txt` | Wi-Fi country. The image ships with Wi-Fi blocked until a country is set. |
| `usbhid.quirks=0x05ac:0x12a8:0x4,0x05ac:0x12ab:0x4` | `cmdline.txt` | `HID_QUIRK_IGNORE`. `usbhid` is built into the kernel and would bind the Apple HID interface. That delayed `SET_CONFIGURATION` (R13). |
| `usbmon`, `raw_gadget` | `/etc/modules-load.d/sniffer.conf` | Loaded at boot. |
| blacklist `ipheth`, `snd_usb_audio`, `cdc_ncm`, `cdc_ether` | `/etc/modprobe.d/sniffer-blacklist.conf` | Only `usb-proxy` may use the Apple device. |
| `usbmuxd` masked | systemd | Same reason. |
| `raw_gadget` | DKMS, `xairy/raw-gadget` at `8c6de54` | Not in the Pi kernel. Version string `1.0+8c6de54`. |
| `usb-proxy` | `/opt/sniffer/usb-proxy`, `AristoChen/usb-proxy` at `a08301d` | The relay, with the patches in section 5.2. |

**Last known Pi state that a fresh card would not have.** The Pi was offline at the end of the session, so check these:

- `config.txt` has the GPU driver line commented out (`#dtoverlay=vc4-kms-v3d`, with a "LOW-POWER TEST" comment). This was left from a power test. The clock is back to normal, and all 4 cores are on (`maxcpus=1` was removed). **Keep 4 cores:** one core caused audio drops.
- Backups exist next to the boot files: `*.before-lowpower` and `cmdline.txt.before-4cores`.
- Some debug tools exist only on the Pi, in `/opt/sniffer/tools/`. They are not in this repo:
  - `otherspeed.c` fetches other-speed descriptors.
  - `hiddesc.c` dumps the HID class and report descriptors.
  - `probe.c` sends 1-byte `SET_REPORT` tests.
  - `setcfgtime.c` times `set_configuration` and interface claims.
- 28 sessions (about 251 MB) are in `/root/sessions/`.

### 5.2 The usb-proxy patches

Upstream `usb-proxy` could not relay this device to this stereo. Each patch fixes one observed failure. They apply in name order on commit `a08301d` and build with no warnings.

| Patch | Problem it fixes |
|---|---|
| `01-detach-before-claim` | The kernel binds `usbhid` and `snd-usb-audio` after `SET_CONFIGURATION`. The patch detaches kernel drivers before each interface claim. |
| `02-rules-wildcard-block` | Adds `-1` wildcards to the injection rules. Ignore and stall rules are checked **before** a request goes to the device; upstream forwarded IN requests first. An ignored OUT request is acked to the host and not forwarded. Prints each matched rule. |
| `03-suspend-retry` | When the host suspends the bus, an endpoint write returns `EAGAIN` and upstream calls `exit()`. The patch waits and retries. Both the Mac and the stereo suspend the bus. |
| `04-full-speed-host` (R11) | The stereo is full speed only, but the device is high speed. At start the relay fetches the other-speed descriptors (`GET_DESCRIPTOR` type 7). If `/sys/class/udc/*/current_speed` is `full-speed`, it serves those in place of the configuration descriptors. It also enables the gadget endpoints with the full-speed packet size and interval. |
| `05-learned-stalls` (R12) | The gadget framework acks an OUT control request with data before the relay can read it, so a device STALL cannot reach the host. The patch remembers each setup packet that the device stalled. It stalls the host at SETUP the next time that request comes. |
| `06-ack-before-device-work` (R13) | A standard request with no data must complete within 50 ms. `SET_CONFIGURATION` on the real device takes about 54 ms. The patch enables the gadget endpoints and acks first, then configures the device and starts the endpoint threads. `SET_INTERFACE` uses the same order. |
| `07-apple-hid-fs-tables` (R14) | The Apple device has a different HID report table at each speed. At full speed the relay serves the 96-byte full-speed HID report descriptor. It re-packetises iAP traffic between the two tables in both directions (section 8.2). It also drops zero-length interrupt reads. It works only when the device's HID report descriptor is 208 bytes at high speed and 96 at full speed. |
| `08-fs-in-max-report` (R15 test) | Adds option `apple_fs_in_max_count` in `config.json` (default 63). A test with 20 did not change anything, and the option was removed from `config.json`. The patch is still applied, with no effect. |
| `09-ack-config-zero` (R17) | A Linux host sends `SET_CONFIGURATION 0`. Upstream skipped it without an ACK, and the host timed out after 5 s. The patch acks configuration 0 and stalls other invalid values. The car stereo never sends it. |

### 5.3 Rules and options

`pi/rules/apple-vendor-rules.json`, loaded by default:

| Rule | Request | Why |
|---|---|---|
| ignore | `bmRequestType 0x40, bRequest 0x40`, any value and index, no data | The Apple charge request. macOS asks for 2400 mA, and the iPhone then trips the Pi's 1.6 A USB-A limit (R4). The relay acks it and does not forward it. |
| stall | any type, `bRequest 0x52`, `wLength 1` | Apple "set USB mode". The iPhone re-enumerates with 6 configurations, and the relay loops. |
| stall | `0x21/0x09` (HID `SET_REPORT`), `wIndex 2`, `wLength 1` | 1-byte `SET_REPORT` probes (report ID only). The stereo sent these in a loop when it got a truncated HID descriptor (R14). |

**Quirk: numbers in this file are hex digits written as JSON integers.** `40` means 0x40, and `52` means 0x52. `usb-proxy` converts them when it matches. The rule list that `usb-proxy` prints at start shows the unconverted value, for example `bRequestType=0x28` for `40`. That is the display only; matching uses 0x40.

`pi/rules/config.json` holds `{"reset_device_before_proxy": false}`. `capture` does not use this file directly. It writes `/run/sniffer/config.json` with the reset flag from `PROXY_RESET`, and it always passes `--enable_customized_config`. `usb-proxy` reads `config.json` only from its working directory (`/run/sniffer`), and only with that flag.

### 5.4 The `capture` command

`/usr/local/bin/capture` is `pi/capture.sh`. Run it as root.

| Command | What it does |
|---|---|
| `capture start [label]` | Loads `usbmon` and `raw_gadget`. Waits for an Apple device on USB-A, and warns if it is not at 480 Mbit/s. Creates `/root/sessions/YYYYmmdd-HHMMSS-label/` and the symlink `/root/sessions/current`. Writes the metadata, then starts three transient systemd units. Prints "Connect the Pi's USB-C cable to the stereo now." |
| `capture mark <text>` | Adds a line to `marks.tsv`: UTC time, NZ local time, text. |
| `capture stop` | Adds a "session stop" mark and stops the units. dumpcap gets SIGTERM, so it closes the file correctly. Prints an `rsync` command. |
| `capture status` | Unit states, UDC state and speed, the Apple device, the current session. |

| Systemd unit | Runs |
|---|---|
| `sniffer-dumpcap` | `dumpcap -i usbmon<bus> -b filesize:500000`, a ring of 500 MB pcapng files |
| `sniffer-udcwatch` | Logs UDC state and speed changes to `udc.log` every 0.2 s |
| `sniffer-proxy` | `capture _proxyloop`, which supervises `usb-proxy` |

The `_proxyloop` supervisor:

- Starts `usb-proxy` with `setsid`. On device disconnect, `usb-proxy` sends SIGINT to its whole process group, which would also kill the loop.
- Restarts `usb-proxy` when the device's USB device number changes to a new non-zero value, which means re-enumeration. The number reads 0 for a moment during a reset; that is not a re-enumeration.
- Adds a UTC timestamp (`HH:MM:SS.mmmZ`) to each line of `proxy.log` with a Perl filter. The filter must stay unbuffered (`$| = 1`), and `usb-proxy` runs under `stdbuf -oL -eL`.

Environment options for `capture start`:

| Variable | Default | Use |
|---|---|---|
| `PROXY_RESET` | `1` | `1` resets the device when `usb-proxy` starts. **Needed for the stereo:** a device left mid-authentication ignores a new `IdentifyDeviceLingoes`. **Use `0` for a Mac host:** its USB mode change makes a reset re-enumerate the device in a loop. Note: the `capture` help text still says "default: no reset". That text is wrong; the code default is 1. |
| `PROXY_ARGS` | `--iso_batch_size=4` | Extra `usb-proxy` options. 4 gave the fewest audio drops: 8 gave 1.1%, 4 gave 0.6%, 2 gave 2.5%, all on one core. For a Mac host, add `--auto_remap_endpoints`: macOS selects configuration 6, which needs all 14 dwc2 endpoints. |
| `PROXY_VERBOSE` | `1` | Number of `-v` flags. |
| `INJECTION_FILE` | the rules file above | Set to empty to disable all rules. |

Files in a session folder:

| File | Content |
|---|---|
| `session.txt` | Start time, kernel, OS, `usb-proxy` commit, `raw_gadget` version, device ID, speed and options |
| `iphone-lsusb-v.txt`, `lsusb-t.txt` | Device descriptors, written before the capture starts |
| `usb_NNNNN_<time>.pcapng` | usbmon capture of the host side: Pi to Apple device, high speed |
| `proxy.log` | `usb-proxy` log with timestamps: the gadget side, which usbmon cannot see |
| `udc.log` | UDC state (`not attached`, `configured`, …) and speed |
| `marks.tsv` | Marks |

The program names say "iPhone" for any Apple device, including the iPad.

### 5.5 Deploying a change to the Pi

Stop a running session first with `capture stop`. Then copy the files and rebuild. The rebuild steps are the same as in `setup.sh`:

```bash
rsync -a --exclude .DS_Store pi/ root@pi5-sniffer.local:/opt/sniffer/pi/
```

```bash
ssh root@pi5-sniffer.local 'install -m 755 /opt/sniffer/pi/capture.sh /usr/local/bin/capture'
```

```bash
ssh root@pi5-sniffer.local 'cd /opt/sniffer/usb-proxy && git checkout -q -f a08301d21d6ba1036cddbcb1a4314578cbc4a274 && for p in /opt/sniffer/pi/patches/usb-proxy-*.patch; do git apply "$p" || exit 1; done && make -j4'
```

`git checkout -f` removes the old patches, so the new set applies cleanly. Do not re-run `setup.sh` for a small change: it also runs `apt full-upgrade`.

To check that the Pi matches this repo, use an rsync dry run:

```bash
rsync -ani --exclude .DS_Store pi/ root@pi5-sniffer.local:/opt/sniffer/pi/
```

### 5.6 Bluetooth iPod mode

The Pi 5 can also be an iPod that plays the phone's Bluetooth audio, instead of relaying a USB iPhone. It sends the track data over iAP1 and turns the stereo's play, pause and next into AVRCP commands for the phone. It uses the same USB-C port as the relay, so `ipod-mode bt` and `ipod-mode relay` switch between them. See [pi/ipod/README.md](pi/ipod/README.md). The Pi 3 sink is its test stereo.

## 6. Running sessions

### 6.1 A car session

1. Pi on bench power in the car. Key in ACC. SSH in over Wi-Fi.
2. Plug the Apple device into a black USB-A port. Unlock it.
3. Run `capture start car-NN`. Wait for "Connect the Pi's USB-C cable to the stereo now."
4. Plug the taped cable into the stereo. Select the USB source on the stereo.
5. Watch `tail -f /root/sessions/current/proxy.log` and `cat /root/sessions/current/udc.log`. `configured` with `full-speed` is normal.
6. Before each action, run `capture mark "<action>"`.
7. Run `capture stop`.
8. On the Mac, copy the session:

```bash
rsync -a root@pi5-sniffer.local:/root/sessions/<session-dir> sessions/
```

9. Run `poweroff` on the Pi before you cut the bench power.

In `car-10`, audio data started about 4 s after the stereo connected.

### 6.2 A Mac test session

```bash
PROXY_RESET=0 PROXY_ARGS=--auto_remap_endpoints capture start mac-NN
```

The Mac selects configuration 6, which needs all 14 dwc2 endpoints. Image Capture lists photos, and iPhone USB networking works. Finder does not show the device, and the device asks for Trust more than once.

### 6.3 Troubleshooting

| Symptom | Likely cause and action |
|---|---|
| `capture start` says a session is still running | A unit from an earlier session is still active. Run `capture stop`. |
| Stereo shows "Unsupported" | Look in `proxy.log` and `udc.log`. Past causes were high-speed descriptors (patch 04), a late `SET_CONFIGURATION` ack (patch 06), and a truncated HID descriptor with 1-byte probes (patch 07 and the stall rule). |
| Stereo stuck on "Reading" | Check that iAP traffic flows: decode the session or read `proxy.log`. In `car-10`, on connection 3, the stereo never answered `GetDevAuthenticationInfo`. The cause is not known. Unplug and plug in again. |
| All USB-A ports turn off | Over-current. A host sent the Apple charge request. Check that the ignore rule is loaded. |
| Relay loops: connect, disconnect, connect | A device re-enumeration or a reset loop. For a Mac host use `PROXY_RESET=0`. |
| `usb-proxy` does not stop, process in state `D` | It is stuck in the kernel (`driver_attach`). Only a reboot clears it. |
| Audio glitches | Check that 4 cores are on (`nproc`) and that `--iso_batch_size=4` is set. Count `isochronous timing error` lines in `proxy.log`. |
| No track title on the stereo | Normal for this stereo. |

## 7. The decoder

### 7.1 Use

```bash
uv run --project decode iap-decode sessions/<session-dir>
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
uv run --project decode pytest decode/tests
```

```bash
uvx ruff check decode
```

### 7.2 Design

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

## 8. Protocol facts learned

### 8.1 Apple device descriptors

The iPhone and the iPad have the same layout: 4 configurations. Linux selects configuration 1 by itself; the stereo selects configuration 2.

| Config | Name | Content |
|---|---|---|
| 1 | PTP | Imaging: bulk 0x02 OUT, bulk 0x81 IN, interrupt 0x83 IN |
| 2 | iPod USB Interface | if0 audio control; if1 audio streaming, alt 1 has iso 0x81 IN, 192 bytes; if2 HID, interrupt 0x83 IN, 64 bytes, **no interrupt OUT** |
| 3 | PTP + Apple Mobile Device | adds usbmux on bulk 0x04 OUT and 0x85 IN |
| 4 | PTP + Apple Mobile Device + Apple USB Ethernet | adds bulk 0x86 IN and 0x05 OUT |

- Audio: UAC1 PCM, 2 channels, 16 bit, 8–48 kHz. Packets are 1 ms at both speeds: high-speed `bInterval` 4, full-speed `bInterval` 1.
- HID report descriptor: **208 bytes at high speed, 96 bytes at full speed.** You cannot read the full-speed one from a device connected at high speed. Patch 07 contains it; the source is `oandrew/ipod-gadget`.
- After Apple "set USB mode" (`0xC0/0x52`), the device re-enumerates with 6 configurations. After a reset, Linux compares the descriptors; a change makes it a new device.

### 8.2 iAP over HID

- Report format: `[report ID][link control byte (LCB)][payload, zero-padded to the report count]`. The count includes the LCB but not the ID.
- LCB bit 0 = this report continues a packet. LCB bit 1 = more reports follow. So `0x00` is a single report, `0x02` first, `0x03` middle, `0x01` last.
- **Only the last fragment may carry padding.** The receiver joins fragments end to end. An early version of patch 07 padded middle fragments and corrupted the certificate.
- Accessory to device: HID `SET_REPORT` control transfers (`0x21/0x09`, `wValue 0x02xx`, `wIndex 2`). Device to accessory: interrupt IN reports.

| Table | Input report IDs (device to accessory) and counts | Output report IDs (accessory to device) and counts |
|---|---|---|
| High speed | 0x01–0x0C: 5, 9, 13, 17, 25, 49, 95, 193, 257, 385, 513, 767 | 0x0D–0x15: 5, 9, 13, 17, 25, 49, 95, 193, 255 |
| Full speed | 0x01–0x04: 12, 14, 20, 63 | 0x05–0x09: 8, 10, 14, 20, 63 |

The relay re-packetises each iAP packet between the tables. Toward the device it sends full 0x15 reports (254 payload bytes) and a last report that fits. Toward the stereo it sends full 0x04 reports (62 payload bytes) and a last report that fits.

A full-speed host must never get the high-speed table. With it, the stereo tried to send its certificate in report 0x15, a 256-byte `SET_REPORT`, and then sent zero-length `SET_REPORT`s in a loop. The working explanation is an 8-bit length field in the stereo. It is not proven.

### 8.3 iAP1 framing

- Packet: `55 len lingo cmd [txid16] data… checksum`. Large packet: `55 00 len16 …`. Over USB there is no `FF` sync byte.
- Checksum: two's complement of the byte sum from the length byte(s) to the end of the data.
- The Extended Interface lingo (0x04) has 2-byte command IDs; all other lingoes have 1-byte IDs.
- This stereo uses the old identification (`IdentifyDeviceLingoes`, not IDPS), so it uses no transaction IDs.

### 8.4 How the stereo behaves

Per connection, from `car-10`:

1. It reads the full-speed descriptors, selects configuration 2 and reads the HID report descriptor.
2. It sends `IdentifyDeviceLingoes`: General, Display Remote, Extended Interface and Digital Audio. Authentication "immediate", device ID 0x00000200.
3. The device asks for the certificate. The stereo answers 0.9 s later: `RetDevAuthenticationInfo` version 2.0, in 2 sections, 946 bytes in total. It is the same certificate every time.
4. The device asks for sample rates (stereo: 32, 44.1 and 48 kHz) and accessory capabilities (0x00000001). It sends a 20-byte signature challenge, then `TrackNewAudioAttributes` at 44,100 Hz.
5. The stereo asks for lingo versions (General 1.9, Extended Interface 1.14, Digital Audio 1.2) and the software version. It selects audio alt 1 and sends `EnterExtendedInterfaceMode`. The device answers "command pending, 3000 ms", then success.
6. Audio: 40 packets, then a 2 s pause until the stereo sends UAC1 `SET_CUR` with 44,100 Hz, then continuous audio.
7. Polling: `GetPlayStatus` every 600 ms. The device sends `PlayStatusChangeNotification` "track time" every 500 ms. On a track change the stereo asks for the title, artist and album once.
8. The stereo's `RetDevAuthenticationSignature` (128 bytes) comes **about 56 s after the challenge**. Audio and controls work during that time.

Other stereo facts:

- Full-speed host only. It needs the full-speed descriptors and the full-speed HID table.
- It gives up ("Unsupported") if `SET_CONFIGURATION` is not acked within 50 ms.
- It suspends the bus before it disconnects. 4 suspends are in `car-10`.
- With a truncated HID descriptor, it probes with a 1-byte `SET_REPORT`, ID 0x2d. When the relay acked the probe, the stereo repeated it about 326 times per second. When the relay stalled it, the stereo repeated it every 100 ms. Both ended in "Unsupported".
- It shows "TR nn" and the elapsed time. The DISP button does nothing visible. It shows no title, even with a direct connection.
- It sends `PlayControl` "end fast forward/rewind" often: 21 times in `car-10`.

### 8.5 Mac host behaviour

- macOS sends the Apple charge request `0x40/0x40` with `wValue 0x0960` (2400 mA) and `wIndex 0x076c` (1900 mA). Within about 6 ms the iPhone exceeded the Pi's limit, and all USB-A ports reported over-current.
- macOS sends "set USB mode 4" (`0xC0/0x52`) and selects configuration 6.
- macOS suspends the bus after about 2 minutes idle.

## 9. Quirks and gotchas

Relay and USB:

- The gadget framework acks OUT control data before user space reads it, so the relay cannot pass a device STALL for such a request (R12, patch 05).
- dwc2 gives isochronous timing errors (`ENODATA`, errno 61) on some audio packets. One core made this much worse. These errors happen on the stereo side, so `usbmon` cannot see them; only `proxy.log` shows them.
- `usb-proxy` hangs when the device re-enumerates, and it kills its own process group (see 5.4).
- systemd needs `WorkingDirectory` to exist before a unit starts; `capture` creates `/run/sniffer` first.

Pi OS:

- The Raspberry Pi OS image blocks Wi-Fi until a country is set. `prepare-sdcard.sh` unblocks it and sets NZ.
- macOS writes `._*` files onto the FAT boot partition. `prepare-sdcard.sh` deletes them.
- After a hard power cut, check that the Wi-Fi keyfile in `/etc/NetworkManager/system-connections/` is not empty. Check `journalctl -b` for filesystem repairs.

Tooling on the Mac:

- In this shell, `grep` is a shell function. It returned no output in some `cd … && grep` commands. Use `/usr/bin/grep`, and add `-a` for files that contain binary bytes.
- GNU coreutils replace the macOS defaults, so `sed -i` takes GNU syntax.
- Perl one-liners with braces broke when sent through SSH quoting. Small Python scripts were more reliable for edits.
- `pkill -f "capture start"` once killed the agent's own SSH shell. Anchor the pattern, for example `pkill -f '^bash /usr/local/bin/capture'`.
- WebSearch refused some USB and MFi questions for the main model. The user asked for web research to go to an Opus subagent.

## 10. Data and privacy

| Place | Content |
|---|---|
| Pi `/root/sessions/` | All 28 sessions: `phase3-mac-1` to `-10`, `car-1` to `car-15`, and others. |
| Mac `sessions/phase2-20261008-194356/` | iPhone descriptors only |
| Mac `sessions/20261008-221642-car-10/` | Complete car session (61 MB) and `decoded/`. **The main data set for the decoder.** |
| Mac `sessions/20261008-223159-car-15-4cores/` | **Partial.** The pcapng is missing, because the Pi went offline during the copy. `proxy.log` and the small files are present. |

Treat all session data as private. Never publish it or upload it to an external service.

- Captures hold the stereo's MFi certificate and the device's serial number.
- They also hold the device name and track, artist and album names.
- The Mac test sessions on the Pi hold usbmux pairing data.

The `decoded/` folders hold the same data in readable form.

## 11. Open work

1. **Copy `car-15`.** When the Pi is on again, copy the pcapng, then decode it:

```bash
rsync -a root@pi5-sniffer.local:/root/sessions/20261008-223159-car-15-4cores sessions/
```

2. **Check the Pi's health** after the hard power cut (section 9).
3. **Golden captures:** run the Phase 5 action list in the car (PLAN.md, Phase 5 step 6), with a `capture mark` before each action. Then add golden-file tests: a short trimmed capture and its expected timeline.
4. **iPhone in the car:** re-test the iPhone through the final relay. Only the iPad has worked there so far.
5. **Investigate** the connection where the stereo never sent its certificate, and the 56 s signature delay. Is the delay normal for this stereo with a direct connection? A direct capture needs a passive analyser; see PLAN.md section 10.
6. **Bluetooth iPod in the car.** It passed on the bench with the Pi 3 as the stereo (`pi/ipod/README.md`, section 8). It has not run with the real stereo.
7. **Optional:** usbmux pairing through the relay (not needed for the stereo). An ADuM4160 USB isolator could replace the full-speed translation: it would make the device enumerate at full speed on the Pi.

## 12. Working with this user

- Write replies in ASD-STE100 (Simplified Technical English), with short sentences. Use the future tense for work that you have not done yet.
- Dates are dd/mm/yyyy. Units are metric. The user is in New Zealand: the Pi runs `Pacific/Auckland`, and the logs use UTC.
- The user does the physical work: cables, power and the car. Say exactly what to plug in or measure, and wait for confirmation.
- Ask before you do anything that can damage hardware or the car battery.

## 13. References

- `xairy/raw-gadget` and `AristoChen/usb-proxy` on GitHub: the relay base.
- `oandrew/ipod` and `oandrew/ipod-gadget` (Go): iAP1 over HID framing, lingo tables, and the full-speed HID report descriptor (`gadget/ipod.h`).
- Rockbox `apps/iap/`: an iPod-side iAP1 implementation.
- Linux `drivers/usb/misc/apple-mfi-fastcharge.c`: the Apple charge request.
- Raspberry Pi white paper RP-009276-WP: Pi 5 USB gadget mode.
