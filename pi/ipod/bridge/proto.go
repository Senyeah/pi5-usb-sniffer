package main

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/oandrew/ipod"
	audio "github.com/oandrew/ipod/lingo-audio"
	dispremote "github.com/oandrew/ipod/lingo-dispremote"
	extremote "github.com/oandrew/ipod/lingo-extremote"
	general "github.com/oandrew/ipod/lingo-general"
	simpleremote "github.com/oandrew/ipod/lingo-simpleremote"
)

// Blank uses keep the upstream lingo tables registered. They give command names in the log.
var (
	_ = audio.Lingos
	_ = dispremote.Lingos
	_ = extremote.Lingos
	_ = general.Lingos
	_ = simpleremote.Lingos
)

const (
	lGeneral = ipod.LingoGeneralID
	lDisp    = ipod.LingoDisplayRemoteID
	lExt     = ipod.LingoExtRemoteID
	lAudio   = ipod.LingoDigitalAudioID
)

func cid(lingo uint8, cmd uint16) ipod.LingoCmdID { return ipod.NewLingoCmdID(uint16(lingo), cmd) }

type rawArgs []byte

func (r rawArgs) MarshalBinary() ([]byte, error) { return r, nil }

// encodeFrame builds one iAP packet (start byte, length, lingo, command, arguments, checksum).
func encodeFrame(id ipod.LingoCmdID, args []byte) ([]byte, error) {
	var serde ipod.CommandSerde // no transaction IDs: the stereo does not use IDPS
	payload, err := serde.MarshalCmd(&ipod.Command{ID: id, Payload: rawArgs(args)})
	if err != nil {
		return nil, err
	}
	pw := ipod.NewPacketWriter()
	if err := pw.WritePacket(payload); err != nil {
		return nil, err
	}
	return pw.Bytes(), nil
}

// splitPackets cuts a frame into iAP packet payloads. Unlike upstream PacketReader it never panics.
// A bad packet ends the frame: the packets before it are still returned, with an error.
func splitPackets(frame []byte) ([][]byte, error) {
	var out [][]byte
	for len(frame) > 0 {
		i := bytes.IndexByte(frame, ipod.PacketStartByte)
		if i < 0 {
			return out, nil // zero padding
		}
		frame = frame[i+1:]
		if len(frame) < 2 {
			return out, errors.New("truncated header")
		}
		off, n := 1, int(frame[0])
		if frame[0] == 0x00 { // large packet: 2-byte length
			if len(frame) < 3 {
				return out, errors.New("truncated header")
			}
			off, n = 3, int(binary.BigEndian.Uint16(frame[1:3]))
		}
		if total := off + n + 1; total > len(frame) {
			return out, fmt.Errorf("truncated packet: header says %d bytes, %d are left", total, len(frame))
		}
		pkt := frame[:off+n+1]
		if ipod.Checksum(pkt) != 0 {
			return out, errors.New("bad checksum")
		}
		out = append(out, pkt[off:off+n])
		frame = frame[off+n+1:]
	}
	return out, nil
}

// tracedStream writes every raw HID report to the trace.
type tracedStream struct {
	rw io.ReadWriter
	t  *tracer
}

func (s *tracedStream) Read(p []byte) (int, error) {
	n, err := s.rw.Read(p)
	if n > 0 {
		s.t.write("D>A", "hid-report", p[:n])
	}
	return n, err
}

func (s *tracedStream) Write(p []byte) (int, error) {
	s.t.write("A>D", "hid-report", p)
	return s.rw.Write(p)
}

// rx is one packet from the Apple device.
type rx struct {
	id   ipod.LingoCmdID
	args []byte
	raw  []byte
}

func parsePayload(p []byte) (rx, bool) {
	if len(p) < 2 {
		return rx{}, false
	}
	if p[0] == lExt {
		if len(p) < 3 {
			return rx{}, false
		}
		return rx{id: cid(p[0], uint16(p[1])<<8|uint16(p[2])), args: p[3:], raw: p}, true
	}
	return rx{id: cid(p[0], uint16(p[1])), args: p[2:], raw: p}, true
}

// cmdName gives the upstream type name, or the numeric ID.
func cmdName(id ipod.LingoCmdID, argLen int) string {
	if r, ok := ipod.Lookup(id, argLen, false); ok {
		return strings.TrimPrefix(fmt.Sprintf("%T", r.Payload), "*")
	}
	return id.GoString()
}

// tracer writes one JSON line per packet. A nil tracer does nothing.
type tracer struct {
	mu sync.Mutex
	f  *os.File
}

func newTracer(path string) (*tracer, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		return nil, err
	}
	return &tracer{f: f}, nil
}

func (t *tracer) write(dir, name string, raw []byte) {
	if t == nil {
		return
	}
	line, _ := json.Marshal(map[string]string{
		"t":    time.Now().UTC().Format("2006-01-02T15:04:05.000000Z"),
		"dir":  dir,
		"name": name,
		"raw":  hex.EncodeToString(raw),
	})
	t.mu.Lock()
	defer t.mu.Unlock()
	t.f.Write(append(line, '\n'))
}

func (t *tracer) close() {
	if t != nil {
		t.f.Close()
	}
}
