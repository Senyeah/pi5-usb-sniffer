# Pi 3 sink: a car stereo for testing the Pi 5 relay

A Raspberry Pi 3 B plays the part of the car stereo. The Pi 5 relay plugs into the Pi 3 in place of the car. The Pi 3 selects the Apple USB configuration, speaks iAP1 like the Panasonic stereo in session `car-10`, and plays the USB audio on its 3.5 mm jack.

```
Apple device ── Pi 5 relay ── USB-C cable (VBUS taped) ──► Pi 3 USB-A port   (the Pi 3 is the USB host)
 (USB device)   usb-proxy                                    iap-sink: iAP1 over HID
                                                             sink-audio: USB audio ──► 3.5 mm jack
```

## Press the stereo buttons

From your Mac, one line each (the Pi 3 must have a connected Apple device):

```bash
ssh root@pi3-sink.local stereo playpause
ssh root@pi3-sink.local stereo next
```

`stereo` sends what the car stereo sends for the button, waits 1.5 s, and prints the result, for example `playing | track 4/12 | Title - Artist | 0:42/3:15`. `playpause` sends end-fast-forward/rewind and then toggle. `next` sends `SetCurrentPlayingTrack(index + 1)` and end-fast-forward/rewind. It sends `PlayControl` Next instead when the device reports an index beyond its track count (the iPhone does). More commands: `iap-sink ctl help`.

## 1. What is reused from `oandrew/ipod`

`oandrew/ipod` is the **iPod side**: it answers a stereo. This project needs the **stereo side**, so the upstream program cannot run here. `iap-sink` reuses the upstream library (`github.com/oandrew/ipod` v0.2.0, commit `3762132`): packet framing, checksums, the lingo tables and the HID report decoder. The accessory side is new code: [iap-sink/](iap-sink/).

## 2. What `iap-sink` does

1. Waits for a USB device with vendor ID `05ac`. Does `SET_CONFIGURATION 2` through sysfs, as the stereo does. Linux picks configuration 1 (PTP) on its own.
2. Opens the HID interface (`/dev/hidrawN`). Reads the report descriptor and builds the report table from it (96 bytes at full speed, 208 bytes at high speed).
3. Sends the same iAP1 packets as the stereo in `car-10`, in the same order:
   - `IdentifyDeviceLingoes` (General, DisplayRemote, ExtendedInterface, DigitalAudio).
   - The MFi certificate in two sections (500 and 446 bytes), about 0.9 s after the iPod asks for it.
   - Sample rates 32, 44.1 and 48 kHz, and accessory capabilities `0x00000001`.
   - The init queries (protocol versions, software version, extended interface mode, play status, track count, name).
   - `GetPlayStatus` every 0.6 s, track queries when the track changes.
   - One `AccessoryAck` for `TrackNewAudioAttributes`, 2.25 s after the first one.
4. Presses play when the iPod is not playing (`IAP_AUTOPLAY=1`): end-fast-forward, then toggle, as the stereo does.

Checked against the capture: all 922 packets that the stereo sent in `car-10` re-encode byte for byte, and the certificate sections equal the captured packets.

### Limit: no signature

The iPod sends a 20-byte challenge and expects a 128-byte signature. The private key is inside the stereo's MFi chip. It is not in a capture and not in this repository. The Pi 3 sends the **real certificate** and **no signature**. The real stereo also needed about 56 s for the signature, and audio and controls worked in that time. What the iPod does after that is not known; this is a test result to collect. `IAP_SIGNATURE=bogus` sends an invalid signature after 56 s, to see the reaction.

## 3. Set-up

1. Flash Raspberry Pi OS Lite 64-bit (Trixie, with cloud-init) to the SD card. Do not use the Imager's own customisation.
2. Personalise the card on the Mac. This writes Wi-Fi, users, host name `pi3-sink` and kernel options. The values come from environment variables, like `pi/prepare-sdcard.sh`:

```bash
WIFI_SSID=... WIFI_PSK_REF=op://<vault>/<item>/<field> SSH_KEY_REF=op://<vault>/<item>/<field> \
  sink/prepare-sdcard.sh /Volumes/bootfs
```

   The script also writes a new cloud-init instance ID to `meta-data`. cloud-init runs the users, keys and `runcmd` steps once per instance ID, so a card that booted before (for example from the Imager) would skip them otherwise.
3. Put the card in the Pi 3 and power it on (micro-USB, 5 V, 2.5 A). Wait about 3 minutes. Use `ssh root@pi3-sink.local`.
4. Install the sink over SSH. The certificate path is optional but needed for the iPod to accept the stereo:

```bash
ACCESSORY_CERT_FILE=sessions/20261008-221642-car-10/decoded/accessory-cert-1.p7b sink/deploy.sh
```

`deploy.sh` runs `build.sh` first (format check, `go vet`, tests with the race detector, then the arm64 build in Docker). Run it again after any change.

Kernel options that `prepare-sdcard.sh` adds to `cmdline.txt`:

| Option | Why |
|---|---|
| `dwc_otg.speed=1` | The Pi 3 host runs at full speed, like the car stereo. This tests relay patches 04 to 07. `sink-speed high` and a reboot give high speed. |
| `usbhid.quirks=0x05ac:0x12a8:0x20000000,0x05ac:0x12ab:0x20000000` | `HID_QUIRK_NO_INIT_REPORTS`. The stereo sent no `GET_REPORT` after `SET_CONFIGURATION`. |
| `cfg80211.ieee80211_regdom=NZ` | Wi-Fi country. |

`/etc/modprobe.d/iap-sink.conf` blocks `apple_mfi_fastcharge` and `ipheth`, so only `iap-sink` talks to the Apple device.

## 4. How the connection works

A Linux host would send `SET_CONFIGURATION 0` or `1` as soon as the device appears. The stereo sent only configuration 2, and the relay cannot take anything else (risk R17 in `PLAN.md`). So `iap-sink`:

1. Claims every USB-A port of the onboard hub (`USBDEVFS_CLAIM_PORT`). The kernel then enumerates the device but does not configure it. `IAP_CLAIM_PORTS=0` switches this off.
2. Releases the port of the Apple device. A claimed port also stops `usbhid` and `snd-usb-audio` from binding.
3. Sends configuration 2 through sysfs. The kernel binds `usbhid` (`/dev/hidrawN`) and `snd-usb-audio` (an ALSA card named after the device).
4. Claims the port again when the session ends, before the next device appears.

The relay needs patch 09 (`pi/patches/`): the kernel still sends `SET_CONFIGURATION 0` once when it enumerates a claimed device.

## 5. A test session

1. Pi 3 on its own power, `iap-sink` and `sink-audio` running (`systemctl status iap-sink sink-audio`). A speaker or headphones on the 3.5 mm jack.
2. Pi 5 relay on bench power with an Apple device on its black USB-A port. Run `capture start sink-NN` on the Pi 5.
3. When the Pi 5 says "Connect the Pi's USB-C cable to the stereo now", plug the **taped** cable into a Pi 3 USB-A port.
4. On the Pi 3 (the first lines appear within 5 s of the cable):

```bash
journalctl -u iap-sink -f          # packets and state
iap-sink ctl status                # JSON: play state, track, title, counters
iap-sink ctl toggle                # play button; also: play pause stop next prev ffwd rew endff track <n>
                                   # next and prev send PlayControl 0x08 and 0x09. "track <n>" sends SetCurrentPlayingTrack, as the stereo did
iap-sink ctl raw 0004001c          # any packet: hex of lingo, command and arguments
```

5. **Restart the relay before each new sink session.** If you restart only the Pi 3 sink, the Apple device is still in the old session and ignores the new `IdentifyDeviceLingoes` (the relay's `PROXY_RESET=1` exists for this). Run `capture stop` and `capture start` on the Pi 5, or replug.
6. Compare with the car: `/var/lib/iap-sink/traces/session-*.jsonl` holds every iAP packet (`A>D` is sent by the Pi 3, `D>A` comes from the Apple device) and, with `IAP_TRACE_HID=1` (default), every raw HID report, with hex.

Never use a USB-C to USB-C cable between the two Pis. Keep VBUS taped, as in the main README.

## 6. Results of the first bench run (09/10/2026, iPhone 15 Pro, iOS 27.0.1)

| Check | Result |
|---|---|
| Pi 5 gadget speed behind the Pi 3 | `full-speed`, as with the car |
| Enumeration, configuration 2, HID descriptor (96 bytes) | Pass, after relay patch 09 |
| iAP1: identify, certificate, sample rates, init script, polling, track metadata, play | Pass. The iPhone accepted the certificate (`AckDevAuthenticationInfo` status 0) |
| USB audio to the 3.5 mm jack | Pass: the ALSA card `iPhone` appears, `alsaloop` plays, audio is clean by ear |
| Control test (session `sink-05`): pause, play, next, prev, toggle ×2, next, prev | **Pass.** All 15 `PlayControl` packets were acked `success` by the iPhone 2 to 3 ms after the sink sent them. Pause and play changed the state at once. Next gave a new song. Previous restarted the song after 3 s, as an iPod does. All 16,877 transfers in the capture are explained. Audio: 64,772 packets, 0 error packets, 1 isochronous timing event at the start. The relay forwarded `SET_CONFIGURATION 2` only (twice per connection, as in `car-10`), and acked configuration 0 without forwarding it. |
| iAP1 after 2.5 minutes | **The iPhone stops answering.** It sent two signature challenges 75 s apart, got no signature, then sent `AckDevAuthenticationStatus` with status `0x07` (failed) and **no packet after that**. Audio keeps playing. The car stereo answered the first challenge after 56 s. So one connection gives about 2.5 minutes of iAP control without the key. |

Bugs found and fixed on the way: the sink crashed in upstream `parsePacket` on a frame with a lost first fragment (now a bounds-checked splitter and a frame reader that drops orphan fragments and logs them), and the relay did not ack configuration 0 (patch 09).

## 7. Settings

`/etc/iap-sink/sink.conf` (read by both services; restart them after a change):

| Variable | Default | Use |
|---|---|---|
| `IAP_AUTOPLAY` | `1` | Press play at the start when the iPod is not playing |
| `IAP_SIGNATURE` | `none` | `bogus` sends an invalid signature after 56 s |
| `IAP_DEBUG` | off | Log the polled packets too |
| `IAP_CLAIM_PORTS` | `1` | Claim the hub ports so the first `SET_CONFIGURATION` is configuration 2 (section 4) |
| `IAP_TRACE_HID` | `1` | Write every raw HID report to the trace file |
| `SINK_AUDIO_OUT` | `Headphones` | Playback card of the 3.5 mm jack (`aplay -l`) |
| `SINK_AUDIO_RATE` | `44100` | Sample rate for `alsaloop` |
| `SINK_AUDIO_LATENCY_US` | `300000` | `alsaloop` latency |
| `SINK_ALSALOOP_ARGS` | empty | Extra `alsaloop` options, for example `-S 3` |

## 8. Files

| Path | Content |
|---|---|
| `iap-sink/` | Go program (module `iap-sink`) and its tests |
| `files/` | Files that go to the Pi: services, `audio-route`, `sink-speed`, `sink.conf`, modprobe file |
| `build.sh` | Tests and builds in Docker. Output `out/iap-sink` |
| `prepare-sdcard.sh`, `deploy.sh`, `install-on-pi.sh` | Card set-up, install over SSH, install steps on the Pi |
| `out/` | Build output. Ignored by git |

Tests with a private capture (the files stay on the Mac):

```bash
IAP_CAPTURE_TIMELINE=sessions/<dir>/decoded/timeline.jsonl IAP_REAL_CERT=sessions/<dir>/decoded/accessory-cert-1.p7b sink/build.sh
```

The certificate and the capture are private. Keep them out of git, like `sessions/`.

## 9. Open points

- The iPhone reports a track index that rises by itself while one song plays, and `GetNumPlayingTracks` stays below it (index 22 of 4). The reports come from the iPhone, not from the sink. The cause is not known; an Apple Music queue is one candidate. Do not compute "next" from the index: use `next`.
- A lost first HID fragment happened once (before the frame reader fix). The cause is not known: the relay's re-packetising (patch 07) or the Pi 3 host. The raw HID trace shows it if it happens again; the sink now logs each dropped fragment with its bytes.
- The relay still crashes when a host switches configuration from 1 to 2 (R17, not fixed). This does not happen in the car.
- Long-term audio drift of `alsaloop` (two clocks) is not measured. If it glitches, try `SINK_ALSALOOP_ARGS="-S 3"` or a larger latency.
