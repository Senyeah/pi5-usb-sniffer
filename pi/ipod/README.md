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
| BlueZ + BlueALSA | A2DP sink and AVRCP. `ipod-audio` runs `alsaloop` from the BlueALSA PCM into the gadget sound card, at a fixed delay and gain. | `files/etc/systemd/system/`, `files/usr/local/sbin/ipod-audio` |
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

Pair the phone once. The bridge opens a 10 minute pairing window by itself in two cases: no phone is paired when it starts, or no phone is connected 60 s after it started (`IPOD_PAIR_AFTER`). A paired phone in range connects before that, so the window stays closed. Open it by hand with `ipod-bridge ctl pair`. On the phone: Settings, Bluetooth, tap **Pi iPod**. The phone is trusted after pairing. After that the **Pi connects to the phone by itself**, about 30 s after power-on: `ipod-bridge` asks for the classic A2DP connection to every paired and trusted phone that is not connected, every 10 s (every 30 s after a minute without success, for example when the phone is out of range). Nobody has to tap the phone. The phone's player stays paused until the stereo presses play, or you do.

When the phone deletes the pairing ("Forget This Device"), the Pi still has its key and every connect fails with `br-connection-key-missing`. A Pi with a pairing never opens the window and stays hidden, so the phone cannot pair again. The bridge now removes such a pairing after 3 failed connects in a row and opens the window. `ipod-bridge ctl forget all|<address>|<name>` does the same by hand.

Go back to the relay: `ipod-mode relay`, then `capture start <label>`. The two modes use the same USB-C port, so only one runs.

Do not use a USB-C to USB-C cable to the stereo. Keep VBUS taped, as in the main README.

## 3. What the stereo gets

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

**"Waiting" when no phone has track data.** An empty, stopped iPod makes the stereo show `Unsupported`. So with no phone (or a phone without track data) the stereo gets a track called `Waiting` (title, artist, album, genre, composer), 1 hour long, with a running position. The USB audio carries silence: the kernel clears the buffer when playback stops. The stereo's play and pause switch this track, and the bridge keeps that state. When a phone connects while the stereo's state is "playing", the bridge sends `Play`. Then the phone's track replaces `Waiting` with a new index. While a phone is connected, the state follows the phone. Every play command gets success, so no `ERROR 2` (section 10).

`TrackNewAudioAttributes` is sent after the authentication and repeated every 0.5 s, up to 40 times, until the stereo acks it. Until the first ack it is sent again when play starts and when the track changes. The car stereo acks once per connection; after that it is not sent again.

**Audio level.** `ipod-audio` sends the phone's audio `IPOD_AUDIO_GAIN_DB` lower (default -6 dB), through the ALSA PCM `ipod_gain` (`/etc/alsa/conf.d/60-ipod-gain.conf`). The car stereo clipped loud songs at the full level. The decoded Bluetooth stream itself does not clip (section 11).

## 4. Settings and tools

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
pi/ipod/latency-test.sh [seconds]  # the Pi's delay, Bluetooth in to USB out (section 12)
uv run pi/ipod/latency/selftest.py # checks the latency analysis with a made-up capture
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

- **The fixes of section 11 in the car**: the "Waiting" track, the skips and the -6 dB level. They pass the tests and were deployed with the car off.
- **`alsaloop` with a real USB host.** Deployed with no host attached. Its sound, its resampling and its behaviour when the phone pauses are not checked yet.
- **The Pi's measured delay.** The report is `IPOD_AUDIO_LATENCY_MS` until `latency-test.sh` has run on the bench (section 12). The stereo's own delay is not in it.
- **That iOS uses the report.** The iPad accepts it (AVDTP accept). Whether its video then matches the car's sound is not checked.
- **The iPad's volume.** It has no effect on the sound (absolute volume with a fixed gain). Plain Bluetooth has no documented way to disable the iPad's slider; a greyed-out slider is reported with CarPlay (iAP2, Apple authentication chip). Left as it is, by decision.
- **Wi-Fi next to Bluetooth.** Both share the radio. A busy Wi-Fi link may disturb the audio.
- **Skips more than one track** are sent as several `Next` or `Previous` commands, 0.3 s apart. Not tried with a phone.
- **"Previous" at the start of a track.** The phone restarts the track when it is more than about 3 s in, the stereo when it is more than about 2 s in. Between the two, one press can go a track back where the stereo meant a restart.
- **One phone at a time.** The bridge follows a playing player first, then the latest.
- No artwork, no playlist browsing, no fast forward end event (BlueZ has no "release").

## 10. First run in the car (09/10/2026)

The Pi 5 was connected to the Panasonic CQ-JZ41F0AE with no phone connected: the phone had deleted its pairing and the hidden Pi could not pair again (section 2). The stereo showed `Error 2` and `Unsupported`, a different one each time.

What the traces (`/var/lib/ipod-bridge/traces`) and the kernel USB events show:

| Check | Result |
|---|---|
| USB | Full speed. The stereo reads the descriptors, selects configuration 2 and, at `EnterExtendedInterfaceMode`, audio alternate setting 1. No request is stalled. |
| iAP | Identify, certificate (946 bytes), init queries and the polling every 0.6 s all work. The stereo's signature came 57 s after the challenge, as in car-10 (56 to 59 s). |
| First play requests | About 3 s after the start the stereo sends `PlayCurrentSelection(0xFFFFFFFF)` and `PlayControl` toggle, 35 to 60 ms apart. The bridge answered the first with status 5 and the second with status 2. |
| After the errors | In session `010258` the stereo polled for 6 s, then sent nothing for about 6 minutes. |

The stereo starts play itself when the iPod says "stopped". In car-10 the iPad was paused with a track, so the stereo never sent these two commands, and the Pi 3 test stereo (a copy of car-10) never did. Status 2 is "command failed" and status 5 is "unknown ID". That fits the two messages, but the display text was not matched to the codes with certainty.

Fixes: the bridge answers both commands with success, also with no phone (and starts the phone when there is one), sends the audio attributes again when play starts, forgets dead pairings and opens the pairing window by itself. The Pi 3 test stereo now sends the same two commands when the iPod is stopped, and `iap-sink ctl status` lists every error reply in `errors` (the `stereo` command prints `STEREO ERROR`).

## 11. Second run in the car (09/10/2026), with the iPad over Bluetooth

The iPad paired, and play, pause (the stereo's mute), DISP, RDM and RPT worked. Three problems remained:

| Problem | Cause (traces of sessions `013439`, `013909`, `014206`) | Fix |
|---|---|---|
| `Unsupported` when USB is selected and no phone is connected | The stereo asked for the title and album of the track and got empty text, with "stopped" and length 0. | The "Waiting" track (section 3). |
| Next sometimes goes back | The stereo's next is `SetCurrentPlayingTrack(index + 1)`, wrapped to 0 at its last read track count. It reads the count before the index changes, so with count = index + 2 the next press after a skip went to 0. The bridge sent `Previous`, and the iPad restarted the track. | 1000 virtual tracks, the index starts at 500, steps go the short way round. |
| Back sometimes goes forward; quick presses lost | At a low index the stereo wraps back to the end of the list, which looked like "next". Two quick presses come as one index two tracks away, which was one skip. The back button sends toggle, `PlayControl` 0x04, then `SetCurrentPlayingTrack` with the old index, which could skip forward again. | The same list; several steps; the back button's second command is ignored. |
| Clipping on loud parts | The decoded Bluetooth stream does not clip: 25 s of a loud song from the iPad had its peak at -4.2 dBFS, RMS -14.4 dBFS, no sample at full scale. The iPad's volume (38 of 127) does not change it. The iPad's USB audio in car-10 had RMS -19.5 to -26.3 dBFS (other songs). The stereo itself clips. | 6 dB less (`IPOD_AUDIO_GAIN_DB`). The ALSA `route` plugin was checked on the Pi: exactly -6.00 dB. |

Also: the bridge sent `TrackNewAudioAttributes` 15 to 40 times after every track change, and the stereo acked only the first time in each connection. It now stops after the first ack.

The Pi 3 test stereo has the stereo's back button (`stereo back`), as recorded here.

## 12. Delay reporting (09/10/2026)

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
