# Bluetooth iPod: the Pi 5 as an iPod for the car stereo

The phone plays over Bluetooth. The Pi 5 is the Bluetooth audio sink, and it is an iPod to the stereo. The stereo gets the audio over USB, the track data over iAP1, and it controls the phone: play, pause, next.

```
phone ──Bluetooth A2DP──► Pi 5 ──BlueALSA──► USB gadget sound card ──► USB-C cable ──► car stereo
  ▲     AVRCP metadata       │                                          (VBUS taped)      (USB host)
  └─────AVRCP controls───────┴── ipod-bridge ◄── iAP1 over HID (/dev/hidg0) ◄──────────────┘
```

Bench set-up: the Pi 3 sink (`../../sink/`) plays the stereo. It has the same USB host behaviour (full speed) and a 3.5 mm jack for the audio.

## 1. Parts

| Part | What it does | Files |
|---|---|---|
| USB gadget | An iPad (`05ac:12ab`) with two configurations. The stereo selects configuration 2: USB audio out of the device, and the iAP HID interface (the 96-byte full-speed report descriptor). Built with configfs. | `files/usr/local/sbin/ipod-gadget` |
| Patched audio function | The kernel's UAC1 function has a full-speed bug. See section 5. | `uac1-fs/`, `install-uac1-fs.sh` |
| `ipod-bridge` | Go program. The iPod side of iAP1 on `/dev/hidg0`. Gets track data from the phone and sends the stereo's controls to it. Also the pairing agent. | `bridge/` |
| BlueZ + BlueALSA | A2DP sink and AVRCP. `bluealsa-aplay` plays the phone's audio into the gadget sound card. | `files/etc/systemd/system/` |
| `ipod-mode` | Switches the Pi 5 between this mode and the USB relay. | `files/usr/local/sbin/ipod-mode` |

`ipod-bridge` reuses `oandrew/ipod` (v0.2.0) for packet checksums and the HID report types. Its replies are checked byte for byte against the real iPad in session `car-10`.

## 2. Set-up

On the Mac, with the Pi 5 online. This installs `bluez-alsa-utils`, builds the patched kernel module (DKMS, about 1 minute), builds `ipod-bridge` in Docker, and copies everything:

```bash
pi/ipod/deploy.sh
```

On the Pi 5. This stops the relay, starts the gadget, the bridge and the audio player, and enables them at every boot:

```bash
ssh root@pi5-sniffer.local ipod-mode bt
```

Pair the phone once. The bridge opens a 10 minute pairing window by itself when no phone is paired. Open it again with `ipod-bridge ctl pair`. On the phone: Settings, Bluetooth, tap **Pi iPod**. The phone is trusted after pairing. After that the **Pi connects to the phone by itself**, about 30 s after power-on: `ipod-bridge` asks for the classic A2DP connection to every paired and trusted phone that is not connected, every 10 s (every 30 s after a minute without success, for example when the phone is out of range). Nobody has to tap the phone. The phone's player stays paused until the stereo presses play, or you do.

Go back to the relay: `ipod-mode relay`, then `capture start <label>`. The two modes use the same USB-C port, so only one runs.

Do not use a USB-C to USB-C cable to the stereo. Keep VBUS taped, as in the main README.

## 3. What the stereo gets

| Stereo asks | The bridge answers from |
|---|---|
| `IdentifyDeviceLingoes` | Acks, then asks for the stereo's certificate, sample rates and info, and sends a signature challenge, like the iPad. |
| Authentication | The Pi has no Apple key and does not check the signature. It sends "passed" when the signature arrives, or after `IPOD_AUTH_TIMEOUT` (70 s). |
| `GetPlayStatus`, track time notifications | AVRCP status and position (extrapolated between updates), track length. Notifications every 0.5 s while playing. |
| Title, artist, album, genre of a track | AVRCP track data of the phone. |
| `GetCurrentPlayingTrackIndex`, `GetNumPlayingTracks` | A virtual playlist: the index counts track changes and the count is index + 2, so "next" always has a target. |
| `PlayControl` toggle, play, pause, stop | `Play`, `Pause`, `Stop` on the phone's player. |
| `PlayControl` next and previous, `SetCurrentPlayingTrack` | `Next` or `Previous`. A higher index than the current one is next, a lower one is previous. |
| Shuffle, repeat | The phone's player properties. |
| `RequestiPodName` | The phone's Bluetooth name, or `IPOD_NAME`. |

The phone sends the data of a new track in steps (the album first, the title after). The bridge waits 0.8 s until the data is complete before it counts a track change.

## 4. Settings and tools

`/etc/ipod-bridge.conf` (restart with `systemctl restart ipod-bridge`):

| Variable | Default | Use |
|---|---|---|
| `IPOD_NAME` | empty | Name that the stereo gets. Empty: the phone's Bluetooth name. |
| `IPOD_BT_NAME` | `Pi iPod` | Bluetooth name of the Pi, as the phone shows it. |
| `IPOD_AUTH_TIMEOUT` | `70s` | When the iPod passes the authentication without a signature. |
| `IPOD_RECONNECT_EVERY` | `10s` | The Pi connects to the paired phones by itself at this interval. `0` switches it off. |
| `IPOD_DEBUG`, `IPOD_TRACE_HID` | off, on | More log lines. A raw HID report trace. |

```bash
ipod-bridge ctl status        # what the stereo and the phone report, packet counts
ipod-bridge ctl devices       # paired phones
ipod-bridge ctl pair [secs]   # open the pairing window
ipod-bridge ctl toggle|play|pause|next|prev    # press the phone's buttons (AVRCP)
journalctl -u ipod-bridge -f
```

Packet traces are in `/var/lib/ipod-bridge/traces/session-*.jsonl` (`A>D` comes from the stereo, `D>A` is sent by the Pi).

## 5. The full-speed USB audio bug

The kernel's `f_uac1` uses **one descriptor list for full and high speed**, with `bInterval = 4`. That is right for high speed. At full speed, which every car stereo here uses, `bInterval` must be 1. `u_audio` plans its packets from it: `1000 / (1 << (bInterval - 1))` is 125 packets per second, each meant for 352 frames. The stereo got isochronous packets of **0 or 200 bytes**, about 56% of real time. Bluetooth audio then piled up in BlueALSA (`PCM overrun`) while the stereo side ran dry (`underrun`). The sound was "extremely jittery".

The fix is `uac1-fs/f_uac1-fullspeed.patch` (42 lines): full-speed copies of the two isochronous endpoint descriptors with `bInterval = 1` and no synchronisation type, like the iPhone. `install-uac1-fs.sh` fetches `f_uac1.c`, `u_audio.c`, `u_audio.h`, `u_uac1.h` and `uac_common.h` from the Raspberry Pi kernel at a pinned commit, checks SHA-256, applies the patch and builds both modules with DKMS into `updates/dkms`. DKMS rebuilds them for a new kernel, which needs the network once.

Measured with `audio-test.sh` (a 440 Hz tone through the gadget, recorded on the Pi 3, no Bluetooth):

| | discontinuities per second | silent gaps |
|---|---|---|
| stock kernel module | about 15, for the first 5 to 9 s | 0 |
| patched module | 0 | 0 |

Also set in `ipod-gadget`: no mute and volume controls on the capture side (their defaults add an interrupt endpoint that the iPhone does not have), and `req_number=16` for margin. To go back to the stock module: `dkms remove usb_f_uac1_fs/<version> --all && depmod -a`, then reboot.

## 6. Reconnecting to the phone

iPhones do not always connect to an audio device that was off, and BlueZ retries only after a lost link. So the bridge does it. One detail matters: `Device1.Connect()` lets BlueZ choose the bearer. For a dual-mode phone after a restart it scans for Low Energy, never pages the phone, and stays `In Progress` for good (btmon showed only LE scan commands). `Device1.ConnectProfile("0000110a-...")` (the phone's A2DP source service) forces the classic connection: the radio pages at once and the phone is connected in about a second.

Checked on 09/10/2026: a disconnect from the Pi side was undone in 3 s, and a cold reboot ended with the phone connected 27 s later, with nobody touching it. The log line `connected to the phone` is the Pi's own request.

After a connection, the phone's player may have no track data until it plays. If the data arrives after the stereo connected, the bridge counts it as a new track and sends a track index notification, so the stereo asks again.

## 7. Tests

```bash
pi/ipod/build.sh                 # gofmt, vet, tests with the race detector, arm64 build
pi/ipod/audio-test.sh [seconds]  # USB audio path without Bluetooth (needs the Pi 3 sink)
ssh root@pi3-sink.local stereo playpause    # stereo buttons on the bench
ssh root@pi3-sink.local stereo next
```

The Go tests run the bridge against a scripted stereo and a fake phone, over the same report framing as `/dev/hidg0`: authentication, notifications, every control mapping, stepwise metadata, and the iPad's exact reply bytes.

## 8. Bench results (09/10/2026, iPhone 15 Pro as the phone, Pi 3 as the stereo)

| Check | Result |
|---|---|
| Stereo session through the gadget | Pass. Full-speed link, certificate accepted, all init queries answered. |
| Audio | Pass after the patch. 0 `aplay` underruns and 0 BlueALSA overruns in about 7 minutes. 0 `alsaloop` underruns on the Pi 3. |
| Track data on the stereo side | Title, artist, album, position, length and a changing index arrive. |
| `stereo playpause` | Pass. The phone pauses and plays. |
| `stereo next` | Pass. One press is one track change. |
| Cold start, nobody touching the phone | Pass. The Pi connects the phone about 30 s after power-on. Track data is on the stereo side before any button. The stereo's play button starts the music. |

## 9. Not verified

- **The car stereo.** Nothing here ran with the Panasonic CQ-JZ41F0AE. The gadget copies the iPad's identity and configuration 2, the stereo's own packets and the iPad's replies, but the stereo may check more.
- **Clock drift** between the Bluetooth source and the USB host. The gadget buffer is nearly full. No dropped frames in 7 minutes.
- **Wi-Fi next to Bluetooth.** Both share the radio. A busy Wi-Fi link may disturb the audio.
- **Jumps** with `SetCurrentPlayingTrack` by more than one track are treated as one next or previous.
- **One phone at a time.** The bridge follows a playing player first, then the latest.
- No artwork, no playlist browsing, no fast forward end event (BlueZ has no "release").
