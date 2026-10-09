package main

import (
	"encoding/hex"
	"errors"
	"io"

	"github.com/oandrew/ipod"
	"github.com/oandrew/ipod/hid"
)

// frameWriter splits iAP frames into HID input reports (iPod to stereo).
// Upstream hid.Encoder does this too, but its default table gives IDs 5 to 9 the wrong direction.
type frameWriter struct {
	defs hid.ReportDefs
	w    hid.ReportWriter
	dir  hid.ReportDir // the direction of the reports that this side writes
}

var _ ipod.FrameWriter = (*frameWriter)(nil)

func (e *frameWriter) WriteFrame(data []byte) error {
	offset, left := 0, len(data)
	for left > 0 {
		def, err := e.defs.Pick(left, e.dir)
		if err != nil {
			return err
		}
		max := def.MaxPayload()
		if max <= 0 {
			return errors.New("output report too small")
		}
		n, lc := left, hid.LinkControlDone
		if left > max {
			n = max
			if offset == 0 {
				lc = hid.LinkControlMoreToFollow
			} else {
				lc = hid.LinkControlContinue | hid.LinkControlMoreToFollow
			}
		} else if offset > 0 {
			lc = hid.LinkControlContinue
		}
		payload := make([]byte, max)
		copy(payload, data[offset:offset+n])
		if err := e.w.WriteReport(hid.Report{ID: byte(def.ID), LinkControl: lc, Data: payload}); err != nil {
			return err
		}
		left -= n
		offset += n
	}
	return nil
}

// frameReader joins HID output reports from the stereo into iAP frames. Upstream hid.Decoder returns the tail of a
// packet when the first fragment is missing, and a stray 0x55 in that tail then looks like a packet start.
// This reader drops orphan fragments and reports them through warn.
type frameReader struct {
	r    hid.ReportReader
	defs hid.ReportDefs
	buf  []byte
	open bool
	warn func(msg string, args ...any)
}

var _ ipod.FrameReader = (*frameReader)(nil)

func (d *frameReader) ReadFrame() ([]byte, error) {
	for {
		rep, err := d.r.ReadReport()
		if err != nil {
			return nil, err
		}
		def, err := d.defs.Find(int(rep.ID))
		if err != nil {
			return nil, err
		}
		data := rep.Data[:min(len(rep.Data), def.MaxPayload())]
		note := func(msg string) {
			if d.warn != nil {
				d.warn(msg, "report_id", rep.ID, "link_control", rep.LinkControl, "bytes", len(rep.Data), "data", hex.EncodeToString(data))
			}
		}
		switch rep.LinkControl {
		case hid.LinkControlDone: // a whole frame in one report
			if d.open {
				note("unfinished frame dropped")
			}
			d.open = false
			return append([]byte(nil), data...), nil
		case hid.LinkControlMoreToFollow: // first fragment
			if d.open {
				note("unfinished frame dropped")
			}
			d.buf, d.open = append(d.buf[:0], data...), true
		case hid.LinkControlContinue | hid.LinkControlMoreToFollow: // middle fragment
			if !d.open {
				note("middle fragment without a first fragment: dropped")
				continue
			}
			d.buf = append(d.buf, data...)
		case hid.LinkControlContinue: // last fragment
			if !d.open {
				note("last fragment without a first fragment: dropped")
				continue
			}
			d.buf = append(d.buf, data...)
			d.open = false
			return append([]byte(nil), d.buf...), nil
		default:
			note("unknown link control")
		}
	}
}

// newLink returns the frame reader and writer for a HID report stream.
// rw gives one report per Read and takes one report per Write, like /dev/hidg0.
func newLink(rw io.ReadWriter, defs hid.ReportDefs, warn func(string, ...any)) (ipod.FrameReader, ipod.FrameWriter) {
	r := &frameReader{r: hid.NewReportReader(rw), defs: defs, warn: warn}
	w := &frameWriter{defs: defs, w: hid.NewReportWriter(rw), dir: hid.ReportDirAccIn}
	return r, w
}
