// ipod-bridge is the iPod for the car stereo. The Pi 5 is a Bluetooth audio sink for the phone. This program
// speaks iAP1 over the USB gadget (/dev/hidg0) and takes the track data and the controls from the phone
// through AVRCP (BlueZ).
package main

import (
	"bufio"
	"context"
	"encoding/hex"
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
	Hidg        string
	Gadget      string
	Name        string
	BTName      string
	TraceDir    string
	Socket      string
	Debug       bool
	TraceHID    bool
	AuthTimeout time.Duration
	Reconnect   time.Duration // 0: do not connect to the phone by itself
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

func loadConfig() config {
	to := 70 * time.Second
	if d, err := time.ParseDuration(os.Getenv("IPOD_AUTH_TIMEOUT")); err == nil {
		to = d
	}
	re := 10 * time.Second
	if v, ok := os.LookupEnv("IPOD_RECONNECT_EVERY"); ok {
		if d, err := time.ParseDuration(v); err == nil {
			re = d
		}
	}
	return config{
		Reconnect:   re,
		Hidg:        envStr("IPOD_HIDG", "/dev/hidg0"),
		Gadget:      envStr("IPOD_GADGET", "/sys/kernel/config/usb_gadget/ipod"),
		Name:        envStr("IPOD_NAME", ""),
		BTName:      envStr("IPOD_BT_NAME", "Pi iPod"),
		TraceDir:    envStr("IPOD_TRACE_DIR", "/var/lib/ipod-bridge/traces"),
		Socket:      envStr("IPOD_SOCKET", "/run/ipod-bridge.sock"),
		Debug:       envBool("IPOD_DEBUG", false),
		TraceHID:    envBool("IPOD_TRACE_HID", true),
		AuthTimeout: to,
	}
}

type server struct {
	cfg config
	ph  *bluezPhone
	cur atomic.Pointer[pod]
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
		if err := run(ctx, cfg); err != nil {
			slog.Error("ipod-bridge", "err", err)
			os.Exit(1)
		}
	case "ctl":
		os.Exit(ctl(loadConfig().Socket, strings.Join(os.Args[2:], " ")))
	default:
		fmt.Fprintln(os.Stderr, "usage: ipod-bridge [run] | ipod-bridge ctl <command>   (ipod-bridge ctl help)")
		os.Exit(2)
	}
}

func run(ctx context.Context, cfg config) error {
	ph, err := newBluezPhone(slog.Default())
	if err != nil {
		return fmt.Errorf("system D-Bus: %w", err)
	}
	go func() {
		if err := ph.Run(); err != nil {
			slog.Error("BlueZ", "err", err)
		}
	}()
	select {
	case <-ph.ready:
	case <-time.After(15 * time.Second):
		return fmt.Errorf("BlueZ did not answer: is bluetooth.service running?")
	}
	// BlueZ may still start up (after a restart of bluetooth.service), so try a few times.
	if err := retry("adapter setup", func() error { return ph.SetupAdapter(cfg.BTName) }); err != nil {
		return err
	}
	if err := ph.RegisterAgent(); err != nil {
		return err
	}
	if !ph.HasPairedDevice() {
		slog.Info("no phone is paired: pairing window open", "seconds", pairingSecs, "bluetooth_name", cfg.BTName)
		if err := retry("pairing window", func() error { return ph.OpenPairing(pairingSecs) }); err != nil {
			return err
		}
	}

	if cfg.Reconnect > 0 {
		go ph.Reconnect(ctx, cfg.Reconnect)
	}

	srv := &server{cfg: cfg, ph: ph}
	if err := srv.listen(ctx); err != nil {
		slog.Error("control socket", "err", err)
	}
	for ctx.Err() == nil {
		if !srv.waitConfigured(ctx) {
			break
		}
		if err := srv.session(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("session ended", "err", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
		}
	}
	return nil
}

func retry(what string, f func() error) error {
	var err error
	for i := 0; i < 10; i++ {
		if err = f(); err == nil {
			return nil
		}
		slog.Warn("BlueZ is not ready, trying again", "step", what, "err", err)
		time.Sleep(time.Second)
	}
	return fmt.Errorf("%s: %w", what, err)
}

func readSys(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(string(b))
}

func (srv *server) udcState() string {
	udc := readSys(srv.cfg.Gadget + "/UDC")
	if udc == "" {
		return "no gadget"
	}
	return readSys("/sys/class/udc/" + udc + "/state")
}

func (srv *server) waitConfigured(ctx context.Context) bool {
	logged := ""
	for ctx.Err() == nil {
		st := srv.udcState()
		if st == "configured" {
			return true
		}
		if st != logged {
			slog.Info("waiting for the stereo", "usb_state", st)
			logged = st
		}
		select {
		case <-ctx.Done():
		case <-time.After(500 * time.Millisecond):
		}
	}
	return false
}

// hidgStream is /dev/hidg0. A write waits for the host to read the report, so it has a deadline.
type hidgStream struct{ f *os.File }

func (s *hidgStream) Read(p []byte) (int, error) { return s.f.Read(p) }

func (s *hidgStream) Write(p []byte) (int, error) {
	s.f.SetWriteDeadline(time.Now().Add(2 * time.Second))
	return s.f.Write(p)
}

func (srv *server) session(ctx context.Context) error {
	cfg := srv.cfg
	f, err := os.OpenFile(cfg.Hidg, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer f.Close()
	desc, err := os.ReadFile(cfg.Gadget + "/functions/hid.0/report_desc")
	if err != nil {
		return err
	}
	defs, err := parseReportDescriptor(desc)
	if err != nil {
		return fmt.Errorf("report descriptor (%d bytes): %w", len(desc), err)
	}
	slog.Info("stereo connected", "reports", describeDefs(defs), "usb_state", srv.udcState())

	o := defaultOptions()
	o.Name = cfg.Name
	o.AuthTimeout = cfg.AuthTimeout
	o.Log = slog.Default()
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

	var stream io.ReadWriter = &hidgStream{f}
	if o.Trace != nil && cfg.TraceHID {
		stream = &tracedStream{rw: stream, t: o.Trace}
	}
	fr, fw := newLink(stream, defs, o.Log.Warn)
	stopCh := make(chan error, 1)
	stop := func(err error) {
		select {
		case stopCh <- err:
		default:
		}
	}
	p := newPod(o, fw, srv.ph, stop)
	go p.loop()
	go readLoop(p, fr, stop)
	go srv.watchUDC(ctx, stop)
	p.start()
	srv.cur.Store(p)
	defer func() {
		srv.cur.Store(nil)
		close(p.done)
	}()

	select {
	case err := <-stopCh:
		return err
	case <-ctx.Done():
		return nil
	}
}

// watchUDC ends the session when the stereo leaves the configured state for 3 s.
func (srv *server) watchUDC(ctx context.Context, stop func(error)) {
	bad := 0
	for ctx.Err() == nil {
		time.Sleep(500 * time.Millisecond)
		if st := srv.udcState(); st != "configured" {
			if bad++; bad >= 6 {
				stop(fmt.Errorf("the stereo left the USB state %q", st))
				return
			}
		} else {
			bad = 0
		}
	}
}

func readLoop(p *pod, fr ipod.FrameReader, stop func(error)) {
	fails := 0
	for {
		frame, err := fr.ReadFrame()
		if err != nil {
			fails++
			if fails >= 3 || err == io.EOF || strings.Contains(err.Error(), "file already closed") {
				stop(fmt.Errorf("read: %w", err))
				return
			}
			p.o.Log.Warn("report read error", "err", err)
			continue
		}
		fails = 0
		pkts, err := splitPackets(frame)
		if err != nil {
			p.o.Log.Warn("bad packet in frame", "err", err, "frame", hex.EncodeToString(frame))
		}
		for _, pk := range pkts {
			if r, ok := parsePayload(append([]byte(nil), pk...)); ok {
				p.post(func() { p.onRx(r) })
			}
		}
	}
}

const ctlHelp = `commands: status | devices | pair [seconds] | toggle | play | pause | next | prev | help
  status   what the stereo and the phone report
  pair     make the Pi visible for pairing (default 600 s)
  toggle, play, pause, next, prev   press the button on the phone's player (AVRCP)`

func (srv *server) control(line string) string {
	f := strings.Fields(line)
	if len(f) == 0 {
		return "empty command. " + ctlHelp
	}
	switch f[0] {
	case "status":
		if p := srv.cur.Load(); p != nil {
			return p.call(p.status)
		}
		return fmt.Sprintf("no stereo session (usb state: %s)\nphone: %+v", srv.udcState(), srv.ph.State())
	case "devices":
		return strings.Join(srv.ph.Devices(), "\n")
	case "pair":
		secs := uint32(pairingSecs)
		if len(f) > 1 {
			if n, err := strconv.Atoi(f[1]); err == nil && n > 0 {
				secs = uint32(n)
			}
		}
		if err := srv.ph.OpenPairing(secs); err != nil {
			return "error: " + err.Error()
		}
		return fmt.Sprintf("pairing open for %d s: pair from the phone's Bluetooth settings (%s)", secs, srv.cfg.BTName)
	case "toggle", "play", "pause", "next", "prev":
		action := map[string]string{"play": "play", "pause": "pause", "next": "next", "prev": "previous"}[f[0]]
		if f[0] == "toggle" {
			action = "play"
			if iapPlayState(srv.ph.State().Status) == 1 {
				action = "pause"
			}
		}
		if err := srv.ph.Control(action); err != nil {
			return "error: " + err.Error()
		}
		return "ok"
	case "help":
		return ctlHelp
	}
	return "unknown command. " + ctlHelp
}

func (srv *server) listen(ctx context.Context) error {
	path := srv.cfg.Socket
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
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(15 * time.Second))
				line, _ := bufio.NewReader(c).ReadString('\n')
				fmt.Fprintln(c, srv.control(strings.TrimSpace(line)))
			}()
		}
	}()
	return nil
}

func ctl(sock, line string) int {
	c, err := net.DialTimeout("unix", sock, 3*time.Second)
	if err != nil {
		fmt.Fprintln(os.Stderr, "cannot reach ipod-bridge:", err)
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
