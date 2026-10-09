# pi-iap-bt-bridge

A Raspberry Pi 5 that lets an old car stereo that knows only iPods play a phone over Bluetooth.

To the stereo, the Pi is an iPod on USB. It synthesizes the iAP1 session (Apple's legacy iPod Accessory Protocol) and the USB audio from the phone's Bluetooth stream. The stereo shows the track data from the phone, and its buttons control the phone.

```
phone ──Bluetooth (A2DP audio, AVRCP data and controls)──► Pi 5 ──USB (iAP1 over HID, USB audio)──► car stereo
```

Built for and tested with a Panasonic CQ-JZ41F0AE (Suzuki PA68L0, Suzuki Swift of about 2010): an iPad in the car, an iPhone on the bench. Other stereos that speak iAP1 over USB may work. They are not tested.

## Status

| Feature | State (09/10/2026) |
|---|---|
| Audio, track data, play, pause, next, back, shuffle, repeat | Work in the car |
| No phone connected | The stereo shows a "Waiting" track, not an error |
| Pairing and reconnecting | Automatic: the Pi connects to a paired phone by itself |
| Lip sync for video | The Pi reports its delay to the phone; the delay is not measured yet |
| Loud songs | Some clipping remains at the default -6 dB |

Open work and the full history are in [PLAN.md](PLAN.md).

## What you need

- A Raspberry Pi 5 with Raspberry Pi OS Lite 64-bit (Trixie). The USB-C port becomes the USB device, so the Pi gets its power from the GPIO header (a 5.1 V supply).
- A USB-C plug to USB-A plug cable, with **VBUS (pin 1 of the USB-A plug) taped**. Never use a USB-C to USB-C cable.
- A Mac or a Linux computer with Docker, `ssh`, `rsync`, and the 1Password CLI (`op`) for the card set-up.
- Optional: a Raspberry Pi 3 B as a test stereo on the bench ([tools/test-stereo/](tools/test-stereo/)).

## Set-up

1. Flash Raspberry Pi OS Lite 64-bit to an SD card. Prepare it on the computer. This sets Wi-Fi, users and USB gadget mode. It also installs the relay tools, which the bridge does not need:

   ```bash
   WIFI_SSID=... WIFI_PSK_REF=op://<vault>/<item>/<field> SSH_KEY_REF=op://<vault>/<item>/<field> tools/relay/prepare-sdcard.sh /Volumes/bootfs
   ```

2. Boot the Pi and wait for its first-boot set-up to finish. Then build and install the bridge:

   ```bash
   ./deploy.sh pi5-sniffer.local
   ```

3. Turn the bridge on. It then starts at every boot:

   ```bash
   ssh root@pi5-sniffer.local ipod-mode bt
   ```

4. Pair the phone: Settings, Bluetooth, **Pi iPod**. The Pi opens a 10 minute pairing window by itself: at start when no phone is paired, or 60 s after start when no phone is connected.
5. Connect the taped cable to the stereo and select its USB source.

Settings are in `/etc/ipod-bridge.conf` on the Pi, for example `IPOD_AUDIO_GAIN_DB` for the level and `IPOD_BT_NAME` for the Bluetooth name. `ipod-bridge ctl help` lists the commands.

## Safety

- Keep VBUS taped on the cable to the stereo. Two 5 V sources would fight.
- Hold 5.0 to 5.25 V at the GPIO header under load. Do not connect a USB-C supply at the same time.
- Shut the Pi down (`poweroff`) before you cut its power.
- In a garage: key in ACC, engine off.

## Repository

| Path | Content |
|---|---|
| `bridge/` | `ipod-bridge` (Go): the iPod side of iAP1, the Bluetooth phone, pairing, delay reports |
| `files/`, `uac1-fs/` | Files for the Pi 5, and a kernel patch for USB audio at full speed |
| `deploy.sh`, `build.sh` | Build (Docker) and install on the Pi |
| `latency/`, `latency-test.sh`, `audio-test.sh` | Bench measurements |
| `tools/relay/` | The USB relay and sniffer that recorded the stereo |
| `tools/decode/` | Decoder for the relay's captures |
| `tools/test-stereo/` | Pi 3 B that plays the car stereo on the bench |

## How it came about

The project started as a USB relay and sniffer. The Pi 5 sat between an iPad and the stereo, and recorded a complete session. A decoder explained every transfer in it. The bridge now gives the same answers as that iPad, with the data from a Bluetooth phone. Each phase, with its result, is in [PLAN.md](PLAN.md).

## Documentation

- [CLAUDE.md](CLAUDE.md): the full technical notes: how it works, settings, tests, protocol facts, quirks. They are written as handover notes for an AI agent, and serve people too.
- [PLAN.md](PLAN.md): goal, status, open work, decisions, history, risks.
- [tools/relay/](tools/relay/README.md), [tools/decode/](tools/decode/README.md), [tools/test-stereo/](tools/test-stereo/README.md): the tools.

Captures and traces hold the stereo's certificate and personal data. They stay out of git (`sessions/`, `out/`).

## Built on

[oandrew/ipod](https://github.com/oandrew/ipod) (iAP1 framing and HID reports), [BlueZ](https://www.bluez.org/), [BlueALSA](https://github.com/arkq/bluez-alsa), and, for the relay, [xairy/raw-gadget](https://github.com/xairy/raw-gadget) and [AristoChen/usb-proxy](https://github.com/AristoChen/usb-proxy).
