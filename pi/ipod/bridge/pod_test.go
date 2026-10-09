package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/oandrew/ipod"
	"github.com/oandrew/ipod/hid"
)

// reportConn is one end of an in-memory HID link. One Write is one report, like /dev/hidg0.
type reportConn struct {
	in     <-chan []byte
	out    chan<- []byte
	closed chan struct{}
}

func pipePair() (a, b *reportConn, closeBoth func()) {
	ab, ba := make(chan []byte, 512), make(chan []byte, 512)
	closed := make(chan struct{})
	var once sync.Once
	return &reportConn{in: ba, out: ab, closed: closed}, &reportConn{in: ab, out: ba, closed: closed},
		func() { once.Do(func() { close(closed) }) }
}

func (c *reportConn) Read(p []byte) (int, error) {
	select {
	case b := <-c.in:
		return copy(p, b), nil
	case <-c.closed:
		return 0, io.EOF
	}
}

func (c *reportConn) Write(p []byte) (int, error) {
	select {
	case c.out <- append([]byte(nil), p...):
		return len(p), nil
	case <-c.closed:
		return 0, io.ErrClosedPipe
	}
}

func tableFor(t *testing.T, desc string) hid.ReportDefs {
	t.Helper()
	defs, err := parseReportDescriptor(mustHex(t, desc))
	if err != nil {
		t.Fatal(err)
	}
	return defs
}

// fakePhone is a Bluetooth phone with a player.
type fakePhone struct {
	mu      sync.Mutex
	st      phoneState
	actions []string
	ch      chan struct{}
}

func newFakePhone() *fakePhone {
	return &fakePhone{
		ch: make(chan struct{}, 1),
		st: phoneState{Connected: true, Name: "Test Phone", Status: "playing", At: time.Now(),
			Track: track{Title: "Song A", Artist: "Artist A", Album: "Album A", Genre: "Pop", DurationMS: 340373}},
	}
}

func (f *fakePhone) State() phoneState {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st
}

func (f *fakePhone) Control(a string) error {
	f.mu.Lock()
	f.actions = append(f.actions, a)
	f.mu.Unlock()
	return nil
}

func (f *fakePhone) SetShuffle(m string) error { f.record("shuffle=" + m); return nil }
func (f *fakePhone) SetRepeat(m string) error  { f.record("repeat=" + m); return nil }
func (f *fakePhone) Changed() <-chan struct{}  { return f.ch }

func (f *fakePhone) record(a string) {
	f.mu.Lock()
	f.actions = append(f.actions, a)
	f.mu.Unlock()
}

func (f *fakePhone) did() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.actions...)
}

func (f *fakePhone) set(fn func(*phoneState)) {
	f.mu.Lock()
	fn(&f.st)
	f.mu.Unlock()
	select {
	case f.ch <- struct{}{}:
	default:
	}
}

// stereo is a scripted car stereo on the other end of the link.
type stereo struct {
	t   *testing.T
	fw  ipod.FrameWriter
	mu  sync.Mutex
	got []rx
}

func startPod(t *testing.T, desc string, ph phone, mod func(*options)) (*stereo, *pod, func()) {
	t.Helper()
	defs := tableFor(t, desc)
	podConn, stConn, closeAll := pipePair()
	o := defaultOptions()
	o.NotifyEvery = 40 * time.Millisecond
	o.AttrEvery = 30 * time.Millisecond
	o.AuthTimeout = 400 * time.Millisecond
	o.TrackSettle = 60 * time.Millisecond
	o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	if mod != nil {
		mod(&o)
	}
	fr, fw := newLink(podConn, defs, nil)
	p := newPod(o, fw, ph, func(error) {})
	go p.loop()
	go readLoop(p, fr, func(error) {})
	p.start()

	s := &stereo{t: t, fw: &frameWriter{defs: defs, w: hid.NewReportWriter(stConn), dir: hid.ReportDirAccOut}}
	reader := &frameReader{r: hid.NewReportReader(stConn), defs: defs}
	go func() {
		for {
			frame, err := reader.ReadFrame()
			if err != nil {
				return
			}
			pkts, _ := splitPackets(frame)
			for _, pk := range pkts {
				if r, ok := parsePayload(append([]byte(nil), pk...)); ok {
					s.mu.Lock()
					s.got = append(s.got, r)
					s.mu.Unlock()
				}
			}
		}
	}()
	return s, p, func() { close(p.done); closeAll() }
}

func (s *stereo) send(id ipod.LingoCmdID, args ...byte) {
	s.t.Helper()
	frame, err := encodeFrame(id, args)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := s.fw.WriteFrame(frame); err != nil {
		s.t.Fatal(err)
	}
}

func (s *stereo) packets() []rx {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]rx(nil), s.got...)
}

// next waits for a packet with this ID after position from. It returns the position after it.
func (s *stereo) next(from int, id ipod.LingoCmdID) (rx, int) {
	s.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pk := s.packets()
		for i := from; i < len(pk); i++ {
			if pk[i].id == id {
				return pk[i], i + 1
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.t.Fatalf("no packet %s after position %d; got %d packets", id.GoString(), from, len(s.packets()))
	return rx{}, 0
}

func (s *stereo) count(id ipod.LingoCmdID) int {
	n := 0
	for _, r := range s.packets() {
		if r.id == id {
			n++
		}
	}
	return n
}

func mustFrame(t *testing.T, id ipod.LingoCmdID, args []byte) string {
	t.Helper()
	f, err := encodeFrame(id, args)
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(f)
}

func frameOf(r rx) string {
	f, _ := encodeFrame(r.id, r.args)
	return hex.EncodeToString(f)
}

func u32b(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }

// authenticate runs the identify and certificate steps. It returns the position after the last packet of the exchange.
func authenticate(t *testing.T, s *stereo) int {
	t.Helper()
	s.send(cid(lGeneral, 0x13), 0x00, 0x00, 0x04, 0x19, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x02, 0x00)
	_, pos := s.next(0, cid(lGeneral, 0x14))
	cert := bytes.Repeat([]byte{0xA5}, 946)
	s.send(cid(lGeneral, 0x15), append([]byte{2, 0, 0, 1}, cert[:500]...)...)
	_, pos = s.next(pos, cid(lGeneral, 0x02))
	s.send(cid(lGeneral, 0x15), append([]byte{2, 0, 1, 1}, cert[500:]...)...)
	_, pos = s.next(pos, cid(lGeneral, 0x16))
	return pos
}

// The packets of the real iPad in session car-10 (D>A), rebuilt from the same values.
func TestRepliesMatchTheRealIPad(t *testing.T) {
	cases := []struct {
		name  string
		id    ipod.LingoCmdID
		args  []byte
		frame string
	}{
		{"iPodAck Identify", cid(lGeneral, 0x02), []byte{0x00, 0x13}, "550400020013e7"},
		{"GetDevAuthenticationInfo", cid(lGeneral, 0x14), nil, "55020014ea"},
		{"AckDevAuthenticationInfo", cid(lGeneral, 0x16), []byte{0x00}, "5503001600e7"},
		{"GetAccessorySampleRateCaps", cid(lAudio, 0x02), nil, "55020a02f2"},
		{"GetAccessoryInfo", cid(lGeneral, 0x27), []byte{0x00}, "5503002700d6"},
		{"TrackNewAudioAttributes", cid(lAudio, 0x04), append(append(u32b(44100), u32b(0)...), u32b(0)...), "550e0a040000ac440000000000000000f4"},
		{"AckDevAuthenticationStatus", cid(lGeneral, 0x19), []byte{0x00}, "5503001900e4"},
		{"ReturnLingoProtocolVersion General", cid(lGeneral, 0x10), []byte{0x00, 0x01, 0x09}, "55050010000109e1"},
		{"ReturniPodSoftwareVersion", cid(lGeneral, 0x0A), []byte{26, 6, 1}, "5505000a1a0601d0"},
		{"iPodAck pending EnterExtendedInterfaceMode", cid(lGeneral, 0x02), append([]byte{0x06, 0x05}, u32b(3000)...), "55080002060500000bb828"},
		{"ReturnColorDisplayImageLimits", ext(0x3A), colorDisplayLimits, "550d04003a00a6004c0200a6004c03cc"},
		{"iPodAck ResetDBSelection", ext(0x01), []byte{0x00, 0x00, 0x16}, "5506040001000016df"},
		{"ReturnRepeat all", ext(0x30), []byte{0x02}, "550404003002c6"},
		{"ReturnPlayStatus", ext(0x1D), append(append(u32b(0x00053195), u32b(0x149a)...), 0x02), "550c04001d000531950000149a0258"},
		{"ReturnNumberCategorizedDBRecords", ext(0x19), u32b(19), "550704001900000013c9"},
		{"ReturnShuffle tracks", ext(0x2D), []byte{0x01}, "550404002d01ca"},
		{"ReturnAudiobookSpeed", ext(0x0A), []byte{0x00}, "550404000a00ee"},
		{"ReturnCurrentPlayingTrackIndex", ext(0x1F), u32b(1), "550704001f00000001d5"},
		{"ReturnNumPlayingTracks", ext(0x36), u32b(2), "550704003600000002bd"},
		{"ReturnCurrentPlayingTrackChapterInfo", ext(0x03), chapterInfoNone, "550b040003ffffffff00000000f2"},
		{"PlayStatusChangeNotification track index", ext(0x27), append([]byte{0x01}, u32b(1)...), "55080400270100000001cb"},
		{"PlayStatusChangeNotification track time", ext(0x27), append([]byte{0x04}, u32b(0x15f2)...), "550804002704000015f2c2"},
		{"ReturnIndexedPlayingTrackInfo capabilities", ext(0x0D), append(append(append([]byte{0x00}, u32b(4)...), u32b(0x00053195)...), 0, 0), "550e04000d000000000400053195000012"},
		{"ReturnIndexedPlayingTrackInfo composer", ext(0x0D), append([]byte{0x06}, 0), "550504000d0600e4"},
		{"ReturnIndexedPlayingTrackInfo genre", ext(0x0D), append([]byte{0x05}, 0), "550504000d0500e5"},
		{"iPodAck PlayControl", ext(0x01), []byte{0x00, 0x00, 0x29}, "5506040001000029cc"},
	}
	for _, c := range cases {
		if got := mustFrame(t, c.id, c.args); got != c.frame {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, c.frame)
		}
	}
}

// The engine must produce the same bytes as the iPad when its state equals the iPad's.
func TestEngineRepliesAreTheIPadsBytes(t *testing.T) {
	ph := newFakePhone()
	ph.set(func(s *phoneState) {
		s.Status = "paused"
		s.PosMS = 0x149a
		s.At = time.Now()
		s.Repeat, s.Shuffle = "alltracks", "alltracks"
		s.Track.DurationMS = 0x00053195
	})
	st, _, done := startPod(t, fullSpeedDescHex, ph, func(o *options) { o.Name = "Test iPad" })
	defer done()
	pos := authenticate(t, st)

	check := func(req ipod.LingoCmdID, args []byte, want ipod.LingoCmdID, wantFrame string) {
		t.Helper()
		st.send(req, args...)
		r, p := st.next(pos, want)
		pos = p
		if got := frameOf(r); got != wantFrame {
			t.Errorf("reply to %s:\n got %s\nwant %s", req.GoString(), got, wantFrame)
		}
	}
	check(cid(lGeneral, 0x0F), []byte{lGeneral}, cid(lGeneral, 0x10), "55050010000109e1")
	check(cid(lGeneral, 0x09), nil, cid(lGeneral, 0x0A), "5505000a1a0601d0")
	check(ext(0x39), nil, ext(0x3A), "550d04003a00a6004c0200a6004c03cc")
	check(ext(0x2F), nil, ext(0x30), "550404003002c6")
	check(ext(0x2C), nil, ext(0x2D), "550404002d01ca")
	check(ext(0x09), nil, ext(0x0A), "550404000a00ee")
	check(ext(0x02), nil, ext(0x03), "550b040003ffffffff00000000f2")
	check(ext(0x1C), nil, ext(0x1D), "550c04001d000531950000149a0258")
	check(ext(0x0C), []byte{0x00, 0, 0, 0, 0, 0, 0}, ext(0x0D), "550e04000d000000000400053195000012")
	check(ext(0x0C), []byte{0x06, 0, 0, 0, 1, 0, 0}, ext(0x0D), "550504000d0600e4")
	st.send(ext(0x29), 0x07)
	r, _ := st.next(pos, ext(0x01))
	if got := frameOf(r); got != "5506040001000029cc" {
		t.Errorf("PlayControl ack %s", got)
	}
}
