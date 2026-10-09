package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/oandrew/ipod"
	"github.com/oandrew/ipod/hid"
)

// Raw packets that the Panasonic stereo sent in session car-10 (A>D, from the decoder's timeline.jsonl).
func TestRequestsMatchCapture(t *testing.T) {
	cases := []struct {
		st  step
		raw string
	}{
		{stepIdentify, "550e0013000004190000000200000200be"},
		{initFirst[0], "5503000f00ee"},
		{initFirst[1], "5503000f04ea"},
		{initFirst[2], "5503000f0ae4"},
		{initFirst[3], "55020009f5"},
		{initFirst[4], "55020005f9"},
		{initFirst[5], "5503040039c0"},
		{initFirst[6], "5503040016e3"},
		{initFirst[7], "550304002fca"},
		{initSecond[0], "550304001cdd"},
		{initSecond[1], "550404001805db"},
		{initSecond[2], "5507040028ffffffffd1"}, // PlayCurrentSelection, seen with a stopped iPod in the car on 09/10/2026
		{initSecond[3], "550404002901ce"},
		{initSecond[4], "550404002601d1"},
		{initSecond[5], "550304002ccd"},
		{initSecond[6], "5503040009f0"},
		{initSecond[7], "550304001edb"},
		{initSecond[8], "5503040035c4"},
		{initSecond[9], "5503040014e5"},
		{initSecond[10], "550404002907c8"},
		{initSecond[11], "5503040002f7"},
		{stepPlayControl(0x01), "550404002901ce"},
		{stepTrackQuery("album", 0x24, 2), "550704002400000002cf"},
		{stepTrackQuery("title", 0x20, 2), "550704002000000002d3"},
		{stepTrackQuery("artist", 0x22, 2), "550704002200000002d1"},
		{stepSetTrack(11), "55070400370000000bb3"},
	}
	for _, c := range cases {
		got, err := encodeFrame(c.st.id, c.st.args)
		if err != nil {
			t.Fatalf("%s: %v", c.st.name, err)
		}
		if hex.EncodeToString(got) != c.raw {
			t.Errorf("%s:\n got %x\nwant %s", c.st.name, got, c.raw)
		}
	}
	// Frames that onRx builds inline.
	got, _ := encodeFrame(cid(lAudio, 0x03), []byte{0x00, 0x00, 0x7D, 0x00, 0x00, 0x00, 0xAC, 0x44, 0x00, 0x00, 0xBB, 0x80})
	if hex.EncodeToString(got) != "550e0a0300007d000000ac440000bb803d" {
		t.Errorf("RetAccessorySampleRateCaps: %x", got)
	}
	got, _ = encodeFrame(cid(lGeneral, 0x28), []byte{0x00, 0x00, 0x00, 0x00, 0x01})
	if hex.EncodeToString(got) != "550700280000000001d0" {
		t.Errorf("RetAccessoryInfo: %x", got)
	}
	got, _ = encodeFrame(cid(lAudio, 0x00), []byte{0x00, 0x04})
	if hex.EncodeToString(got) != "55040a000004ee" {
		t.Errorf("AccessoryAck: %x", got)
	}
}

// fakePod is a minimal iPod. It answers like the iPad in car-10, with made-up values.
type fakePod struct {
	enc *hid.Encoder
	dec *hid.Decoder
	wmu sync.Mutex

	mu    sync.Mutex
	got   []rx
	state byte
	track int32
	fail  map[uint16]byte // error status for the ACK of these commands
}

func newFakePod(conn io.ReadWriter, defs hid.ReportDefs) *fakePod {
	return &fakePod{
		enc:   hid.NewEncoder(hid.NewReportWriter(conn), defs),
		dec:   hid.NewDecoder(hid.NewReportReader(conn), defs),
		state: 2, track: 1,
	}
}

func (p *fakePod) send(id ipod.LingoCmdID, args ...byte) {
	frame, _ := encodeFrame(id, args)
	p.wmu.Lock()
	defer p.wmu.Unlock()
	p.enc.WriteFrame(frame)
}

func u32(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }

func (p *fakePod) run() {
	for {
		frame, err := p.dec.ReadFrame()
		if err != nil {
			return
		}
		pr := ipod.NewPacketReader(append([]byte(nil), frame...))
		for {
			payload, err := pr.ReadPacket()
			if err != nil {
				break
			}
			if r, ok := parsePayload(append([]byte(nil), payload...)); ok {
				p.mu.Lock()
				p.got = append(p.got, r)
				p.mu.Unlock()
				p.reply(r)
			}
		}
	}
}

func (p *fakePod) ackExt(cmd uint16) { p.send(ext(1), 0x00, byte(cmd>>8), byte(cmd)) }

func (p *fakePod) reply(r rx) {
	switch r.id {
	case cid(lGeneral, 0x13): // IdentifyDeviceLingoes
		p.send(cid(lGeneral, 0x02), 0x00, 0x13)
		p.send(cid(lGeneral, 0x14))
	case cid(lGeneral, 0x15): // RetDevAuthenticationInfo
		if r.args[2] != r.args[3] {
			p.send(cid(lGeneral, 0x02), 0x00, 0x15)
			return
		}
		p.send(cid(lGeneral, 0x16), 0x00)
		p.send(cid(lAudio, 0x02))
		p.send(cid(lGeneral, 0x27), 0x00)
		p.send(cid(lGeneral, 0x17), append(bytes.Repeat([]byte{0x92}, 20), 0x01)...)
		for i := 0; i < 5; i++ { // the iPod repeats this until the accessory acks
			time.AfterFunc(time.Duration(i)*30*time.Millisecond, func() {
				p.send(cid(lAudio, 0x04), append(u32(44100), make([]byte, 8)...)...)
			})
		}
	case cid(lGeneral, 0x0F):
		p.send(cid(lGeneral, 0x10), r.args[0], 0x01, 0x09)
	case cid(lGeneral, 0x09):
		p.send(cid(lGeneral, 0x0A), 26, 6, 1)
	case cid(lGeneral, 0x05):
		p.send(cid(lGeneral, 0x02), 0x06, 0x05, 0x00, 0x00, 0x0B, 0xB8) // pending
		p.send(cid(lGeneral, 0x02), 0x00, 0x05)
	case ext(0x39):
		p.send(ext(0x3A), 0x00, 0xA6, 0x00, 0x4C, 0x02)
	case ext(0x16):
		p.ackExt(0x16)
	case ext(0x2F):
		p.send(ext(0x30), 0x02)
	case ext(0x1C):
		p.mu.Lock()
		st := p.state
		p.mu.Unlock()
		p.send(ext(0x1D), append(append(u32(340373), u32(5274)...), st)...)
	case ext(0x18):
		p.send(ext(0x19), u32(19)...)
	case ext(0x26):
		p.ackExt(0x26)
	case ext(0x2C):
		p.send(ext(0x2D), 0x01)
	case ext(0x09):
		p.send(ext(0x0A), 0x00)
	case ext(0x1E):
		p.mu.Lock()
		tr := p.track
		p.mu.Unlock()
		p.send(ext(0x1F), be32(tr)...)
	case ext(0x35):
		p.send(ext(0x36), u32(3)...)
	case ext(0x14):
		p.send(ext(0x15), []byte("Test iPad\x00")...)
	case ext(0x28): // PlayCurrentSelection
		p.mu.Lock()
		status := p.fail[0x28]
		if status == 0 {
			p.state = 1
		}
		p.mu.Unlock()
		p.send(ext(1), status, 0x00, 0x28)
	case ext(0x29):
		p.mu.Lock()
		status := p.fail[0x29]
		if r.args[0] == 0x01 && status == 0 {
			if p.state == 1 {
				p.state = 2
			} else {
				p.state = 1
			}
		}
		p.mu.Unlock()
		p.send(ext(1), status, 0x00, 0x29)
	case ext(0x02):
		p.send(ext(0x03), 0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0)
	case ext(0x37):
		p.mu.Lock()
		p.track = int32(binary.BigEndian.Uint32(r.args))
		p.mu.Unlock()
		p.ackExt(0x37)
	case ext(0x20):
		p.send(ext(0x21), []byte("Test Title\x00")...)
	case ext(0x22):
		p.send(ext(0x23), []byte("Test Artist\x00")...)
	case ext(0x24):
		p.send(ext(0x25), []byte("Test Album\x00")...)
	}
}

func (p *fakePod) received() []rx {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]rx(nil), p.got...)
}

func testOptions(cert []byte) options {
	o := defaultOptions()
	o.Cert = cert
	o.CertDelay = 5 * time.Millisecond
	o.InitDelay = 5 * time.Millisecond
	o.Settle = 20 * time.Millisecond
	o.AudioAckDelay = 200 * time.Millisecond
	o.PollEvery = 40 * time.Millisecond
	o.StepTimeout = time.Second
	o.Autoplay = true
	o.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	return o
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

func statusOf(t *testing.T, st *stereo) statusDoc {
	t.Helper()
	var d statusDoc
	if err := json.Unmarshal([]byte(st.call(st.status)), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func indexOf(rs []rx, from int, id ipod.LingoCmdID, arg0 int) int {
	for i := from; i < len(rs); i++ {
		if rs[i].id == id && (arg0 < 0 || (len(rs[i].args) > 0 && int(rs[i].args[0]) == arg0)) {
			return i
		}
	}
	return -1
}

func TestStereoSessionAgainstFakeIPod(t *testing.T) {
	for name, desc := range map[string]string{"full speed": fullSpeedDescHex, "high speed": highSpeedDescHex} {
		t.Run(name, func(t *testing.T) {
			defs := tableFor(t, desc)
			cert := make([]byte, 946) // same size as the stereo's certificate: two sections, 500 and 446 bytes
			for i := range cert {
				cert[i] = byte(i * 3)
			}
			accConn, podConn, closeAll := pipePair()
			defer closeAll()
			pod := newFakePod(podConn, defs)
			go pod.run()

			fr, fw := newLink(accConn, defs, nil)
			stopped := make(chan error, 1)
			st := newStereo(testOptions(cert), fw, func(err error) {
				select {
				case stopped <- err:
				default:
				}
			})
			go st.loop()
			defer close(st.done)
			go readLoop(st, fr, st.stop)
			st.start()

			waitFor(t, "init and metadata", func() bool {
				d := statusOf(t, st)
				return d.InitDone && d.Title == "Test Title"
			})

			got := pod.received()
			if i := indexOf(got, 0, cid(lGeneral, 0x13), -1); i != 0 {
				t.Fatalf("IdentifyDeviceLingoes at %d, want first", i)
			}

			// Certificate: two sections, the data joins to the certificate.
			c0 := indexOf(got, 0, cid(lGeneral, 0x15), 0x02)
			c1 := indexOf(got, c0+1, cid(lGeneral, 0x15), 0x02)
			if c0 < 0 || c1 < 0 {
				t.Fatal("certificate sections missing")
			}
			if !bytes.Equal(got[c0].args[:4], []byte{2, 0, 0, 1}) || !bytes.Equal(got[c1].args[:4], []byte{2, 0, 1, 1}) {
				t.Errorf("section headers %x %x", got[c0].args[:4], got[c1].args[:4])
			}
			if joined := append(append([]byte(nil), got[c0].args[4:]...), got[c1].args[4:]...); !bytes.Equal(joined, cert) {
				t.Error("certificate sections do not join to the certificate")
			}
			if len(got[c0].args[4:]) != 500 {
				t.Errorf("first section holds %d bytes, want 500", len(got[c0].args[4:]))
			}

			// Caps, info and the init script, in the order of car-10.
			pos := c1
			for _, want := range []ipod.LingoCmdID{cid(lAudio, 0x03), cid(lGeneral, 0x28)} {
				if i := indexOf(got, pos, want, -1); i < 0 {
					t.Errorf("reply %v missing", want)
				}
			}
			pos = c1
			for _, s := range append(append([]step(nil), initFirst...), initSecond...) {
				if s.ifStopped { // the fake iPod is paused
					continue
				}
				i := indexOf(got, pos, s.id, -1)
				if i < 0 {
					t.Fatalf("init step %q missing after position %d", s.name, pos)
				}
				pos = i + 1
			}

			// One AccessoryAck for the audio attributes, no signature.
			waitFor(t, "AccessoryAck", func() bool { return indexOf(pod.received(), 0, cid(lAudio, 0x00), -1) >= 0 })
			time.Sleep(300 * time.Millisecond)
			got = pod.received()
			acks := 0
			for _, r := range got {
				if r.id == cid(lAudio, 0x00) {
					acks++
				}
				if r.id == cid(lGeneral, 0x18) {
					t.Error("sent a signature without a key")
				}
			}
			if acks != 1 {
				t.Errorf("%d AccessoryAck packets, want 1", acks)
			}

			// Autoplay: the iPod was paused, so end-ff/rew then toggle.
			waitFor(t, "autoplay", func() bool { return statusOf(t, st).PlayState == "playing" })
			got = pod.received()
			i7 := indexOf(got, 0, ext(0x29), 0x07)
			i1 := indexOf(got, 0, ext(0x29), 0x01)
			if i1 < 0 || i7 < 0 || i7 > i1 {
				t.Errorf("play button sequence wrong: end-ff/rew at %d, toggle at %d", i7, i1)
			}

			d := statusOf(t, st)
			if d.Artist != "Test Artist" || d.Album != "Test Album" || d.IPodName != "Test iPad" || d.IPodSW != "26.6.1" || d.NumTracks != 3 {
				t.Errorf("status %+v", d)
			}

			// Control: next and prev send PlayControl 0x08 and 0x09.
			for cmd, code := range map[string]int{"next": 0x08, "prev": 0x09} {
				if r := st.call(func() string { return st.control(cmd) }); r != "ok" {
					t.Fatal(r)
				}
				waitFor(t, cmd, func() bool { return indexOf(pod.received(), 0, ext(0x29), code) >= 0 })
			}

			select {
			case err := <-stopped:
				t.Errorf("session stopped: %v", err)
			default:
			}
		})
	}
}

func TestNoCertificateStaysSilent(t *testing.T) {
	defs := tableFor(t, fullSpeedDescHex)
	accConn, podConn, closeAll := pipePair()
	defer closeAll()
	pod := newFakePod(podConn, defs)
	go pod.run()
	fr, fw := newLink(accConn, defs, nil)
	st := newStereo(testOptions(nil), fw, func(error) {})
	go st.loop()
	defer close(st.done)
	go readLoop(st, fr, st.stop)
	st.start()
	// The fake iPod asks for the certificate. With no certificate nothing goes back, so init starts after 8 s only.
	waitFor(t, "identify", func() bool { return len(pod.received()) > 0 })
	time.Sleep(300 * time.Millisecond)
	for _, r := range pod.received() {
		if r.id == cid(lGeneral, 0x15) {
			t.Fatal("sent a certificate that does not exist")
		}
	}
}

func TestStereoNextButton(t *testing.T) {
	cases := []struct {
		name          string
		track, tracks int32
		want          []byte
		wantID        ipod.LingoCmdID
	}{
		{"index inside the list", 1, 3, be32(2), ext(0x37)},
		{"last track wraps to 0", 2, 3, be32(0), ext(0x37)},
		{"index beyond the count (iPhone)", 22, 4, []byte{0x08}, ext(0x29)},
	}
	for _, c := range cases {
		rec := &recordingFrames{}
		s := newStereo(testOptions(nil), rec, func(error) {})
		go s.loop()
		s.post(func() { s.track, s.numTracks = c.track, uint32(c.tracks) })
		if r := s.call(func() string { return s.control("stereo-next") }); r != "ok" {
			t.Fatal(r)
		}
		waitFor(t, c.name, func() bool { return rec.count() >= 1 })
		want, _ := encodeFrame(c.wantID, c.want)
		if got := rec.frames()[0]; !bytes.Equal(got, want) {
			t.Errorf("%s: sent %x, want %x", c.name, got, want)
		}
		close(s.done)
	}
}

func startAgainst(t *testing.T, pod *fakePod, o options) *stereo {
	t.Helper()
	defs := tableFor(t, fullSpeedDescHex)
	accConn, podConn, closeAll := pipePair()
	t.Cleanup(closeAll)
	pod.enc = hid.NewEncoder(hid.NewReportWriter(podConn), defs)
	pod.dec = hid.NewDecoder(hid.NewReportReader(podConn), defs)
	go pod.run()
	fr, fw := newLink(accConn, defs, nil)
	st := newStereo(o, fw, func(error) {})
	go st.loop()
	t.Cleanup(func() { close(st.done) })
	go readLoop(st, fr, st.stop)
	st.start()
	return st
}

// With a stopped iPod the real stereo starts play by itself, between the two track counts of the init script.
func TestStoppedIPodMakesTheStereoStartPlay(t *testing.T) {
	pod := &fakePod{state: 0, track: 1}
	o := testOptions(make([]byte, 946))
	o.Autoplay = false
	st := startAgainst(t, pod, o)
	waitFor(t, "init", func() bool { return statusOf(t, st).InitDone })
	got := pod.received()
	count := indexOf(got, 0, ext(0x18), -1)
	sel := indexOf(got, 0, ext(0x28), -1)
	toggle := indexOf(got, 0, ext(0x29), 0x01)
	notify := indexOf(got, 0, ext(0x26), -1)
	if !(count >= 0 && count < sel && sel < toggle && toggle < notify) {
		t.Fatalf("order: track count %d, PlayCurrentSelection %d, toggle %d, notifications %d", count, sel, toggle, notify)
	}
	if !bytes.Equal(got[sel].args, []byte{0xFF, 0xFF, 0xFF, 0xFF}) {
		t.Errorf("PlayCurrentSelection args %x", got[sel].args)
	}
	if d := statusOf(t, st); len(d.Errors) != 0 {
		t.Errorf("errors %v with an iPod that says OK", d.Errors)
	}
}

// Both commands failed in the car. The stereo shows that to the driver, so the test stereo must report it.
func TestErrorRepliesAreReported(t *testing.T) {
	pod := &fakePod{state: 0, track: 1, fail: map[uint16]byte{0x28: 5, 0x29: 2}}
	o := testOptions(make([]byte, 946))
	o.Autoplay = false
	st := startAgainst(t, pod, o)
	waitFor(t, "init", func() bool { return statusOf(t, st).InitDone })
	errs := statusOf(t, st).Errors
	want := map[string]bool{"PlayCurrentSelection: status 5": false, "PlayControl: status 2": false}
	for _, e := range errs {
		if _, ok := want[e]; ok {
			want[e] = true
		}
	}
	for e, seen := range want {
		if !seen {
			t.Errorf("error %q missing from %v", e, errs)
		}
	}
}

// Car-10 had a paused iPad: the stereo did not start play in the init script.
func TestPausedIPodGetsNoPlayCurrentSelection(t *testing.T) {
	pod := &fakePod{state: 2, track: 1}
	o := testOptions(make([]byte, 946))
	o.Autoplay = false
	st := startAgainst(t, pod, o)
	waitFor(t, "init", func() bool { return statusOf(t, st).InitDone })
	if i := indexOf(pod.received(), 0, ext(0x28), -1); i >= 0 {
		t.Error("PlayCurrentSelection sent to a paused iPod")
	}
}

func TestStereoBackButton(t *testing.T) {
	cases := []struct {
		name          string
		track, tracks int32
		pos           uint32
		want          [][]byte // first frames, as payloads: lingo, command, args
	}{
		{"early in the track: previous index", 3, 6, 500, [][]byte{{0x04, 0x00, 0x37, 0, 0, 0, 2}}},
		{"index 0 wraps to the end", 0, 6, 500, [][]byte{{0x04, 0x00, 0x37, 0, 0, 0, 5}}},
		{"later in the track: restart", 3, 6, 7900, [][]byte{{0x04, 0x00, 0x29, 0x01}, {0x04, 0x00, 0x29, 0x04}}},
	}
	for _, c := range cases {
		rec := &recordingFrames{}
		s := newStereo(testOptions(nil), rec, func(error) {})
		go s.loop()
		s.post(func() { s.track, s.numTracks, s.pos = c.track, uint32(c.tracks), c.pos })
		if r := s.call(func() string { return s.control("stereo-back") }); r != "ok" {
			t.Fatal(r)
		}
		waitFor(t, c.name, func() bool { return rec.count() >= len(c.want) })
		for i, w := range c.want {
			r, _ := parsePayload(w)
			want, _ := encodeFrame(r.id, r.args)
			if got := rec.frames()[i]; !bytes.Equal(got, want) {
				t.Errorf("%s: frame %d %x, want %x", c.name, i, got, want)
			}
		}
		close(s.done)
	}
}
