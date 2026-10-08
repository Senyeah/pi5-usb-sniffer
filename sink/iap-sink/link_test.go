package main

import (
	"bytes"
	"io"
	"sync"
	"testing"

	"github.com/oandrew/ipod/hid"
)

// reportConn is one end of an in-memory HID link. One Write is one report, like hidraw.
type reportConn struct {
	in     <-chan []byte
	out    chan<- []byte
	closed chan struct{}
}

func pipePair() (a, b *reportConn, closeBoth func()) {
	ab, ba := make(chan []byte, 256), make(chan []byte, 256)
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

func TestFrameRoundTrip(t *testing.T) {
	for name, desc := range map[string]string{"full speed": fullSpeedDescHex, "high speed": highSpeedDescHex} {
		defs := tableFor(t, desc)
		for _, n := range []int{1, 17, 62, 63, 100, 254, 255, 512, 1000} {
			acc, pod, done := pipePair()
			_, fw := newLink(acc, defs, nil)
			dec := hid.NewDecoder(hid.NewReportReader(pod), defs)

			frame := make([]byte, n)
			for i := range frame {
				frame[i] = byte(i*7 + 1)
			}
			if err := fw.WriteFrame(frame); err != nil {
				t.Fatalf("%s %d bytes: write: %v", name, n, err)
			}
			got, err := dec.ReadFrame()
			if err != nil {
				t.Fatalf("%s %d bytes: read: %v", name, n, err)
			}
			// The last report is padded with zeros. iAP ignores trailing zeros.
			if !bytes.HasPrefix(got, frame) || len(bytes.TrimRight(got[len(frame):], "\x00")) != 0 {
				t.Errorf("%s %d bytes: frame differs", name, n)
			}
			done()
		}
	}
}

func TestWritesOnlyOutputReports(t *testing.T) {
	defs := tableFor(t, fullSpeedDescHex)
	rec := &recordingWriter{}
	fw := &frameWriter{defs: defs, w: rec}
	if err := fw.WriteFrame(make([]byte, 200)); err != nil {
		t.Fatal(err)
	}
	// Full speed: output report 9 has 62 payload bytes. 200 bytes need four reports.
	want := []hid.LinkControl{hid.LinkControlMoreToFollow, hid.LinkControlContinue | hid.LinkControlMoreToFollow,
		hid.LinkControlContinue | hid.LinkControlMoreToFollow, hid.LinkControlContinue}
	if len(rec.reports) != len(want) {
		t.Fatalf("%d reports, want %d", len(rec.reports), len(want))
	}
	for i, r := range rec.reports {
		if r.ID < 5 || r.ID > 9 {
			t.Errorf("report %d has ID %d, want an output ID 5..9", i, r.ID)
		}
		if r.LinkControl != want[i] {
			t.Errorf("report %d link control %#x, want %#x", i, r.LinkControl, want[i])
		}
	}
}

// recordingFrames is a FrameWriter that keeps what it gets.
type recordingFrames struct {
	mu sync.Mutex
	f  [][]byte
}

func (r *recordingFrames) WriteFrame(b []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.f = append(r.f, append([]byte(nil), b...))
	return nil
}

func (r *recordingFrames) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.f)
}

func (r *recordingFrames) frames() [][]byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([][]byte(nil), r.f...)
}

type recordingWriter struct{ reports []hid.Report }

func (r *recordingWriter) WriteReport(p hid.Report) error {
	r.reports = append(r.reports, p)
	return nil
}
