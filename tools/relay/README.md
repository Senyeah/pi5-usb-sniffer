# USB relay and sniffer (`tools/relay`)

The Pi 5 first ran as a USB relay and sniffer. It sat between an Apple device and the car stereo, forwarded all USB traffic in both directions and recorded it. The recordings, mainly session `car-10`, showed what the stereo sends and what an iPad answers. The Bluetooth bridge in the repository root copies that iPad. See [../../CLAUDE.md](../../CLAUDE.md) for the bridge and [../../PLAN.md](../../PLAN.md) for the history (phases 0–6) and the relay risks (R1–R17).

The relay is still installed on the Pi 5. `ipod-mode relay` switches the USB-C port back to it, and `ipod-mode bt` returns to the bridge. This folder was `pi/` before 09/10/2026. The paths **on the Pi** did not change: the files are in `/opt/sniffer/pi/`, the command is `capture`, and the host name is still `pi5-sniffer`.

## 1. What the relay is

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

## 3. Pi software

### 3.1 How the Pi was built

1. Flash Raspberry Pi OS Lite 64-bit (Trixie, with cloud-init) to an SD card.
2. On the Mac, run `tools/relay/prepare-sdcard.sh /Volumes/bootfs` with `WIFI_SSID`, `WIFI_PSK_REF` and `SSH_KEY_REF` set. It reads the SSH key and the Wi-Fi password with `op read` (1Password CLI). It writes cloud-init `user-data` and `network-config`, adds lines to `config.txt` and `cmdline.txt`, and copies `setup.sh`, `capture.sh` and `sniffer-setup.service` to `bootfs/sniffer/`.
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
| `usb-proxy` | `/opt/sniffer/usb-proxy`, `AristoChen/usb-proxy` at `a08301d` | The relay, with the patches in section 3.2. |

**Last known Pi state that a fresh card would not have.** The Pi was offline at the end of the session, so check these:

- `config.txt` has the GPU driver line commented out (`#dtoverlay=vc4-kms-v3d`, with a "LOW-POWER TEST" comment). This was left from a power test. The clock is back to normal, and all 4 cores are on (`maxcpus=1` was removed). **Keep 4 cores:** one core caused audio drops.
- Backups exist next to the boot files: `*.before-lowpower` and `cmdline.txt.before-4cores`.
- Some debug tools exist only on the Pi, in `/opt/sniffer/tools/`. They are not in this repo:
  - `otherspeed.c` fetches other-speed descriptors.
  - `hiddesc.c` dumps the HID class and report descriptors.
  - `probe.c` sends 1-byte `SET_REPORT` tests.
  - `setcfgtime.c` times `set_configuration` and interface claims.
- 28 sessions (about 251 MB) are in `/root/sessions/`.

### 3.2 The usb-proxy patches

Upstream `usb-proxy` could not relay this device to this stereo. Each patch fixes one observed failure. They apply in name order on commit `a08301d` and build with no warnings.

| Patch | Problem it fixes |
|---|---|
| `01-detach-before-claim` | The kernel binds `usbhid` and `snd-usb-audio` after `SET_CONFIGURATION`. The patch detaches kernel drivers before each interface claim. |
| `02-rules-wildcard-block` | Adds `-1` wildcards to the injection rules. Ignore and stall rules are checked **before** a request goes to the device; upstream forwarded IN requests first. An ignored OUT request is acked to the host and not forwarded. Prints each matched rule. |
| `03-suspend-retry` | When the host suspends the bus, an endpoint write returns `EAGAIN` and upstream calls `exit()`. The patch waits and retries. Both the Mac and the stereo suspend the bus. |
| `04-full-speed-host` (R11) | The stereo is full speed only, but the device is high speed. At start the relay fetches the other-speed descriptors (`GET_DESCRIPTOR` type 7). If `/sys/class/udc/*/current_speed` is `full-speed`, it serves those in place of the configuration descriptors. It also enables the gadget endpoints with the full-speed packet size and interval. |
| `05-learned-stalls` (R12) | The gadget framework acks an OUT control request with data before the relay can read it, so a device STALL cannot reach the host. The patch remembers each setup packet that the device stalled. It stalls the host at SETUP the next time that request comes. |
| `06-ack-before-device-work` (R13) | A standard request with no data must complete within 50 ms. `SET_CONFIGURATION` on the real device takes about 54 ms. The patch enables the gadget endpoints and acks first, then configures the device and starts the endpoint threads. `SET_INTERFACE` uses the same order. |
| `07-apple-hid-fs-tables` (R14) | The Apple device has a different HID report table at each speed. At full speed the relay serves the 96-byte full-speed HID report descriptor. It re-packetises iAP traffic between the two tables in both directions (../../CLAUDE.md, section 9.2). It also drops zero-length interrupt reads. It works only when the device's HID report descriptor is 208 bytes at high speed and 96 at full speed. |
| `08-fs-in-max-report` (R15 test) | Adds option `apple_fs_in_max_count` in `config.json` (default 63). A test with 20 did not change anything, and the option was removed from `config.json`. The patch is still applied, with no effect. |
| `09-ack-config-zero` (R17) | A Linux host sends `SET_CONFIGURATION 0`. Upstream skipped it without an ACK, and the host timed out after 5 s. The patch acks configuration 0 and stalls other invalid values. The car stereo never sends it. |

### 3.3 Rules and options

`tools/relay/rules/apple-vendor-rules.json`, loaded by default:

| Rule | Request | Why |
|---|---|---|
| ignore | `bmRequestType 0x40, bRequest 0x40`, any value and index, no data | The Apple charge request. macOS asks for 2400 mA, and the iPhone then trips the Pi's 1.6 A USB-A limit (R4). The relay acks it and does not forward it. |
| stall | any type, `bRequest 0x52`, `wLength 1` | Apple "set USB mode". The iPhone re-enumerates with 6 configurations, and the relay loops. |
| stall | `0x21/0x09` (HID `SET_REPORT`), `wIndex 2`, `wLength 1` | 1-byte `SET_REPORT` probes (report ID only). The stereo sent these in a loop when it got a truncated HID descriptor (R14). |

**Quirk: numbers in this file are hex digits written as JSON integers.** `40` means 0x40, and `52` means 0x52. `usb-proxy` converts them when it matches. The rule list that `usb-proxy` prints at start shows the unconverted value, for example `bRequestType=0x28` for `40`. That is the display only; matching uses 0x40.

`tools/relay/rules/config.json` holds `{"reset_device_before_proxy": false}`. `capture` does not use this file directly. It writes `/run/sniffer/config.json` with the reset flag from `PROXY_RESET`, and it always passes `--enable_customized_config`. `usb-proxy` reads `config.json` only from its working directory (`/run/sniffer`), and only with that flag.

### 3.4 The `capture` command

`/usr/local/bin/capture` is `tools/relay/capture.sh`. Run it as root.

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

### 3.5 Deploying a change to the Pi

Stop a running session first with `capture stop`. Then copy the files and rebuild. The rebuild steps are the same as in `setup.sh`:

```bash
rsync -a --exclude .DS_Store tools/relay/ root@pi5-sniffer.local:/opt/sniffer/pi/
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
rsync -ani --exclude .DS_Store tools/relay/ root@pi5-sniffer.local:/opt/sniffer/pi/
```


## 4. Running sessions

### 4.1 A car session

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

### 4.2 A Mac test session

```bash
PROXY_RESET=0 PROXY_ARGS=--auto_remap_endpoints capture start mac-NN
```

The Mac selects configuration 6, which needs all 14 dwc2 endpoints. Image Capture lists photos, and iPhone USB networking works. Finder does not show the device, and the device asks for Trust more than once.

### 4.3 Troubleshooting

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

## 5. Software sources and build notes

### 5.1 On the Pi

| Part | Source | Purpose |
|---|---|---|
| Raspberry Pi OS Lite, 64-bit, current release | raspberrypi.com | Base OS. Kernel 6.12 or later. |
| EEPROM update | `sudo rpi-eeprom-update -a` | Current firmware. Early firmware broke dwc2. |
| `dtoverlay=dwc2,dr_mode=peripheral` | `/boot/firmware/config.txt`, under `[all]` | Puts the USB-C port in device mode. The UDC name is `1000480000.usb`. |
| `usb_max_current_enable=1` | `/boot/firmware/config.txt`, under `[all]` | USB-A current limit for GPIO power. |
| `raw_gadget` kernel module | github.com/xairy/raw-gadget, pinned to `8c6de54` | Lets a user-space program act as any USB device. Known to work on Pi 5. Not in the Pi kernel, so DKMS builds it. |
| `usb-proxy` | github.com/AristoChen/usb-proxy, pinned to `a08301d` | The relay. C++, libusb + raw_gadget. Isochronous support was added in 2026. Local patches in `tools/relay/patches/`: detach kernel drivers before each claim; rule wildcards (`-1`), block ignore/stall rules before forwarding, ack ignored OUT requests. |
| `usbmon` + `dumpcap` | kernel module + `wireshark-common` | Records host-side traffic as pcapng. |

Build notes:

- `raw_gadget`: install the kernel headers (`linux-headers-rpi-2712`, or the package for `uname -r`). Then run `make` and `./insmod.sh` in `raw_gadget/`. Kernel 5.19 and later need no patch.
- `usb-proxy`: install `libusb-1.0-0-dev`, `libjsoncpp-dev` and `pkg-config`, then run `make`. Record the git commit hash that you build.
- Run it with `--device=1000480000.usb --driver=1000480000.usb --vendor_id=05ac --product_id=<iPhone PID>`. Check `--help` for the verbose log option.
- Stop other software from taking the iPhone on the host side:
  - `sudo systemctl mask usbmuxd`
  - Blacklist the `ipheth` module.
  - `usb-proxy` must detach any kernel drivers that bind to the iPhone's interfaces (`usbhid`, `snd-usb-audio`). Check this in Phase 2.

### 5.2 On the Mac

- The decoder reads pcapng files itself, so it does not need `tshark`. Wireshark is optional, for manual inspection.
- `uv` runs the decoder. It is a uv project in `tools/decode/` with no runtime dependencies.

## 6. Mac host behaviour

- macOS sends the Apple charge request `0x40/0x40` with `wValue 0x0960` (2400 mA) and `wIndex 0x076c` (1900 mA). Within about 6 ms the iPhone exceeded the Pi's limit, and all USB-A ports reported over-current.
- macOS sends "set USB mode 4" (`0xC0/0x52`) and selects configuration 6.
- macOS suspends the bus after about 2 minutes idle.

## 7. Quirks: relay and USB

- The gadget framework acks OUT control data before user space reads it, so the relay cannot pass a device STALL for such a request (R12, patch 05).
- dwc2 gives isochronous timing errors (`ENODATA`, errno 61) on some audio packets. One core made this much worse. These errors happen on the stereo side, so `usbmon` cannot see them; only `proxy.log` shows them.
- `usb-proxy` hangs when the device re-enumerates, and it kills its own process group (see 5.4).
- systemd needs `WorkingDirectory` to exist before a unit starts; `capture` creates `/run/sniffer` first.

## 8. Open work

1. **Copy `car-15`.** When the Pi is on again, copy the pcapng, then decode it:

```bash
rsync -a root@pi5-sniffer.local:/root/sessions/20261008-223159-car-15-4cores sessions/
```

2. **Check the Pi's health** after the hard power cut (../../CLAUDE.md, section 10).
3. **Golden captures:** run the Phase 5 action list in the car (../../PLAN.md, Phase 5 step 6), with a `capture mark` before each action. Then add golden-file tests: a short trimmed capture and its expected timeline.
4. **iPhone in the car:** re-test the iPhone through the final relay. Only the iPad has worked there so far.
5. **Investigate** the connection where the stereo never sent its certificate, and the 56 s signature delay. Is the delay normal for this stereo with a direct connection? A direct capture needs a passive analyser; see ../../PLAN.md section 10.
6. **Optional:** usbmux pairing through the relay (not needed for the stereo). An ADuM4160 USB isolator could replace the full-speed translation: it would make the device enumerate at full speed on the Pi.
7. **R17:** the relay still crashes when a host switches from configuration 1 to 2 (the Pi 3 test stereo avoids it; the car never does it).
