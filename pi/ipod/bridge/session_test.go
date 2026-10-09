package main

import (
	"encoding/binary"
	"fmt"
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

			// Metadata of the current track, in the middle of a large virtual list.
			idx, pos := st.query(pos, ext(0x1E), nil, ext(0x1F))
			if binary.BigEndian.Uint32(idx.args) != virtualStart {
				t.Errorf("index %x", idx.args)
			}
			num, pos := st.query(pos, ext(0x35), nil, ext(0x36))
			if binary.BigEndian.Uint32(num.args) != virtualCount {
				t.Errorf("number of tracks %x", num.args)
			}
			for _, c := range []struct {
				req  uint16
				want string
			}{{0x20, "Song A"}, {0x22, "Artist A"}, {0x24, "Album A"}} {
				r, p := st.query(pos, ext(c.req), u32b(virtualStart), ext(c.req+1))
				pos = p
				if string(r.args) != c.want+"\x00" {
					t.Errorf("reply to %#x: %q, want %q", c.req, r.args, c.want)
				}
			}
			r, pos := st.query(pos, ext(0x20), u32b(0), ext(0x21)) // no previous track yet
			if string(r.args) != "\x00" {
				t.Errorf("title of an unknown track: %q", r.args)
			}
			r, pos = st.query(pos, ext(0x0C), append(append([]byte{0x05}, u32b(virtualStart)...), 0, 0), ext(0x0D))
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
			st.send(ext(0x37), u32b(virtualStart+1)...)
			waitActions(t, ph, "next")
			ph.set(func(s *phoneState) {
				s.Status = "playing"
				s.Track = track{Title: "Song B", Artist: "Artist B", Album: "Album B", DurationMS: 200000}
			})
			v, pos := st.nextNotification(pos, 0x01)
			if v != virtualStart+1 {
				t.Errorf("index notification %d, want %d", v, virtualStart+1)
			}
			idx, pos = st.query(pos, ext(0x1E), nil, ext(0x1F))
			if binary.BigEndian.Uint32(idx.args) != virtualStart+1 {
				t.Errorf("index after next %x", idx.args)
			}
			num, pos = st.query(pos, ext(0x35), nil, ext(0x36))
			if binary.BigEndian.Uint32(num.args) != virtualCount {
				t.Errorf("number of tracks after next %x", num.args)
			}
			r, pos = st.query(pos, ext(0x20), u32b(virtualStart+1), ext(0x21))
			if string(r.args) != "Song B\x00" {
				t.Errorf("new title %q", r.args)
			}
			r, pos = st.query(pos, ext(0x20), u32b(virtualStart), ext(0x21))
			if string(r.args) != "Song A\x00" {
				t.Errorf("previous title %q", r.args)
			}
			ps, pos := st.query(pos, ext(0x1C), nil, ext(0x1D))
			if binary.BigEndian.Uint32(ps.args) != 200000 {
				t.Errorf("track length %x", ps.args)
			}

			// Previous: the index goes back and Song A is the current track again.
			st.send(ext(0x37), u32b(virtualStart)...)
			waitActions(t, ph, "previous")
			ph.set(func(s *phoneState) {
				s.Track = track{Title: "Song A", Artist: "Artist A", Album: "Album A", Genre: "Pop", DurationMS: 340373}
			})
			v, pos = st.nextNotification(pos, 0x01)
			if v != virtualStart {
				t.Errorf("index notification %d, want %d", v, virtualStart)
			}
			r, pos = st.query(pos, ext(0x20), u32b(virtualStart), ext(0x21))
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

// An error reply makes the real stereo show "ERROR 2": with no phone the commands are acknowledged and ignored.
func TestControlWithoutPhoneIsAcknowledged(t *testing.T) {
	ph := newFakePhone()
	ph.set(func(s *phoneState) { *s = phoneState{Status: "stopped"} })
	st, _, done := startPod(t, fullSpeedDescHex, ph, nil)
	defer done()
	pos := authenticate(t, st)
	for _, c := range []struct {
		name string
		id   ipod.LingoCmdID
		args []byte
	}{
		{"PlayControl toggle", ext(0x29), []byte{0x01}},
		{"PlayControl next", ext(0x29), []byte{0x08}},
		{"PlayCurrentSelection", ext(0x28), []byte{0xFF, 0xFF, 0xFF, 0xFF}},
	} {
		var ack rx
		ack, pos = st.query(pos, c.id, c.args, ext(0x01))
		if ack.args[0] != ackOK {
			t.Errorf("%s: ack %x, want success", c.name, ack.args)
		}
	}
	if len(ph.did()) != 0 {
		t.Errorf("the fake phone got %v", ph.did())
	}
}

// When the iPod is stopped, the real stereo starts play with PlayCurrentSelection(-1), then a toggle.
func TestPlayCurrentSelectionStartsThePhone(t *testing.T) {
	for _, c := range []struct {
		status string
		want   bool
	}{{"paused", true}, {"stopped", true}, {"playing", false}} {
		ph := newFakePhone()
		ph.set(func(s *phoneState) { s.Status = c.status })
		st, _, done := startPod(t, fullSpeedDescHex, ph, nil)
		pos := authenticate(t, st)
		ack, _ := st.query(pos, ext(0x28), []byte{0xFF, 0xFF, 0xFF, 0xFF}, ext(0x01))
		if ack.args[0] != ackOK || ack.args[1] != 0x00 || ack.args[2] != 0x28 {
			t.Errorf("%s: ack %x, want success for 0x0028", c.status, ack.args)
		}
		if c.want {
			waitActions(t, ph, "play")
		} else {
			time.Sleep(100 * time.Millisecond)
			if len(ph.did()) != 0 {
				t.Errorf("%s: the fake phone got %v", c.status, ph.did())
			}
		}
		done()
	}
}

// The stereo acks the attributes when it is ready for audio. Until then they go again at each start of play,
// so a phone that connects late reaches the stereo. After the first ack they do not repeat: the car stereo
// acks once per connection.
func TestAttributesRepeatUntilTheFirstAck(t *testing.T) {
	ph := newFakePhone()
	ph.set(func(s *phoneState) { s.Status = "paused" })
	st, _, done := startPod(t, fullSpeedDescHex, ph, func(o *options) { o.AttrTries = 3 })
	defer done()
	authenticate(t, st)
	attrs := cid(lAudio, 0x04)
	waitCount := func(want int) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for st.count(attrs) < want && time.Now().Before(deadline) {
			time.Sleep(5 * time.Millisecond)
		}
		time.Sleep(150 * time.Millisecond) // more than a few repeats must not come
		if n := st.count(attrs); n != want {
			t.Fatalf("%d TrackNewAudioAttributes, want %d", n, want)
		}
	}
	waitCount(3)
	ph.set(func(s *phoneState) { s.Status = "playing" })
	waitCount(6)
	st.send(cid(lAudio, 0x00), 0x00, 0x04) // AccessoryAck
	ph.set(func(s *phoneState) { s.Status = "paused" })
	time.Sleep(100 * time.Millisecond)
	ph.set(func(s *phoneState) {
		s.Status = "playing"
		s.Track = track{Title: "Song B", DurationMS: 1000}
	})
	waitCount(6)
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
	if v != virtualStart+1 {
		t.Errorf("index %d after the change, want %d", v, virtualStart+1)
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
	if n := p.call(func() string { return fmt.Sprint(p.idx) }); n != fmt.Sprint(virtualStart+1) {
		t.Errorf("index %s, want %d", n, virtualStart+1)
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
	r, pos := st.query(pos, ext(0x20), u32b(virtualStart), ext(0x21))
	if string(r.args) != waitingText+"\x00" {
		t.Fatalf("title with no track data: %q", r.args)
	}

	ph.set(func(s *phoneState) {
		s.Status = "playing"
		s.Track = track{Title: "Song A", Artist: "Artist A", Album: "Album A", DurationMS: 340373}
	})
	v, pos := st.nextNotification(pos, 0x01)
	if v != virtualStart+1 {
		t.Errorf("index notification %d, want %d", v, virtualStart+1)
	}
	r, _ = st.query(pos, ext(0x20), u32b(virtualStart+1), ext(0x21))
	if string(r.args) != "Song A\x00" {
		t.Errorf("title after the notification: %q", r.args)
	}
}

// With no phone the stereo gets a playing track called "Waiting", not an empty, stopped iPod ("Unsupported").
func TestWaitingTrackWithoutPhone(t *testing.T) {
	ph := newFakePhone()
	ph.set(func(s *phoneState) { *s = phoneState{Status: "stopped"} })
	st, _, done := startPod(t, fullSpeedDescHex, ph, nil)
	defer done()
	pos := authenticate(t, st)

	ps, pos := st.query(pos, ext(0x1C), nil, ext(0x1D))
	if l := binary.BigEndian.Uint32(ps.args); l != waitingTrack.DurationMS || ps.args[8] != 1 {
		t.Fatalf("play status %x: want playing, length %d", ps.args, waitingTrack.DurationMS)
	}
	time.Sleep(60 * time.Millisecond)
	ps2, pos := st.query(pos, ext(0x1C), nil, ext(0x1D))
	if binary.BigEndian.Uint32(ps2.args[4:]) <= binary.BigEndian.Uint32(ps.args[4:]) {
		t.Errorf("the position does not move: %x then %x", ps.args, ps2.args)
	}
	for _, n := range []uint32{virtualStart, virtualStart + 1, 0} {
		for _, req := range []uint16{0x20, 0x22, 0x24} {
			var r rx
			r, pos = st.query(pos, ext(req), u32b(n), ext(req+1))
			if string(r.args) != waitingText+"\x00" {
				t.Errorf("reply to %#x for track %d: %q", req, n, r.args)
			}
		}
	}
	for _, kind := range []byte{0x05, 0x06} {
		var r rx
		r, pos = st.query(pos, ext(0x0C), append(append([]byte{kind}, u32b(virtualStart)...), 0, 0), ext(0x0D))
		if string(r.args[1:]) != waitingText+"\x00" {
			t.Errorf("track info %#x: %q", kind, r.args)
		}
	}
	r, _ := st.query(pos, ext(0x14), nil, ext(0x15))
	if string(r.args) != "iPod\x00" {
		t.Errorf("iPod name %q", r.args)
	}
}

// The stereo's play and pause switch the "Waiting" track, and the position stops while it is paused.
func TestStereoPausesTheWaitingTrack(t *testing.T) {
	ph := newFakePhone()
	ph.set(func(s *phoneState) { *s = phoneState{Status: "stopped"} })
	st, _, done := startPod(t, fullSpeedDescHex, ph, nil)
	defer done()
	pos := authenticate(t, st)
	ack, pos := st.query(pos, ext(0x29), []byte{0x01}, ext(0x01))
	if ack.args[0] != ackOK {
		t.Fatalf("ack %x", ack.args)
	}
	ps, pos := st.query(pos, ext(0x1C), nil, ext(0x1D))
	time.Sleep(60 * time.Millisecond)
	ps2, pos := st.query(pos, ext(0x1C), nil, ext(0x1D))
	if ps.args[8] != 2 || ps2.args[8] != 2 || binary.BigEndian.Uint32(ps2.args[4:]) != binary.BigEndian.Uint32(ps.args[4:]) {
		t.Errorf("paused: %x then %x, want state 2 and a fixed position", ps.args, ps2.args)
	}
	_, pos = st.query(pos, ext(0x29), []byte{0x0A}, ext(0x01))
	ps3, _ := st.query(pos, ext(0x1C), nil, ext(0x1D))
	if ps3.args[8] != 1 {
		t.Errorf("after play: %x", ps3.args)
	}
	if len(ph.did()) != 0 {
		t.Errorf("the fake phone got %v", ph.did())
	}
}

// A phone that connects while the stereo plays "Waiting" gets Play, and its track replaces "Waiting".
func TestPhoneThatConnectsGetsTheStereosState(t *testing.T) {
	for _, stereoPlays := range []bool{true, false} {
		ph := newFakePhone()
		ph.set(func(s *phoneState) { *s = phoneState{Status: "stopped"} })
		st, _, done := startPod(t, fullSpeedDescHex, ph, nil)
		pos := authenticate(t, st)
		st.send(ext(0x26), 0x01)
		_, pos = st.next(pos, ext(0x01))
		if !stereoPlays {
			_, pos = st.query(pos, ext(0x29), []byte{0x01}, ext(0x01))
		}
		ph.set(func(s *phoneState) {
			*s = phoneState{Connected: true, Player: true, Name: "Test Phone", Status: "paused",
				Track: track{Title: "Song A", Artist: "Artist A", Album: "Album A", DurationMS: 340373}}
		})
		if stereoPlays {
			waitActions(t, ph, "play")
		} else {
			time.Sleep(200 * time.Millisecond)
			if len(ph.did()) != 0 {
				t.Errorf("the stereo paused, but the phone got %v", ph.did())
			}
		}
		v, pos := st.nextNotification(pos, 0x01)
		r, _ := st.query(pos, ext(0x20), u32b(v), ext(0x21))
		if string(r.args) != "Song A\x00" {
			t.Errorf("title after the phone came: %q", r.args)
		}
		done()
	}
}

func TestStepsTo(t *testing.T) {
	for _, c := range []struct{ from, to, want int32 }{
		{500, 501, 1}, {500, 499, -1}, {500, 502, 2}, {500, 500, 0},
		{500, 520, maxSkips}, {500, 400, -maxSkips},
		{998, 0, 2},  // the stereo wrapped "next" at the end of its list
		{1, 999, -2}, // and "previous" at the start
	} {
		if got := stepsTo(c.from, c.to); got != c.want {
			t.Errorf("stepsTo(%d, %d) = %d, want %d", c.from, c.to, got, c.want)
		}
	}
}

// Two quick presses of "next" come as one index two tracks on: two skips, and the stereo's index.
func TestJumpOfTwoTracks(t *testing.T) {
	ph := newFakePhone()
	st, _, done := startPod(t, fullSpeedDescHex, ph, nil)
	defer done()
	pos := authenticate(t, st)
	st.send(ext(0x26), 0x01)
	_, pos = st.next(pos, ext(0x01))
	st.send(ext(0x37), u32b(virtualStart+2)...)
	deadline := time.Now().Add(2 * time.Second)
	for len(ph.did()) < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := ph.did(); len(got) != 2 || got[0] != "next" || got[1] != "next" {
		t.Fatalf("phone got %v, want next twice", got)
	}
	ph.set(func(s *phoneState) { s.Track = track{Title: "Song C", DurationMS: 1000} })
	if v, _ := st.nextNotification(pos, 0x01); v != virtualStart+2 {
		t.Errorf("index %d, want %d", v, virtualStart+2)
	}
}

// The back button: PlayControl "previous track", then SetCurrentPlayingTrack with the index the stereo had.
// That is one skip back, not a skip back and a skip forward.
func TestBackButtonIsOneSkip(t *testing.T) {
	ph := newFakePhone()
	st, _, done := startPod(t, fullSpeedDescHex, ph, nil)
	defer done()
	pos := authenticate(t, st)
	_, pos = st.query(pos, ext(0x29), []byte{0x04}, ext(0x01))
	ph.set(func(s *phoneState) { s.Track = track{Title: "Song Z", DurationMS: 1000} })
	time.Sleep(200 * time.Millisecond) // the track change settles before the stereo's index comes
	st.query(pos, ext(0x37), u32b(virtualStart), ext(0x01))
	time.Sleep(100 * time.Millisecond)
	if got := ph.did(); len(got) != 1 || got[0] != "previous" {
		t.Errorf("phone got %v, want one previous", got)
	}
}
