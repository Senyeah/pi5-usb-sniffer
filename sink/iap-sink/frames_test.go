package main

import (
	"bytes"
	"testing"

	"github.com/oandrew/ipod"
	"github.com/oandrew/ipod/hid"
)

func mustFrame(t *testing.T, id ipod.LingoCmdID, args []byte) []byte {
	t.Helper()
	f, err := encodeFrame(id, args)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestSplitPackets(t *testing.T) {
	small := mustFrame(t, ext(0x1D), []byte{0, 0, 1, 0, 0, 0, 2, 0, 1})
	large := mustFrame(t, ext(0x21), bytes.Repeat([]byte("x"), 300))
	frame := append(append(append([]byte(nil), small...), large...), 0, 0, 0) // padding at the end
	pkts, err := splitPackets(frame)
	if err != nil || len(pkts) != 2 {
		t.Fatalf("got %d packets, err %v", len(pkts), err)
	}
	if r, ok := parsePayload(pkts[1]); !ok || r.id != ext(0x21) || len(r.args) != 300 {
		t.Errorf("second packet: %+v", r)
	}
}

func TestSplitPacketsNeverPanics(t *testing.T) {
	good := mustFrame(t, ext(0x1D), []byte{0, 0, 1, 0, 0, 0, 2, 0, 1})
	bad := append([]byte(nil), good...)
	bad[len(bad)-1] ^= 0xFF // wrong checksum
	// A tail that starts in the middle of a packet: the header byte 0x45 says 69 bytes, 62 are left.
	tail := append([]byte{0x55, 0x45}, bytes.Repeat([]byte{0x41}, 60)...)
	for name, f := range map[string][]byte{
		"truncated":    good[:len(good)-3],
		"bad checksum": bad,
		"mid-packet":   tail,
		"start only":   {0x55},
		"large header": {0x55, 0x00, 0x01},
		"empty":        nil,
	} {
		pkts, err := splitPackets(f)
		if name != "empty" && err == nil && len(pkts) > 0 {
			t.Errorf("%s: accepted %d packets", name, len(pkts))
		}
	}
	// Good packets before a bad one still come back.
	pkts, err := splitPackets(append(append([]byte(nil), good...), tail...))
	if len(pkts) != 1 || err == nil {
		t.Errorf("got %d packets, err %v", len(pkts), err)
	}
}

func TestFrameReaderDropsOrphanFragments(t *testing.T) {
	defs := tableFor(t, fullSpeedDescHex)
	acc, pod, done := pipePair()
	defer done()
	warnings := 0
	fr, _ := newLink(acc, defs, func(string, ...any) { warnings++ })

	report := func(id byte, lc hid.LinkControl, payload []byte, size int) []byte {
		r := make([]byte, 1+size)
		r[0], r[1] = id, byte(lc)
		copy(r[2:], payload)
		return r
	}
	good := mustFrame(t, ext(0x1D), []byte{0, 0, 1, 0, 0, 0, 2, 0, 1})
	// A last fragment with no first fragment, then a middle fragment, then a whole frame.
	pod.Write(report(4, hid.LinkControlContinue, bytes.Repeat([]byte{0x41}, 40), 63))
	pod.Write(report(4, hid.LinkControlContinue|hid.LinkControlMoreToFollow, bytes.Repeat([]byte{0x42}, 40), 63))
	pod.Write(report(3, hid.LinkControlDone, good, 20))

	frame, err := fr.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(frame, good) {
		t.Errorf("frame %x, want it to start with %x", frame, good)
	}
	if warnings != 2 {
		t.Errorf("%d warnings, want 2", warnings)
	}

	// Two fragments of one packet join into one frame.
	long := mustFrame(t, ext(0x23), bytes.Repeat([]byte("a"), 90)) // 96 bytes: needs two reports at full speed
	pod.Write(report(4, hid.LinkControlMoreToFollow, long[:62], 63))
	pod.Write(report(4, hid.LinkControlContinue, long[62:], 63))
	frame, err = fr.ReadFrame()
	if err != nil || !bytes.HasPrefix(frame, long) {
		t.Fatalf("joined frame wrong: %v", err)
	}
}
