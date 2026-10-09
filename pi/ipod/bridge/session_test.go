package main

import (
	"encoding/binary"
	"testing"
	"time"

	"github.com/oandrew/ipod"
)

// nextNotification waits for a PlayStatusChangeNotification of one kind after position from.
func (s *stereo) nextNotification(from int, kind byte) (uint32, int) {
	s.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		pk := s.packets()
		for i := from; i < len(pk); i++ {
			if pk[i].id == ext(0x27) && len(pk[i].args) >= 5 && pk[i].args[0] == kind {
				return binary.BigEndian.Uint32(pk[i].args[1:5]), i + 1
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.t.Fatalf("no notification of kind %#x", kind)
	return 0, 0
}

func (s *stereo) query(from int, req ipod.LingoCmdID, args []byte, want ipod.LingoCmdID) (rx, int) {
	s.t.Helper()
	s.send(req, args...)
	return s.next(from, want)
}

func waitActions(t *testing.T, ph *fakePhone, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, a := range ph.did() {
			if a == want {
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("phone did not get %q; got %v", want, ph.did())
}

func TestAuthenticationSequence(t *testing.T) {
	ph := newFakePhone()
	st, _, done := startPod(t, fullSpeedDescHex, ph, nil)
	defer done()
	pos := authenticate(t, st)
	_, pos = st.next(pos, cid(lAudio, 0x02))
	info, pos := st.next(pos, cid(lGeneral, 0x27))
	if len(info.args) != 1 || info.args[0] != 0 {
		t.Errorf("GetAccessoryInfo args %x, want the capabilities type 0", info.args)
	}
	challenge, pos := st.next(pos, cid(lGeneral, 0x17))
	if len(challenge.args) != 21 || challenge.args[20] != 1 {
		t.Errorf("challenge %x: want 20 bytes and the counter 1", challenge.args)
	}

	// The iPod repeats TrackNewAudioAttributes until the stereo acks it.
	_, pos = st.next(pos, cid(lAudio, 0x04))
	deadline := time.Now().Add(2 * time.Second)
	for st.count(cid(lAudio, 0x04)) < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if st.count(cid(lAudio, 0x04)) < 3 {
		t.Fatal("TrackNewAudioAttributes is not repeated")
	}
	st.send(cid(lAudio, 0x00), 0x00, 0x04)
	time.Sleep(100 * time.Millisecond)
	n := st.count(cid(lAudio, 0x04))
	time.Sleep(150 * time.Millisecond)
	if st.count(cid(lAudio, 0x04)) != n {
		t.Error("TrackNewAudioAttributes goes on after the ack")
	}

	// No signature comes: the authentication passes after the timeout (400 ms in this test).
	status, _ := st.next(pos, cid(lGeneral, 0x19))
	if len(status.args) != 1 || status.args[0] != 0 {
		t.Errorf("AckDevAuthenticationStatus %x, want passed", status.args)
	}
}

func TestSignaturePassesAuthenticationAtOnce(t *testing.T) {
	ph := newFakePhone()
	st, _, done := startPod(t, fullSpeedDescHex, ph, func(o *options) { o.AuthTimeout = time.Minute })
	defer done()
	pos := authenticate(t, st)
	_, pos = st.next(pos, cid(lGeneral, 0x17))
	st.send(cid(lGeneral, 0x18), make([]byte, 128)...)
	status, _ := st.next(pos, cid(lGeneral, 0x19))
	if status.args[0] != 0 {
		t.Errorf("status %x", status.args)
	}
	if st.count(cid(lGeneral, 0x19)) != 1 {
		t.Error("the authentication status was sent more than once")
	}
}

func TestStereoSessionWithPhone(t *testing.T) {
	for name, desc := range map[string]string{"full speed": fullSpeedDescHex, "high speed": highSpeedDescHex} {
		t.Run(name, func(t *testing.T) {
			ph := newFakePhone()
			st, _, done := startPod(t, desc, ph, func(o *options) { o.AuthTimeout = time.Minute })
			defer done()
			pos := authenticate(t, st)

			// Extended interface mode: a pending ack, then the final one.
			st.send(cid(lGeneral, 0x05))
			pend, pos := st.next(pos, cid(lGeneral, 0x02))
			fin, pos := st.next(pos, cid(lGeneral, 0x02))
			if pend.args[0] != 0x06 || fin.args[0] != 0x00 || fin.args[1] != 0x05 {
				t.Errorf("acks %x then %x", pend.args, fin.args)
			}

			// Notifications start after the stereo asks for them.
			st.send(ext(0x26), 0x01)
			_, pos = st.next(pos, ext(0x01))
			t1, pos := st.nextNotification(pos, 0x04)
			t2, pos := st.nextNotification(pos, 0x04)
			if t2 < t1 {
				t.Errorf("the track time goes back: %d then %d", t1, t2)
			}

			// Metadata of the current track. The index is 1 and there is one track more.
			idx, pos := st.query(pos, ext(0x1E), nil, ext(0x1F))
			if binary.BigEndian.Uint32(idx.args) != 1 {
				t.Errorf("index %x", idx.args)
			}
			num, pos := st.query(pos, ext(0x35), nil, ext(0x36))
			if binary.BigEndian.Uint32(num.args) != 3 {
				t.Errorf("number of tracks %x", num.args)
			}
			for _, c := range []struct {
				req  uint16
				want string
			}{{0x20, "Song A"}, {0x22, "Artist A"}, {0x24, "Album A"}} {
				r, p := st.query(pos, ext(c.req), u32b(1), ext(c.req+1))
				pos = p
				if string(r.args) != c.want+"\x00" {
					t.Errorf("reply to %#x: %q, want %q", c.req, r.args, c.want)
				}
			}
			r, pos := st.query(pos, ext(0x20), u32b(0), ext(0x21)) // no previous track yet
			if string(r.args) != "\x00" {
				t.Errorf("title of an unknown track: %q", r.args)
			}
			r, pos = st.query(pos, ext(0x0C), []byte{0x05, 0, 0, 0, 1, 0, 0}, ext(0x0D))
			if string(r.args) != "\x05Pop\x00" {
				t.Errorf("genre reply %q", r.args)
			}
			r, pos = st.query(pos, ext(0x14), nil, ext(0x15))
			if string(r.args) != "Test Phone\x00" {
				t.Errorf("iPod name %q: it must be the phone's name", r.args)
			}

			// Play and pause. The phone plays, so toggle pauses.
			st.send(ext(0x29), 0x07)
			st.send(ext(0x29), 0x01)
			waitActions(t, ph, "pause")
			ph.set(func(s *phoneState) { s.Status = "paused" })
			st.send(ext(0x29), 0x01)
			waitActions(t, ph, "play")
			st.send(ext(0x29), 0x0B)
			st.send(ext(0x29), 0x0A)

			// Next: the stereo asks for index + 1. The phone changes the track and the iPod tells the stereo.
			st.send(ext(0x37), u32b(2)...)
			waitActions(t, ph, "next")
			ph.set(func(s *phoneState) {
				s.Status = "playing"
				s.Track = track{Title: "Song B", Artist: "Artist B", Album: "Album B", DurationMS: 200000}
			})
			v, pos := st.nextNotification(pos, 0x01)
			if v != 2 {
				t.Errorf("index notification %d, want 2", v)
			}
			idx, pos = st.query(pos, ext(0x1E), nil, ext(0x1F))
			if binary.BigEndian.Uint32(idx.args) != 2 {
				t.Errorf("index after next %x", idx.args)
			}
			num, pos = st.query(pos, ext(0x35), nil, ext(0x36))
			if binary.BigEndian.Uint32(num.args) != 4 {
				t.Errorf("number of tracks after next %x", num.args)
			}
			r, pos = st.query(pos, ext(0x20), u32b(2), ext(0x21))
			if string(r.args) != "Song B\x00" {
				t.Errorf("new title %q", r.args)
			}
			r, pos = st.query(pos, ext(0x20), u32b(1), ext(0x21))
			if string(r.args) != "Song A\x00" {
				t.Errorf("previous title %q", r.args)
			}
			ps, pos := st.query(pos, ext(0x1C), nil, ext(0x1D))
			if binary.BigEndian.Uint32(ps.args) != 200000 {
				t.Errorf("track length %x", ps.args)
			}

			// Previous: the index goes back to 1 and Song A is the current track again.
			st.send(ext(0x37), u32b(1)...)
			waitActions(t, ph, "previous")
			ph.set(func(s *phoneState) {
				s.Track = track{Title: "Song A", Artist: "Artist A", Album: "Album A", Genre: "Pop", DurationMS: 340373}
			})
			v, pos = st.nextNotification(pos, 0x01)
			if v != 1 {
				t.Errorf("index notification %d, want 1", v)
			}
			r, pos = st.query(pos, ext(0x20), u32b(1), ext(0x21))
			if string(r.args) != "Song A\x00" {
				t.Errorf("title after previous %q", r.args)
			}

			// PlayControl next and previous, shuffle and repeat.
			st.send(ext(0x29), 0x08)
			st.send(ext(0x29), 0x09)
			st.send(ext(0x2E), 0x01)
			waitActions(t, ph, "shuffle=alltracks")
			st.send(ext(0x31), 0x01)
			waitActions(t, ph, "repeat=singletrack")

			// Unknown commands get an ACK with "unknown ID".
			st.send(cid(lGeneral, 0x7F))
			a, pos := st.next(pos, cid(lGeneral, 0x02))
			_ = a
			ack, _ := st.query(pos, ext(0x3F), nil, ext(0x01))
			if ack.args[0] != 0x05 {
				t.Errorf("ack for an unknown command %x", ack.args)
			}
		})
	}
}

func TestControlWithoutPhoneFails(t *testing.T) {
	ph := newFakePhone()
	ph.set(func(s *phoneState) { *s = phoneState{Status: "stopped"} })
	st, _, done := startPod(t, fullSpeedDescHex, ph, nil)
	defer done()
	pos := authenticate(t, st)
	ack, _ := st.query(pos, ext(0x29), []byte{0x01}, ext(0x01))
	if ack.args[0] != ackFailed {
		t.Errorf("ack %x: with no phone the command must fail", ack.args)
	}
	if len(ph.did()) != 0 {
		t.Errorf("the fake phone got %v", ph.did())
	}
}

// The phone sends a new track in steps: the album first, then the title. That is one track change.
func TestStepwiseMetadataIsOneTrackChange(t *testing.T) {
	ph := newFakePhone()
	st, p, done := startPod(t, fullSpeedDescHex, ph, nil)
	defer done()
	pos := authenticate(t, st)
	st.send(ext(0x26), 0x01)
	_, pos = st.next(pos, ext(0x01))

	ph.set(func(s *phoneState) { s.Track.Album = "Album B" }) // step 1: the old title with the new album
	time.Sleep(20 * time.Millisecond)
	ph.set(func(s *phoneState) {
		s.Track = track{Title: "Song B", Artist: "Artist B", Album: "Album B", DurationMS: 200000}
	})
	v, pos := st.nextNotification(pos, 0x01)
	if v != 2 {
		t.Errorf("index %d after the change, want 2", v)
	}
	time.Sleep(400 * time.Millisecond)
	got := 0
	for _, r := range st.packets() {
		if r.id == ext(0x27) && len(r.args) >= 5 && r.args[0] == 0x01 {
			got++
		}
	}
	if got != 1 {
		t.Errorf("%d track index notifications, want 1", got)
	}
	if n := p.call(func() string { return string(rune('0' + p.idx)) }); n != "2" {
		t.Errorf("index %q, want 2", n)
	}
}

// The phone has no track data when the stereo connects (it just connected, paused). The data comes later.
// The stereo asked for the title before and got an empty one, so the iPod must tell it that the track is new.
func TestLateTrackDataIsAnnounced(t *testing.T) {
	ph := newFakePhone()
	ph.set(func(s *phoneState) { s.Track = track{}; s.Status = "paused" })
	st, _, done := startPod(t, fullSpeedDescHex, ph, nil)
	defer done()
	pos := authenticate(t, st)
	st.send(ext(0x26), 0x01)
	_, pos = st.next(pos, ext(0x01))
	r, pos := st.query(pos, ext(0x20), u32b(1), ext(0x21))
	if string(r.args) != "\x00" {
		t.Fatalf("title with no track data: %q", r.args)
	}

	ph.set(func(s *phoneState) {
		s.Status = "playing"
		s.Track = track{Title: "Song A", Artist: "Artist A", Album: "Album A", DurationMS: 340373}
	})
	v, pos := st.nextNotification(pos, 0x01)
	if v != 2 {
		t.Errorf("index notification %d, want 2", v)
	}
	r, _ = st.query(pos, ext(0x20), u32b(2), ext(0x21))
	if string(r.args) != "Song A\x00" {
		t.Errorf("title after the notification: %q", r.args)
	}
}
