# pi-iap-bt-bridge: a Pi that turns Bluetooth audio into iAP1

Handover notes for an agent that starts with no context. Last updated 09/10/2026.

Read this file first. Then read [PLAN.md](PLAN.md): the goal, the status, the open work, the user's decisions, the full history (phases 0–12) and risks R1–R23 with evidence.

## 1. What this project is

A Raspberry Pi 5 lets an old car stereo that knows only iPods play a phone over Bluetooth. The phone sends its audio and track data to the Pi over Bluetooth (A2DP and AVRCP). To the stereo, the Pi is an iPod on USB: it synthesizes the whole iAP1 session (identification, the authentication exchange, play status, track data, notifications) and the USB audio from the Bluetooth input. The stereo's buttons control the phone.

```
phone ──Bluetooth A2DP──► Pi 5 ──BlueALSA──► alsaloop ──► USB gadget sound card ──► USB-C cable ──► car stereo
  ▲     AVRCP metadata       │                                                        (VBUS taped)      (USB host)
  └─────AVRCP controls───────┴── ipod-bridge ◄── iAP1 over HID (/dev/hidg0) ◄────────────────────────────┘
```

The stereo speaks **iAP1** (Apple's legacy iPod Accessory Protocol) over **USB HID** and takes music over **USB Audio Class 1**. No CarPlay, no iAP2. The stereo is a Panasonic CQ-JZ41F0AE (Suzuki PA68L0) in a Suzuki Swift of about 2010.

| Part | State on 09/10/2026 |
|---|---|
| Stereo session (USB and iAP1) | **Works in the car.** Full-speed link, certificate, init queries, polling. |
| Audio from the phone | **Plays in the car.** Less clipping after the fixed -6 dB gain, but some remains (third car run). |
| No phone connected | A playing "Waiting" track instead of `Unsupported` (third car run: works). |
| Stereo buttons | Play, pause (also the stereo's mute), next, back, DISP, RDM and RPT work (third car run: "seems to work OK"). |
| Pairing and reconnecting | The Pi connects to a paired phone by itself, forgets dead pairings, and opens a pairing window when no phone connects. |
| Delay reporting | The iPad accepts the Pi's delay report (500 ms for now). The Pi's real delay is not measured yet, and `alsaloop` has not run with a USB host yet. |

### How we got here

The project did not start as a bridge. Each step gave the next one its facts. [PLAN.md](PLAN.md), section 6, has every phase with its result.

1. **Relay and sniffer** (Pi 5, `tools/relay/`, 08/10/2026). The Pi sat between an iPad and the stereo, relayed all USB traffic and recorded it. Nine `usb-proxy` patches made the stereo accept the relayed device (full-speed descriptors, the full-speed HID table, fast acks). Session `car-10` is the recording of a working stereo session.
2. **Decoder** (`tools/decode/`). It explained all 27,174 transfers of `car-10`: what the stereo sends, in which order, and what the iPad answers.
3. **Test stereo** (Pi 3 B, `tools/test-stereo/`). It replays the stereo's side of `car-10` byte for byte, so the Pi 5 can be tested on the bench.
4. **Bluetooth bridge** (this folder). The Pi 5 answers like the iPad of `car-10`, with data from a Bluetooth phone. It passed on the bench, then three car runs found what the bench could not show: the stereo's play commands for a stopped iPod, its index-based skips, and clipping. Then came delay reporting.

## 2. Hardware and wiring

```
phone ── Bluetooth ──► Pi 5 (onboard Bluetooth, BCM4345C0)
                         │  USB-C port (device side, dwc2 peripheral, UDC 1000480000.usb)
                         │  USB-C plug to USB-A plug cable, VBUS (pin 1) TAPED
                         ▼
                       Car stereo USB-A socket (USB host, full speed only)

Bench supply 5.1 V ──► GPIO pins 2 and 4 (5 V), pins 6 and 14 (GND)
```

On the bench the Pi 3 B test stereo takes the place of the car stereo, with the same taped cable. The relay wiring (an Apple device on a black USB-A port of the Pi 5) is in [tools/relay/README.md](tools/relay/README.md).

| Item | Facts |
|---|---|
| Stereo | Suzuki PA68L0 = Panasonic CQ-JZ41F0AE, factory unit in a Suzuki Swift of about 2010. USB-A socket. Text display. Full-speed (12 Mbit/s) USB host only. |
| iPhone | iPhone 15 Pro, `05ac:12a8`, iOS 27. Used for the desk and Mac tests. |
| iPad | `05ac:12ab`, reports software version 26.6.1 over iAP. **Both working car sessions used the iPad.** The iPhone has not been re-tested in the car through the final relay. |
| Pi | Raspberry Pi 5, Raspberry Pi OS Lite 64-bit (Debian 13 Trixie), kernel `6.18.50+rpt-rpi-2712`. |
| Pi 3 B | Test stereo: host `pi3-sink`, full-speed USB host like the car (`dwc_otg.speed=1`), audio on the 3.5 mm jack. |

Rules that protect the hardware:

- **Keep VBUS taped** on pin 1 of the USB-A plug that goes to the stereo. Use the same taped cable for Mac tests, with a USB-C to USB-A adapter on the Mac. Never use a USB-C to USB-C cable to a host. Phase 1 measured 0 V on the Pi's USB-C VBUS, so the Pi does not drive it. Current from a host into the Pi's 5 V rail is still unknown.
- **Power:** bench supply at 5.1 V, current limit 5 A, into the GPIO header. Never connect a USB-C power supply at the same time. Use short, thick leads. Thin leads dropped 0.19 V: the SD card then read garbage, and the Pi shut down at about 1 A. Check the voltage under load with `vcgencmd pmic_read_adc EXT5V_V`. It must stay at 5.0 V or more.
- **Shut down before you cut power:** `ssh root@pi5-sniffer.local poweroff`. Hard power cuts during boot once left the Wi-Fi profile as a 0-byte file. The last session on 08/10/2026 ended with a hard cut.
- **In the garage:** key in ACC only. Never run the engine in a closed garage. A flat battery makes the stereo ask for its security code.

## 3. Access to the Pis

| Item | Value |
|---|---|
| Host names | `pi5-sniffer` (the bridge, named in the relay days) and `pi3-sink` (the test stereo), or `<name>.local` over mDNS |
| Login | `root` with an SSH key. User `jack` exists too: key only, passwordless sudo. |
| SSH public key | The user's own key. `prepare-sdcard.sh` reads it with `op read` from the secret reference in `SSH_KEY_REF`. |
| Wi-Fi | One WPA2 network, set with `WIFI_SSID` and `WIFI_PSK_REF` when the card is prepared. The Pi has a NetworkManager keyfile for it. |
| Ethernet to the Mac | No DHCP. IPv6 link-local only. `pi5-sniffer.local` still works. |

Rules:

- **Use WPA2** (`WIFI_KEY_MGMT=psk`). WPA3 (`sae`) association failed on the Pi at a low clock speed.
- **Keep network details and secret references out of the repo.** The repo is public. Pass them to `prepare-sdcard.sh` as environment variables.
- **Never print secrets.** Pipe them into SSH from `op read`, through stdin. cloud-init writes the Wi-Fi password into its logs in plain text. Never print `/var/log/cloud-init*.log` or network configs without a mask that you have tested first. A leak of this type happened once, and the user had to rotate a password.

## 4. Repository map

macOS adds `.DS_Store` files; exclude them when you copy files to a Pi.

```
pi-iap-bt-bridge/
├── README.md               # this file
├── PLAN.md                 # goal, status, open work, decisions, history (phases 0–12), risks R1–R23
├── bridge/                 # ipod-bridge (Go): the iPod side of iAP1, the BlueZ phone, pairing, delay reports
├── files/                  # files installed on the Pi 5: ipod-gadget, ipod-mode, ipod-audio, units, settings, ALSA gain
├── uac1-fs/                # DKMS patch: the kernel UAC1 gadget at full speed (R18)
├── latency/                # analyze.py and selftest.py: the Pi's delay, Bluetooth in to USB out
├── build.sh                # tests and builds ipod-bridge in Docker; output out/ipod-bridge
├── deploy.sh               # builds and installs everything on the Pi 5
├── install-on-pi.sh        # run on the Pi 5 by deploy.sh
├── install-uac1-fs.sh      # run on the Pi 5 by deploy.sh: fetches, patches and builds the UAC1 modules
├── audio-test.sh           # USB audio path without Bluetooth (needs the Pi 3)
├── latency-test.sh         # captures for the delay measurement (needs the Pi 3)
├── tools/
│   ├── relay/              # the USB relay and sniffer: Pi 5 card set-up, usb-proxy patches, rules, capture
│   ├── decode/             # Python decoder for the relay's captures (uv project)
│   └── test-stereo/        # Pi 3 B that plays the car stereo (iap-sink, stereo buttons, audio to the jack)
├── out/                    # build output and latency captures, not in git
└── sessions/               # captures copied from the Pis, and decoder output. PRIVATE, not in git
```

Before 09/10/2026 the bridge was in `pi/ipod/`, the relay in `pi/`, the decoder in `decode/` and the test stereo in `sink/`. The repository was `pi5-usb-sniffer`. Older notes and commits use those paths.

## 5. Set-up

The Pi 5 was built as the relay first: see [tools/relay/README.md](tools/relay/README.md), section 3.1 (card set-up with `tools/relay/prepare-sdcard.sh`, then `setup.sh` on first boot). The relay software stays installed. The bridge goes on top.

On the Mac, with the Pi 5 online. This installs `bluez-alsa-utils`, builds the patched kernel module (DKMS, about 1 minute), builds `ipod-bridge` in Docker, and copies everything:

```bash
./deploy.sh
```

On the Pi 5. This stops the relay, starts the gadget, the bridge and the audio player, and enables them at every boot:

```bash
ssh root@pi5-sniffer.local ipod-mode bt
```

Pair the phone once. The bridge opens a 10 minute pairing window by itself in two cases: no phone is paired when it starts, or no phone is connected 60 s after it started (`IPOD_PAIR_AFTER`). A paired phone in range connects before that, so the window stays closed. Open it by hand with `ipod-bridge ctl pair`. On the phone: Settings, Bluetooth, tap **Pi iPod**. The phone is trusted after pairing. After that the **Pi connects to the phone by itself**, about 30 s after power-on: `ipod-bridge` asks for the classic A2DP connection to every paired and trusted phone that is not connected, every 10 s (every 30 s after a minute without success, for example when the phone is out of range). Nobody has to tap the phone. If the stereo's state is "playing" when the phone connects, the bridge starts the phone's player (section 6.2); else press play on the stereo or the phone.

When the phone deletes the pairing ("Forget This Device"), the Pi still has its key and every connect fails with `br-connection-key-missing`. A Pi with a pairing never opens the window and stays hidden, so the phone cannot pair again. The bridge now removes such a pairing after 3 failed connects in a row and opens the window. `ipod-bridge ctl forget all|<address>|<name>` does the same by hand.

Go back to the relay: `ipod-mode relay`, then `capture start <label>`. The two modes use the same USB-C port, so only one runs.

Do not use a USB-C to USB-C cable to the stereo. Keep VBUS taped (section 2).

The Pi 3 test stereo: [tools/test-stereo/README.md](tools/test-stereo/README.md).

## 6. How it works

### 6.1 Parts

| Part | What it does | Files |
|---|---|---|
| Part | What it does | Files |
|---|---|---|
| USB gadget | An iPad (`05ac:12ab`) with two configurations. The stereo selects configuration 2: USB audio out of the device, and the iAP HID interface (the 96-byte full-speed report descriptor). Built with configfs. | `files/usr/local/sbin/ipod-gadget` |
| Patched audio function | The kernel's UAC1 function has a full-speed bug. See section 6.4. | `uac1-fs/`, `install-uac1-fs.sh` |
| `ipod-bridge` | Go program. The iPod side of iAP1 on `/dev/hidg0`. Gets track data from the phone and sends the stereo's controls to it. Also the pairing agent. | `bridge/` |
| BlueZ + BlueALSA | A2DP sink and AVRCP. `ipod-audio` runs `alsaloop` from the BlueALSA PCM into the gadget sound card, at a fixed delay and gain. | `files/etc/systemd/system/`, `files/usr/local/sbin/ipod-audio` |
| `ipod-mode` | Switches the Pi 5 between this mode and the USB relay. | `files/usr/local/sbin/ipod-mode` |

`ipod-bridge` reuses `oandrew/ipod` (v0.2.0) for packet checksums and the HID report types. Its replies are checked byte for byte against the real iPad in session `car-10`.

### 6.2 What the stereo gets

| Stereo asks | The bridge answers from |
|---|---|
| `IdentifyDeviceLingoes` | Acks, then asks for the stereo's certificate, sample rates and info, and sends a signature challenge, like the iPad. |
| Authentication | The Pi has no Apple key and does not check the signature. It sends "passed" when the signature arrives, or after `IPOD_AUTH_TIMEOUT` (70 s). |
| `GetPlayStatus`, track time notifications | AVRCP status and position (extrapolated between updates), track length. Notifications every 0.5 s while playing. With no phone: the "Waiting" track (below). |
| Title, artist, album, genre of a track | AVRCP track data of the phone. With no phone: `Waiting`. |
| `GetCurrentPlayingTrackIndex`, `GetNumPlayingTracks` | A virtual playlist of 1000 tracks. The index starts at 500 and counts track changes. The stereo shows this number. |
| `PlayCurrentSelection`, `PlayControl` toggle, play, pause, stop | `Play`, `Pause`, `Stop` on the phone's player. `PlayCurrentSelection` is what the stereo sends first when the iPod is stopped. |
| `PlayControl` next and previous, `SetCurrentPlayingTrack` | `Next` or `Previous`. `SetCurrentPlayingTrack` gives the steps from the current index, the short way round the list, up to 5 (quick presses add up). After `PlayControl` previous, a `SetCurrentPlayingTrack` with the old index (the back button's second command) does nothing. |
| Shuffle, repeat | The phone's player properties. |
| `RequestiPodName` | The phone's Bluetooth name, or `IPOD_NAME`. With no phone: `iPod`. |

The phone sends the data of a new track in steps (the album first, the title after). The bridge waits 0.8 s until the data is complete before it counts a track change.

**"Waiting" when no phone has track data.** An empty, stopped iPod makes the stereo show `Unsupported`. So with no phone (or a phone without track data) the stereo gets a track called `Waiting` (title, artist, album, genre, composer), 1 hour long, with a running position. The USB audio carries silence: the kernel clears the buffer when playback stops. The stereo's play and pause switch this track, and the bridge keeps that state. When a phone connects while the stereo's state is "playing", the bridge sends `Play`. Then the phone's track replaces `Waiting` with a new index. While a phone is connected, the state follows the phone. Every play command gets success, so no `ERROR 2` (PLAN.md, Phase 9).

`TrackNewAudioAttributes` is sent after the authentication and repeated every 0.5 s, up to 40 times, until the stereo acks it. Until the first ack it is sent again when play starts and when the track changes. The car stereo acks once per connection; after that it is not sent again.

**Audio level.** `ipod-audio` sends the phone's audio `IPOD_AUDIO_GAIN_DB` lower (default -6 dB), through the ALSA PCM `ipod_gain` (`/etc/alsa/conf.d/60-ipod-gain.conf`). The car stereo clipped loud songs at the full level. The decoded Bluetooth stream itself does not clip (PLAN.md, Phase 10).

### 6.3 Reconnecting to the phone

iPhones do not always connect to an audio device that was off, and BlueZ retries only after a lost link. So the bridge does it. One detail matters: `Device1.Connect()` lets BlueZ choose the bearer. For a dual-mode phone after a restart it scans for Low Energy, never pages the phone, and stays `In Progress` for good (btmon showed only LE scan commands). `Device1.ConnectProfile("0000110a-...")` (the phone's A2DP source service) forces the classic connection: the radio pages at once and the phone is connected in about a second.

Checked on 09/10/2026: a disconnect from the Pi side was undone in 3 s, and a cold reboot ended with the phone connected 27 s later, with nobody touching it. The log line `connected to the phone` is the Pi's own request.

After a connection, the phone's player may have no track data until it plays. If the data arrives after the stereo connected, the bridge counts it as a new track and sends a track index notification, so the stereo asks again.

### 6.4 The full-speed USB audio bug

The kernel's `f_uac1` uses **one descriptor list for full and high speed**, with `bInterval = 4`. That is right for high speed. At full speed, which every car stereo here uses, `bInterval` must be 1. `u_audio` plans its packets from it: `1000 / (1 << (bInterval - 1))` is 125 packets per second, each meant for 352 frames. The stereo got isochronous packets of **0 or 200 bytes**, about 56% of real time. Bluetooth audio then piled up in BlueALSA (`PCM overrun`) while the stereo side ran dry (`underrun`). The sound was "extremely jittery".

The fix is `uac1-fs/f_uac1-fullspeed.patch` (42 lines): full-speed copies of the two isochronous endpoint descriptors with `bInterval = 1` and no synchronisation type, like the iPhone. `install-uac1-fs.sh` fetches `f_uac1.c`, `u_audio.c`, `u_audio.h`, `u_uac1.h` and `uac_common.h` from the Raspberry Pi kernel at a pinned commit, checks SHA-256, applies the patch and builds both modules with DKMS into `updates/dkms`. DKMS rebuilds them for a new kernel, which needs the network once.

Measured with `audio-test.sh` (a 440 Hz tone through the gadget, recorded on the Pi 3, no Bluetooth):

| | discontinuities per second | silent gaps |
|---|---|---|
| stock kernel module | about 15, for the first 5 to 9 s | 0 |
| patched module | 0 | 0 |

Also set in `ipod-gadget`: no mute and volume controls on the capture side (their defaults add an interrupt endpoint that the iPhone does not have), and `req_number=16` for margin. To go back to the stock module: `dkms remove usb_f_uac1_fs/<version> --all && depmod -a`, then reboot.

### 6.5 Delay reporting

The phone delays its video by the delay that the audio sink reports: A2DP delay reporting (AVDTP 1.3, the signal `DELAY_REPORT`), as AirPods do. The iPad offers it on all its stream endpoints (`DelayReporting: true`), and BlueZ shows the stream's `Delay` property. BlueALSA 4.3.1 only reads that property (for a remote speaker's delay) and never sets it for a sink, so the iPad got 0.

| Part | How |
|---|---|
| Report | `ipod-bridge` sets `org.bluez.MediaTransport1.Delay` (1/10 ms). BlueZ sends `DELAY_REPORT` at once. BlueZ lets other programs set it only while BlueALSA does not hold the transport, so the bridge sets it when the transport is idle, for example right after the phone connects. BlueZ sends the value again for a new stream configuration. |
| Value | `IPOD_AUDIO_LATENCY_MS` + the measured rest of the pipeline (`pipelineExtra` in `bridge/main.go`) + `IPOD_EXTRA_DELAY_MS`. |
| Constant delay | `alsaloop` holds the delay between the BlueALSA PCM and the gadget at `IPOD_AUDIO_LATENCY_MS`, and resamples (`-S 4`) the small difference between the phone's clock and the stereo's USB clock. `bluealsa-aplay` kept its buffer full but let BlueALSA's pipe (up to 1.5 s) take up the difference, so its delay could grow during a drive. |
| No feedback | The BlueALSA ALSA plugin adds BlueALSA's `Delay`, which includes the reported value, to its capture delay. `alsaloop` would then cut its buffer by that much. The bridge sets BlueALSA's `DelayAdjustment` to minus the reported value. |

Checked on 09/10/2026 with `btmon`: the Pi sends `90 0d 04 13 89` (`DELAY_REPORT`, endpoint 1, 500.1 ms) and the iPad answers `92 0d` (accept).

**Measuring the Pi's delay.** `latency-test.sh` needs the Pi 5 on the Pi 3 by USB (an `iap-sink` session) and the phone connected. It plays the phone over AVRCP for 60 s and captures:

- `btmon` on the Pi 5: the arrival of each Bluetooth audio packet (RTP with SBC frames), on the Pi 5 clock.
- `usbmon` on the Pi 3: each isochronous audio packet that leaves the gadget, on the Pi 3 clock.
- The `ipod-bridge` trace: the HID reports of the iAP session, which `usbmon` also has. The Pi 3 sends a report before the Pi 5 reads it, and the Pi 5 writes one before the Pi 3 gets it. That bounds the offset between the two clocks, to a fraction of a millisecond.

`latency/analyze.py` decodes the SBC frames with ffmpeg, finds each 0.25 s piece of sound in the USB audio, and gives the delay of the first sample of each Bluetooth packet (a packet's samples all arrive at once) and the average sample delay, which is the value to report. `latency/selftest.py` builds a capture with a known delay and clock offset and checks the result (it finds the offset to 0.05 ms and the delay to the expected value).

## 7. Settings and tools

`/etc/ipod-bridge.conf` (restart with `systemctl restart ipod-bridge`):

| Variable | Default | Use |
|---|---|---|
| `IPOD_NAME` | empty | Name that the stereo gets. Empty: the phone's Bluetooth name. |
| `IPOD_BT_NAME` | `Pi iPod` | Bluetooth name of the Pi, as the phone shows it. |
| `IPOD_AUTH_TIMEOUT` | `70s` | When the iPod passes the authentication without a signature. |
| `IPOD_RECONNECT_EVERY` | `10s` | The Pi connects to the paired phones by itself at this interval. `0` switches it off. |
| `IPOD_PAIR_AFTER` | `60s` | The pairing window opens by itself when no phone is connected this long after start. `0` switches it off. |
| `IPOD_AUDIO_GAIN_DB` | `-6` | The phone's audio goes to the stereo this many dB lower. `0`: unchanged. Restart `ipod-audio` after a change. |
| `IPOD_AUDIO_LATENCY_MS` | `500` | `alsaloop`'s fixed delay. The reported delay follows it. Restart `ipod-audio` and `ipod-bridge`. |
| `IPOD_EXTRA_DELAY_MS` | `0` | Added to the reported delay, for the stereo's own delay (not measured). |
| `IPOD_DELAY_REPORT` | on | `0`: do not report the delay to the phone. |
| `IPOD_AUDIO_PLAYER` | `alsaloop` | `aplay`: the earlier `bluealsa-aplay`, whose delay can grow during a drive. |
| `IPOD_DEBUG`, `IPOD_TRACE_HID` | off, on | More log lines. A raw HID report trace. |

```bash
ipod-bridge ctl status        # what the stereo and the phone report, packet counts
ipod-bridge ctl devices       # paired phones
ipod-bridge ctl pair [secs]   # open the pairing window
ipod-bridge ctl forget all|<address>|<name>   # remove a pairing (and open the window if no phone is left)
ipod-bridge ctl toggle|play|pause|next|prev    # press the phone's buttons (AVRCP)
journalctl -u ipod-bridge -f
```

Packet traces are in `/var/lib/ipod-bridge/traces/session-*.jsonl` (`A>D` comes from the stereo, `D>A` is sent by the Pi).

## 8. Tests

```bash
./build.sh                       # gofmt, vet, tests with the race detector, arm64 build
./audio-test.sh [seconds]        # USB audio path without Bluetooth (needs the Pi 3 test stereo)
./latency-test.sh [seconds]      # the Pi's delay, Bluetooth in to USB out (section 6.5)
uv run latency/selftest.py       # checks the latency analysis with a made-up capture
tools/test-stereo/build.sh       # the test stereo's tests and arm64 build
uv run --project tools/decode pytest tools/decode/tests   # the decoder's tests
ssh root@pi3-sink.local stereo playpause    # stereo buttons on the bench
ssh root@pi3-sink.local stereo next
ssh root@pi3-sink.local stereo back
```

The Go tests run the bridge against a scripted stereo and a fake phone, over the same report framing as `/dev/hidg0`: authentication, notifications, every control mapping, stepwise metadata, and the iPad's exact reply bytes.

## 9. Protocol facts

### 9.1 Apple device descriptors

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

### 9.2 iAP over HID

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

### 9.3 iAP1 framing

- Packet: `55 len lingo cmd [txid16] data… checksum`. Large packet: `55 00 len16 …`. Over USB there is no `FF` sync byte.
- Checksum: two's complement of the byte sum from the length byte(s) to the end of the data.
- The Extended Interface lingo (0x04) has 2-byte command IDs; all other lingoes have 1-byte IDs.
- This stereo uses the old identification (`IdentifyDeviceLingoes`, not IDPS), so it uses no transaction IDs.

### 9.4 How the stereo behaves (relay sessions, 08/10/2026)

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

Learned by the bridge in the car (09/10/2026), with no relay in between:

- When the iPod says "stopped", the stereo starts play itself, about 3 s after it connects: `PlayCurrentSelection(0xFFFFFFFF)`, then `PlayControl` toggle. In `car-10` the iPad was paused with a track, so this never showed there.
- An error reply to any command shows on the display: `Error 2` or `Unsupported` (status 2 is "command failed", status 5 "unknown ID"; the mapping to the text is likely but not proven). After errors the stereo may stop polling for minutes.
- A stopped iPod with an empty title and length 0 also gives `Unsupported`.
- Next is `SetCurrentPlayingTrack(index + 1)`. The stereo wraps it to 0 at the last track count it read, and it reads the count before the index changes. Quick presses add up to one index several tracks away.
- Back, 2 s or more into a track: toggle, `PlayControl` 0x04 (previous track), 1.2 s later `SetCurrentPlayingTrack` with the same index, end fast forward/rewind, toggle again if paused. Earlier in a track: `SetCurrentPlayingTrack(index - 1)`.
- The stereo acks `TrackNewAudioAttributes` once per connection, when it is ready for audio. It sends UAC1 `SET_CUR` for the sample rate to endpoint 0x81.
- The signature still comes about 57 s after the challenge, as in `car-10`.
- The stereo clips loud songs at the full digital level of the Bluetooth stream. The decoded stream itself does not clip.

## 10. Quirks and gotchas

The bridge:

- Writing `function_name` in the UAC1 configfs function hangs on kernel 6.18. `ipod-gadget` does not set it, so the ALSA card is `UAC1Gadget`.
- `echo` adds a newline to the HID report descriptor (97 bytes in place of 96). `ipod-gadget` writes it with `printf '%s'`.
- The DKMS build of the UAC1 modules also needs `uac_common.h`.
- BlueZ is not ready for a moment after a restart or an rfkill change (`set Powered=true: Failed`). The bridge retries 10 times, 1 s apart.
- When the host disconnects, a read of `/dev/hidg0` fails with `ENOMEM` and the kernel logs `End Point Request ERROR: -108`. The bridge ends the session and waits for the UDC state `configured`.
- `f_uac1` acks the stereo's endpoint `SET_CUR` (sample rate, endpoint 0x81) but does not apply it: it compares hard-coded endpoint numbers. That does no harm at 44.1 kHz.
- When nothing plays into the gadget, it sends silence: the kernel clears the buffer when playback stops.
- The BlueALSA ALSA plugin prints debug lines (`D: bluealsa-pcm.c`) into the `ipod-audio` journal.
- The Pi 5 kernel has no dynamic debug. To watch the stereo's USB control requests, enable the `gadget` events in `/sys/kernel/tracing` (`usb_ep_queue` on ep0, `usb_ep_enable`, `usb_gadget_set_state`).
- `btmon` text output is lost when `timeout` kills it: capture with `btmon -w` and read back with `btmon -r`. A `btmon` that starts after the channels are open cannot name AVDTP signals; look for the raw bytes.

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
- In zsh, a variable that holds several SSH options does not split. Use a bash array: `O=(-o BatchMode=yes)`, then `ssh "${O[@]}"`.
- The Mac's `rsync` has no `--chown`. The deploy scripts copy as root instead.
- `ffmpeg` (Homebrew) has the SBC codec; `latency/analyze.py` and `latency/selftest.py` use it. `tshark` is not installed.

## 11. Not verified

- **Clipping.** Less after the fixed -6 dB, but some remains (third car run). `IPOD_AUDIO_GAIN_DB` can go lower.
- **`alsaloop` with a real USB host.** Deployed with no host attached. Its sound, its resampling and its behaviour when the phone pauses are not checked yet.
- **The Pi's measured delay.** The report is `IPOD_AUDIO_LATENCY_MS` until `latency-test.sh` has run on the bench (section 6.5). The stereo's own delay is not in it.
- **That iOS uses the report.** The iPad accepts it (AVDTP accept). Whether its video then matches the car's sound is not checked.
- **The iPad's volume.** It has no effect on the sound (absolute volume with a fixed gain). Plain Bluetooth has no documented way to disable the iPad's slider; a greyed-out slider is reported with CarPlay (iAP2, Apple authentication chip). Left as it is, by decision.
- **Wi-Fi next to Bluetooth.** Both share the radio. A busy Wi-Fi link may disturb the audio.
- **Skips more than one track** are sent as several `Next` or `Previous` commands, 0.3 s apart. Not tried with a phone.
- **"Previous" at the start of a track.** The phone restarts the track when it is more than about 3 s in, the stereo when it is more than about 2 s in. Between the two, one press can go a track back where the stereo meant a restart.
- **One phone at a time.** The bridge follows a playing player first, then the latest.
- No artwork, no playlist browsing, no fast forward end event (BlueZ has no "release").

## 12. Data and privacy

| Place | Content |
|---|---|
| Mac `sessions/20261008-221642-car-10/` | Complete car session (61 MB) and `decoded/`. **The main data set for the decoder.** |
| Mac `sessions/20261008-223159-car-15-4cores/` | **Partial.** The pcapng is missing, because the Pi went offline during the copy. `proxy.log` and the small files are present. |
| Pi 5 `/var/lib/ipod-bridge/traces/` | Every iAP packet of each bridge session with the stereo, and the raw HID reports. Holds the stereo's certificate. |
| Pi 3 `/var/lib/iap-sink/traces/` | The same for the test stereo. |
| Mac `out/latency-*/` | Bluetooth and USB captures of the delay measurement: audio, HID reports, the stereo's certificate. |

Treat all session data as private. Never publish it or upload it to an external service.

- Captures hold the stereo's MFi certificate and the device's serial number.
- They also hold the device name and track, artist and album names.
- The Mac test sessions on the Pi hold usbmux pairing data.

The `decoded/` folders hold the same data in readable form.

## 13. Working with this user

- Write replies in ASD-STE100 (Simplified Technical English), with short sentences. Use the future tense for work that you have not done yet.
- Dates are dd/mm/yyyy. Units are metric. The user is in New Zealand: the Pi runs `Pacific/Auckland`, and the logs use UTC.
- The user does the physical work: cables, power and the car. Say exactly what to plug in or measure, and wait for confirmation.
- Ask before you do anything that can damage hardware or the car battery.
- Ask clarifying questions before decisions, also small ones. The user decides design choices; the PLAN lists the decisions so far.
- The repository is public. Keep phone names, Bluetooth addresses, network names and secret references out of it.

## 14. References

- `xairy/raw-gadget` and `AristoChen/usb-proxy` on GitHub: the relay base.
- `oandrew/ipod` and `oandrew/ipod-gadget` (Go): iAP1 over HID framing, lingo tables, and the full-speed HID report descriptor (`gadget/ipod.h`).
- Rockbox `apps/iap/`: an iPod-side iAP1 implementation.
- Linux `drivers/usb/misc/apple-mfi-fastcharge.c`: the Apple charge request.
- Raspberry Pi white paper RP-009276-WP: Pi 5 USB gadget mode.
- BlueZ 5.82 (`profiles/audio/transport.c`, `avdtp.c`, `avrcp.c`) and BlueALSA 4.3.1 (`src/asound/bluealsa-pcm.c`, `utils/aplay/`): read for delay reporting and volume.
- Bluetooth Accessory Design Guidelines for Apple Products: AVRCP Absolute Volume, and the volume behaviour of iOS.
