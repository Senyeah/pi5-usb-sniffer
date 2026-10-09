// iap-sink emulates the car stereo (the iAP1 accessory side) on a Raspberry Pi 3 USB host.
// The Pi 5 relay plugs into this host and the Pi 3 plays the stereo's part of car-10.
package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/oandrew/ipod"
)

type config struct {
	VID       string
	Config    int
	CertFile  string
	Signature string
	Autoplay  bool
	TraceDir  string
	Socket    string
	SysUSB    string
	DevRoot   string
	Debug     bool
	Claim     bool
	TraceHID  bool
}

func envStr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}

func envBool(k string, def bool) bool {
	switch strings.ToLower(os.Getenv(k)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envInt(k string, def int) int {
	if n, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return n
	}
	return def
}

func loadConfig() config {
	return config{
		VID:       envStr("IAP_VID", "05ac"),
		Config:    envInt("IAP_CONFIG", 2),
		CertFile:  envStr("IAP_CERT", "/etc/iap-sink/accessory-cert.p7b"),
		Signature: envStr("IAP_SIGNATURE", "none"),
		Autoplay:  envBool("IAP_AUTOPLAY", true),
		TraceDir:  envStr("IAP_TRACE_DIR", "/var/lib/iap-sink/traces"),
		Socket:    envStr("IAP_SOCKET", "/run/iap-sink.sock"),
		SysUSB:    envStr("IAP_SYS_USB", "/sys/bus/usb/devices"),
		DevRoot:   envStr("IAP_DEV", "/dev"),
		Debug:     envBool("IAP_DEBUG", false),
		Claim:     envBool("IAP_CLAIM_PORTS", true),
		TraceHID:  envBool("IAP_TRACE_HID", true),
	}
}

type server struct {
	cur    atomic.Pointer[stereo]
	claims *portClaimer // nil when IAP_CLAIM_PORTS=0
}

func main() {
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "run":
		cfg := loadConfig()
		lvl := slog.LevelInfo
		if cfg.Debug {
			lvl = slog.LevelDebug
		}
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))
		ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
		defer cancel()
		run(ctx, cfg)
	case "ctl":
		os.Exit(ctl(loadConfig().Socket, strings.Join(os.Args[2:], " ")))
	default:
		fmt.Fprintln(os.Stderr, "usage: iap-sink [run] | iap-sink ctl <command>   (iap-sink ctl help)")
		os.Exit(2)
	}
}

func run(ctx context.Context, cfg config) {
	srv := &server{}
	if cfg.Claim {
		srv.claims = newPortClaimer(cfg.SysUSB, cfg.DevRoot, slog.Default())
	}
	if err := srv.listen(ctx, cfg.Socket); err != nil {
		slog.Error("control socket", "err", err)
	}
	for ctx.Err() == nil {
		if err := srv.session(ctx, cfg); err != nil && ctx.Err() == nil {
			slog.Warn("session ended", "err", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
}

func (srv *server) waitDevice(ctx context.Context, cfg config) *usbDev {
	logged := false
	for ctx.Err() == nil {
		if srv.claims != nil {
			srv.claims.sync()
		}
		if d := findDevice(cfg.SysUSB, cfg.VID); d != nil {
			return d
		}
		if !logged {
			slog.Info("waiting for an Apple device on USB", "vendor", cfg.VID)
			logged = true
		}
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
	return nil
}

// waitGone returns when the device is unplugged or enumerated again.
func waitGone(ctx context.Context, d *usbDev) {
	num := readSys(d.Path + "/devnum")
	for ctx.Err() == nil {
		if cur := readSys(d.Path + "/devnum"); cur == "" || cur != num {
			return
		}
		select {
		case <-ctx.Done():
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (srv *server) session(ctx context.Context, cfg config) error {
	dev := srv.waitDevice(ctx, cfg)
	if dev == nil {
		return nil
	}
	now := readSys(dev.Path + "/bConfigurationValue")
	slog.Info("Apple device found", "path", dev.Name, "id", dev.VID+":"+dev.PID, "config_now", now)
	if cfg.Claim && now != "" && now != "0" && now != strconv.Itoa(cfg.Config) {
		// The kernel configured it before the port was claimed. A switch would differ from the stereo, which sends only configuration 2.
		slog.Warn("the kernel already configured this device: unplug and plug in the USB cable again", "configuration", now)
		waitGone(ctx, dev)
		return nil
	}
	if srv.claims != nil {
		srv.claims.release(dev.Name)
		defer srv.claims.reclaim(dev.Name)
	}
	if err := selectConfig(dev, cfg.Config); err != nil {
		return fmt.Errorf("select configuration %d: %w", cfg.Config, err)
	}
	var node string
	var err error
	for i := 0; i < 50; i++ {
		if node, err = findHIDRaw(dev, cfg.Config, cfg.DevRoot); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	if srv.claims != nil {
		// Claim the port again as soon as the drivers are bound. A device that appears at once after this one
		// (the Pi 5 restarts its gadget) must not be configured by the kernel. A claim does not touch this device.
		if !waitBound(dev, cfg.Config, 5*time.Second) {
			slog.Warn("not all interfaces have a driver yet", "path", dev.Name)
		}
		srv.claims.reclaim(dev.Name)
	}
	f, err := os.OpenFile(node, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	desc, err := readReportDescriptor(f)
	if err != nil {
		return err
	}
	defs, err := parseReportDescriptor(desc)
	if err != nil {
		return fmt.Errorf("report descriptor (%d bytes): %w", len(desc), err)
	}
	speed := readSys(dev.Path + "/speed")
	slog.Info("HID ready", "node", node, "descriptor_bytes", len(desc), "usb_speed_mbps", speed, "reports", describeDefs(defs))

	o := defaultOptions()
	o.Signature = cfg.Signature
	o.Autoplay = cfg.Autoplay
	o.Log = slog.Default()
	if cert, err := os.ReadFile(cfg.CertFile); err == nil {
		o.Cert = cert
		slog.Info("certificate loaded", "bytes", len(cert))
	} else {
		slog.Warn("no certificate", "file", cfg.CertFile, "err", err)
	}
	if cfg.TraceDir != "" {
		if err := os.MkdirAll(cfg.TraceDir, 0o750); err == nil {
			p := filepath.Join(cfg.TraceDir, time.Now().UTC().Format("session-20060102-150405")+".jsonl")
			if t, err := newTracer(p); err == nil {
				o.Trace = t
				defer t.close()
				slog.Info("trace file", "path", p)
			}
		}
	}

	var stream io.ReadWriter = f
	if o.Trace != nil && cfg.TraceHID {
		stream = &tracedStream{rw: f, t: o.Trace}
	}
	fr, fw := newLink(stream, defs, o.Log.Warn)
	stopCh := make(chan error, 1)
	stop := func(err error) {
		select {
		case stopCh <- err:
		default:
		}
	}
	st := newStereo(o, fw, stop)
	st.meta = map[string]string{
		"usb_path": dev.Name, "usb_id": dev.VID + ":" + dev.PID, "usb_speed_mbps": speed,
		"configuration": strconv.Itoa(cfg.Config), "hidraw": node, "reports": describeDefs(defs),
	}
	go st.loop()
	go readLoop(st, fr, stop)
	st.start()
	srv.cur.Store(st)
	defer func() {
		srv.cur.Store(nil)
		close(st.done)
	}()

	select {
	case err := <-stopCh:
		return err
	case <-ctx.Done():
		return nil
	}
}

func readLoop(st *stereo, fr ipod.FrameReader, stop func(error)) {
	fails := 0
	for {
		frame, err := fr.ReadFrame()
		if err != nil {
			fails++
			fatal := errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.EIO) ||
				errors.Is(err, syscall.ESHUTDOWN) || errors.Is(err, os.ErrClosed) || errors.Is(err, io.EOF)
			if fatal || fails >= 3 {
				stop(fmt.Errorf("read: %w", err))
				return
			}
			st.o.Log.Warn("report read error", "err", err)
			continue
		}
		fails = 0
		pkts, err := splitPackets(frame)
		if err != nil {
			st.o.Log.Warn("bad packet in frame", "err", err, "frame", hex.EncodeToString(frame))
		}
		for _, p := range pkts {
			if r, ok := parsePayload(append([]byte(nil), p...)); ok {
				st.post(func() { st.onRx(r) })
			}
		}
	}
}

func (srv *server) listen(ctx context.Context, path string) error {
	os.Remove(path)
	l, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	os.Chmod(path, 0o660)
	go func() { <-ctx.Done(); l.Close(); os.Remove(path) }()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go srv.handle(c)
		}
	}()
	return nil
}

func (srv *server) handle(c net.Conn) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	line, _ := bufio.NewReader(c).ReadString('\n')
	st := srv.cur.Load()
	if st == nil {
		fmt.Fprintln(c, "no session: no Apple device is connected")
		return
	}
	line = strings.TrimSpace(line)
	fmt.Fprintln(c, st.call(func() string { return st.control(line) }))
}

func ctl(sock, line string) int {
	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot reach iap-sink:", err)
		return 1
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(15 * time.Second))
	if strings.TrimSpace(line) == "" {
		line = "status"
	}
	fmt.Fprintln(c, line)
	io.Copy(os.Stdout, c)
	return 0
}
