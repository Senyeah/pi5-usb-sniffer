package main

import (
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"syscall"
	"time"

	"github.com/oandrew/ipod"
)

// options tune the stereo. The defaults copy the timing of the Panasonic CQ-JZ41F0AE in session car-10.
type options struct {
	Cert           []byte
	CertSection    int           // bytes per RetDevAuthenticationInfo section
	CertDelay      time.Duration // the stereo's MFi chip needed about 0.9 s
	Signature      string        // "none" or "bogus"
	SignatureDelay time.Duration // the stereo needed about 56 s
	Autoplay       bool
	PollEvery      time.Duration
	InitDelay      time.Duration // pause after the sample rate and info replies
	Settle         time.Duration // pause between the first and second init group
	AudioAckDelay  time.Duration
	StepTimeout    time.Duration
	Trace          *tracer
	Log            *slog.Logger
}

func defaultOptions() options {
	return options{
		CertSection:    500,
		CertDelay:      900 * time.Millisecond,
		Signature:      "none",
		SignatureDelay: 56 * time.Second,
		PollEvery:      600 * time.Millisecond,
		InitDelay:      130 * time.Millisecond,
		Settle:         1500 * time.Millisecond,
		AudioAckDelay:  2250 * time.Millisecond,
		StepTimeout:    2 * time.Second,
		Log:            slog.Default(),
	}
}

const stepGap = 5 * time.Millisecond

// step is one request in a script. The next step starts when expect accepts a reply.
type step struct {
	name      string
	id        ipod.LingoCmdID
	args      []byte
	expect    func(rx) bool // nil: do not wait
	pause     time.Duration // wait before the request
	pauseOnly bool
	sent      bool
}

func wantReply(id ipod.LingoCmdID) func(rx) bool {
	return func(r rx) bool { return r.id == id }
}

// wantAck accepts the success or failure ACK for a command. General "pending" is not final.
func wantAck(lingo uint8, cmd uint16) func(rx) bool {
	return func(r rx) bool {
		switch lingo {
		case lGeneral:
			return r.id == cid(lGeneral, 0x02) && len(r.args) >= 2 && uint16(r.args[1]) == cmd && r.args[0] != 0x06
		case lExt:
			return r.id == cid(lExt, 0x0001) && len(r.args) >= 3 && binary.BigEndian.Uint16(r.args[1:3]) == cmd
		}
		return false
	}
}

func ext(cmd uint16) ipod.LingoCmdID { return cid(lExt, cmd) }

// Requests copied from the stereo in car-10. The tests check the bytes against the capture.
var (
	stepIdentify = step{name: "IdentifyDeviceLingoes", id: cid(lGeneral, 0x13),
		// General, DisplayRemote, ExtendedInterface, DigitalAudio; options 0x2; device ID 0x200
		args: []byte{0x00, 0x00, 0x04, 0x19, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x02, 0x00}}

	initFirst = []step{
		{name: "RequestLingoProtocolVersion General", id: cid(lGeneral, 0x0F), args: []byte{lGeneral}, expect: wantReply(cid(lGeneral, 0x10))},
		{name: "RequestLingoProtocolVersion ExtendedInterface", id: cid(lGeneral, 0x0F), args: []byte{lExt}, expect: wantReply(cid(lGeneral, 0x10))},
		{name: "RequestLingoProtocolVersion DigitalAudio", id: cid(lGeneral, 0x0F), args: []byte{lAudio}, expect: wantReply(cid(lGeneral, 0x10))},
		{name: "RequestiPodSoftwareVersion", id: cid(lGeneral, 0x09), expect: wantReply(cid(lGeneral, 0x0A))},
		{name: "EnterExtendedInterfaceMode", id: cid(lGeneral, 0x05), expect: wantAck(lGeneral, 0x05)},
		{name: "GetColorDisplayImageLimits", id: ext(0x39), expect: wantReply(ext(0x3A))},
		{name: "ResetDBSelection", id: ext(0x16), expect: wantAck(lExt, 0x16)},
		{name: "GetRepeat", id: ext(0x2F), expect: wantReply(ext(0x30))},
	}
	initSecond = []step{
		stepGetPlayStatus,
		{name: "GetNumberCategorizedDBRecords track", id: ext(0x18), args: []byte{0x05}, expect: wantReply(ext(0x19))},
		{name: "SetPlayStatusChangeNotification", id: ext(0x26), args: []byte{0x01}, expect: wantAck(lExt, 0x26)},
		{name: "GetShuffle", id: ext(0x2C), expect: wantReply(ext(0x2D))},
		{name: "GetAudiobookSpeed", id: ext(0x09), expect: wantReply(ext(0x0A))},
		{name: "GetCurrentPlayingTrackIndex", id: ext(0x1E), expect: wantReply(ext(0x1F))},
		{name: "GetNumPlayingTracks", id: ext(0x35), expect: wantReply(ext(0x36))},
		{name: "RequestiPodName", id: ext(0x14), expect: wantReply(ext(0x15))},
		stepPlayControl(0x07),
		{name: "GetCurrentPlayingTrackChapterInfo", id: ext(0x02), expect: wantReply(ext(0x03))},
	}

	stepGetPlayStatus = step{name: "GetPlayStatus", id: ext(0x1C), expect: wantReply(ext(0x1D))}
	stepGetTrackIndex = step{name: "GetCurrentPlayingTrackIndex", id: ext(0x1E), expect: wantReply(ext(0x1F))}
)

func stepPlayControl(action byte) step {
	return step{name: fmt.Sprintf("PlayControl %#x", action), id: ext(0x29), args: []byte{action}, expect: wantAck(lExt, 0x29)}
}

func be32(n int32) []byte { return binary.BigEndian.AppendUint32(nil, uint32(n)) }

func stepTrackQuery(name string, cmd uint16, track int32) step {
	return step{name: name, id: ext(cmd), args: be32(track), expect: wantReply(ext(cmd + 1))}
}

func stepSetTrack(track int32) step {
	return step{name: "SetCurrentPlayingTrack", id: ext(0x37), args: be32(track), expect: wantAck(lExt, 0x37)}
}

// stereo is the accessory side. Everything runs on one goroutine through ev. That avoids locks.
type stereo struct {
	o    options
	fw   ipod.FrameWriter
	ev   chan func()
	done chan struct{}
	stop func(error)

	queue    []step
	cur      *step
	curTimer *time.Timer
	busy     bool

	identified, initStarted, initDone bool
	sentCaps, sentInfo                bool
	audioAckArmed                     bool

	playState   int // 0 stopped, 1 playing, 2 paused, -1 unknown
	length, pos uint32
	track       int32
	numTracks   uint32
	metaTrack   int32
	title       string
	artist      string
	album       string
	podName     string
	podSW       string
	polls       int

	txCount, rxCount int
	rxNames          map[string]int
	lastErr          string
	meta             map[string]string // device facts set by the session
}

func newStereo(o options, fw ipod.FrameWriter, stop func(error)) *stereo {
	return &stereo{
		o: o, fw: fw, stop: stop,
		ev:        make(chan func(), 256),
		done:      make(chan struct{}),
		playState: -1, track: -1, metaTrack: -2,
		rxNames: map[string]int{},
		meta:    map[string]string{},
	}
}

func (s *stereo) post(f func()) {
	select {
	case s.ev <- f:
	case <-s.done:
	}
}

func (s *stereo) after(d time.Duration, f func()) *time.Timer {
	return time.AfterFunc(d, func() { s.post(f) })
}

func (s *stereo) loop() {
	for {
		select {
		case f := <-s.ev:
			f()
		case <-s.done:
			return
		}
	}
}

// call runs f on the loop and waits for its answer. It returns an error text when the session has ended.
func (s *stereo) call(f func() string) string {
	out := make(chan string, 1)
	select {
	case s.ev <- func() { out <- f() }:
	case <-s.done:
		return "session ended"
	}
	select {
	case r := <-out:
		return r
	case <-s.done:
		return "session ended"
	}
}

// start sends the first packet and starts the poll timer.
func (s *stereo) start() {
	s.post(func() {
		s.enqueue(stepIdentify)
		// Without an authentication exchange the init script would never start.
		s.after(8*time.Second, func() {
			if !s.initStarted {
				s.o.Log.Warn("the iPod did not ask for sample rates and info: starting the init script anyway")
				s.startInit()
			}
		})
		var tick func()
		tick = func() {
			s.poll()
			s.after(s.o.PollEvery, tick)
		}
		s.after(s.o.PollEvery, tick)
	})
}

func (s *stereo) send(name string, id ipod.LingoCmdID, args []byte) error {
	frame, err := encodeFrame(id, args)
	if err != nil {
		return err
	}
	s.txCount++
	s.o.Trace.write("A>D", name, frame)
	if !strings.HasPrefix(name, "GetPlayStatus") {
		s.o.Log.Info("A>D", "cmd", name, "args", hex.EncodeToString(args))
	} else {
		s.o.Log.Debug("A>D", "cmd", name)
	}
	if err := s.fw.WriteFrame(frame); err != nil {
		if errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.ESHUTDOWN) || errors.Is(err, syscall.EIO) ||
			errors.Is(err, syscall.EBADF) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
			s.stop(fmt.Errorf("write %s: %w", name, err))
		} else {
			s.o.Log.Warn("write failed", "cmd", name, "err", err) // a STALL gives EPIPE
		}
		return err
	}
	return nil
}

// enqueue adds steps to the script. The script runs one request at a time.
func (s *stereo) enqueue(steps ...step) {
	for _, st := range steps {
		s.queue = append(s.queue, st)
	}
	if !s.busy {
		s.advance()
	}
}

func (s *stereo) advance() {
	if len(s.queue) == 0 {
		s.busy, s.cur = false, nil
		s.onIdle()
		return
	}
	s.busy = true
	st := s.queue[0]
	s.queue = s.queue[1:]
	s.cur = &st
	s.curTimer = s.after(st.pause, func() { s.fire(&st) })
}

func (s *stereo) fire(st *step) {
	if s.cur != st {
		return
	}
	if st.pauseOnly {
		s.cur = nil
		s.advance()
		return
	}
	s.send(st.name, st.id, st.args)
	if st.expect == nil {
		s.cur = nil
		s.after(stepGap, s.advance)
		return
	}
	st.sent = true
	s.curTimer = s.after(s.o.StepTimeout, func() {
		if s.cur == st {
			s.o.Log.Warn("no reply, going on", "cmd", st.name)
			s.cur = nil
			s.advance()
		}
	})
}

func (s *stereo) consume(r rx) {
	st := s.cur
	if st == nil || !st.sent || st.expect == nil {
		return
	}
	if r.id == cid(lGeneral, 0x02) && len(r.args) >= 6 && r.args[0] == 0x06 && st.expect != nil {
		// "command pending": wait for the final ACK for as long as the iPod says.
		wait := time.Duration(binary.BigEndian.Uint32(r.args[2:6]))*time.Millisecond + 500*time.Millisecond
		s.curTimer.Stop()
		s.curTimer = s.after(max(wait, s.o.StepTimeout), func() {
			if s.cur == st {
				s.o.Log.Warn("no final ACK, going on", "cmd", st.name)
				s.cur = nil
				s.advance()
			}
		})
		return
	}
	if st.expect(r) {
		s.curTimer.Stop()
		s.cur = nil
		s.after(stepGap, s.advance)
	}
}

func (s *stereo) startInit() {
	if s.initStarted {
		return
	}
	s.initStarted = true
	first := append([]step(nil), initFirst...)
	first[0].pause = s.o.InitDelay
	s.enqueue(first...)
	s.enqueue(step{name: "settle", pauseOnly: true, pause: s.o.Settle})
	s.enqueue(initSecond...)
}

func (s *stereo) onIdle() {
	if !s.initStarted || s.initDone {
		return
	}
	s.initDone = true
	s.o.Log.Info("init script done", "play_state", s.playState, "track", s.track, "tracks", s.numTracks)
	if s.o.Autoplay && s.playState != 1 {
		s.o.Log.Info("autoplay: pressing play")
		s.pressToggle()
	}
}

func (s *stereo) poll() {
	if !s.initDone || s.busy {
		return
	}
	s.polls++
	s.enqueue(stepGetPlayStatus)
	if s.polls%2 == 0 {
		s.enqueue(stepGetTrackIndex)
	}
}

// pressToggle sends what the stereo sends for the play button: end fast forward/rewind, then toggle.
func (s *stereo) pressToggle() {
	s.enqueue(stepPlayControl(0x07), stepPlayControl(0x01))
}

func (s *stereo) queryMeta() {
	if s.track < 0 || s.track == s.metaTrack {
		return
	}
	s.metaTrack = s.track
	s.enqueue(
		step{name: "GetNumPlayingTracks", id: ext(0x35), expect: wantReply(ext(0x36))},
		stepTrackQuery("GetIndexedPlayingTrackAlbumName", 0x24, s.track),
		stepTrackQuery("GetIndexedPlayingTrackTitle", 0x20, s.track),
		stepTrackQuery("GetIndexedPlayingTrackArtistName", 0x22, s.track),
	)
}

func cstr(b []byte) string { return strings.TrimRight(string(b), "\x00") }

// onRx handles one packet from the iPod.
func (s *stereo) onRx(r rx) {
	s.rxCount++
	name := cmdName(r.id, len(r.args))
	s.rxNames[name]++
	s.o.Trace.write("D>A", name, r.raw)
	if r.id == ext(0x1D) || r.id == ext(0x1F) || r.id == ext(0x27) { // polled or frequent
		s.o.Log.Debug("D>A", "cmd", name, "args", hex.EncodeToString(r.args))
	} else {
		s.o.Log.Info("D>A", "cmd", name, "args", hex.EncodeToString(r.args))
	}

	switch r.id {
	case cid(lGeneral, 0x14): // GetDevAuthenticationInfo
		s.after(s.o.CertDelay, s.sendCert)
	case cid(lGeneral, 0x16): // AckDevAuthenticationInfo
		if len(r.args) > 0 && r.args[0] != 0 {
			s.o.Log.Warn("the iPod rejected the certificate", "status", r.args[0])
		}
	case cid(lGeneral, 0x17): // GetDevAuthenticationSignature: challenge, counter
		s.onChallenge(r.args)
	case cid(lGeneral, 0x19): // AckDevAuthenticationStatus
		s.o.Log.Info("authentication result", "status", hex.EncodeToString(r.args))
	case cid(lAudio, 0x02): // GetAccessorySampleRateCaps
		s.send("RetAccessorySampleRateCaps", cid(lAudio, 0x03),
			[]byte{0x00, 0x00, 0x7D, 0x00, 0x00, 0x00, 0xAC, 0x44, 0x00, 0x00, 0xBB, 0x80}) // 32000, 44100, 48000 Hz
		s.sentCaps = true
		s.maybeInit()
	case cid(lGeneral, 0x27): // GetAccessoryInfo
		if len(r.args) > 0 && r.args[0] == 0x00 { // capabilities
			s.send("RetAccessoryInfo", cid(lGeneral, 0x28), []byte{0x00, 0x00, 0x00, 0x00, 0x01})
			s.sentInfo = true
			s.maybeInit()
		} else {
			s.o.Log.Warn("unsupported accessory info type", "args", hex.EncodeToString(r.args))
		}
	case cid(lAudio, 0x04): // TrackNewAudioAttributes: the iPod repeats it every 0.5 s until it gets an ACK
		if !s.audioAckArmed {
			s.audioAckArmed = true
			s.after(s.o.AudioAckDelay, func() {
				s.audioAckArmed = false
				s.send("AccessoryAck TrackNewAudioAttributes", cid(lAudio, 0x00), []byte{0x00, 0x04})
			})
		}
	case ext(0x1D): // ReturnPlayStatus: length, position, state
		if len(r.args) >= 9 {
			s.length = binary.BigEndian.Uint32(r.args[0:4])
			s.pos = binary.BigEndian.Uint32(r.args[4:8])
			s.playState = int(r.args[8])
		}
	case ext(0x1F): // ReturnCurrentPlayingTrackIndex
		if len(r.args) >= 4 {
			s.track = int32(binary.BigEndian.Uint32(r.args))
			s.queryMeta()
		}
	case ext(0x36): // ReturnNumPlayingTracks
		if len(r.args) >= 4 {
			s.numTracks = binary.BigEndian.Uint32(r.args)
		}
	case ext(0x21):
		s.title = cstr(r.args)
		s.logNowPlaying()
	case ext(0x23):
		s.artist = cstr(r.args)
		s.logNowPlaying()
	case ext(0x25):
		s.album = cstr(r.args)
	case ext(0x15):
		s.podName = cstr(r.args)
	case cid(lGeneral, 0x0A): // ReturniPodSoftwareVersion
		if len(r.args) >= 3 {
			s.podSW = fmt.Sprintf("%d.%d.%d", r.args[0], r.args[1], r.args[2])
		}
	case ext(0x27): // PlayStatusChangeNotification: type, value
		if len(r.args) >= 5 && r.args[0] == 0x01 { // track index
			s.track = int32(binary.BigEndian.Uint32(r.args[1:5]))
			s.queryMeta()
		}
	}
	s.consume(r)
}

func (s *stereo) logNowPlaying() {
	s.o.Log.Info("now playing", "track", s.track, "title", s.title, "artist", s.artist, "album", s.album, "state", s.playState)
}

func (s *stereo) maybeInit() {
	if s.sentCaps && s.sentInfo {
		s.startInit()
	}
}

func (s *stereo) sendCert() {
	cert := s.o.Cert
	if len(cert) == 0 {
		s.o.Log.Warn("no certificate file: the iPod gets no certificate")
		return
	}
	n := (len(cert) + s.o.CertSection - 1) / s.o.CertSection
	var steps []step
	for i := 0; i < n; i++ {
		lo, hi := i*s.o.CertSection, min(len(cert), (i+1)*s.o.CertSection)
		args := append([]byte{0x02, 0x00, byte(i), byte(n - 1)}, cert[lo:hi]...) // version 2.0, section, last section
		exp := wantAck(lGeneral, 0x15)
		if i == n-1 {
			exp = wantReply(cid(lGeneral, 0x16))
		}
		steps = append(steps, step{name: fmt.Sprintf("RetDevAuthenticationInfo %d/%d", i, n-1), id: cid(lGeneral, 0x15), args: args, expect: exp})
	}
	s.enqueue(steps...)
	s.identified = true
}

// onChallenge: the private key stays inside the stereo's MFi chip, so the Pi 3 cannot sign.
func (s *stereo) onChallenge(args []byte) {
	s.o.Log.Info("signature challenge", "challenge", hex.EncodeToString(args))
	switch s.o.Signature {
	case "bogus":
		s.after(s.o.SignatureDelay, func() {
			sig := make([]byte, 128)
			for i := range sig {
				sig[i] = byte(0xA5 ^ i)
			}
			s.o.Log.Warn("sending an INVALID 128-byte signature")
			s.send("RetDevAuthenticationSignature", cid(lGeneral, 0x18), sig)
		})
	default:
		s.o.Log.Info("no signing key: no answer, as the stereo during its first 56 s")
	}
}

type statusDoc struct {
	Device     map[string]string `json:"device"`
	InitDone   bool              `json:"init_done"`
	CertSent   bool              `json:"cert_sent"`
	PlayState  string            `json:"play_state"`
	PositionMS uint32            `json:"position_ms"`
	LengthMS   uint32            `json:"length_ms"`
	Track      int32             `json:"track"`
	NumTracks  uint32            `json:"num_tracks"`
	Title      string            `json:"title"`
	Artist     string            `json:"artist"`
	Album      string            `json:"album"`
	IPodName   string            `json:"ipod_name"`
	IPodSW     string            `json:"ipod_software"`
	Tx         int               `json:"packets_sent"`
	Rx         int               `json:"packets_received"`
	RxByName   map[string]int    `json:"received_by_name"`
}

func (s *stereo) status() string {
	state := map[int]string{-1: "unknown", 0: "stopped", 1: "playing", 2: "paused"}[s.playState]
	if state == "" {
		state = fmt.Sprintf("%#x", s.playState)
	}
	b, _ := json.MarshalIndent(statusDoc{
		Device: s.meta, InitDone: s.initDone, CertSent: s.identified, PlayState: state,
		PositionMS: s.pos, LengthMS: s.length, Track: s.track, NumTracks: s.numTracks,
		Title: s.title, Artist: s.artist, Album: s.album, IPodName: s.podName, IPodSW: s.podSW,
		Tx: s.txCount, Rx: s.rxCount, RxByName: s.rxNames,
	}, "", "  ")
	return string(b)
}

const helpText = `commands: status | toggle | stereo-next | play | pause | stop | next | prev | ffwd | rew | endff |
          track <n> | raw <hex of lingo,command,args> | help`

// control runs one command line from the control socket.
func (s *stereo) control(line string) string {
	f := strings.Fields(line)
	if len(f) == 0 {
		return "empty command. " + helpText
	}
	switch f[0] {
	case "status":
		return s.status()
	case "toggle":
		s.pressToggle()
	case "play":
		if s.playState != 1 {
			s.pressToggle()
		}
	case "pause":
		if s.playState == 1 {
			s.pressToggle()
		}
	case "stop":
		s.enqueue(stepPlayControl(0x02))
	case "stereo-next": // the stereo's own next button: SetCurrentPlayingTrack(index + 1), then end-ff/rew
		if s.track >= 0 && s.numTracks > 0 && uint32(s.track) < s.numTracks {
			n := s.track + 1
			if uint32(n) >= s.numTracks {
				n = 0
			}
			s.enqueue(stepSetTrack(n), stepPlayControl(0x07))
		} else { // the iPhone reports an index beyond its track count: an index would jump to a wrong track
			s.enqueue(stepPlayControl(0x08))
		}
	case "next":
		s.enqueue(stepPlayControl(0x08)) // iAP1 PlayControl "Next"
	case "prev":
		s.enqueue(stepPlayControl(0x09)) // iAP1 PlayControl "Previous"
	case "ffwd":
		s.enqueue(stepPlayControl(0x05))
	case "rew":
		s.enqueue(stepPlayControl(0x06))
	case "endff":
		s.enqueue(stepPlayControl(0x07))
	case "track":
		var n int32
		if len(f) < 2 {
			return "usage: track <n>"
		}
		if _, err := fmt.Sscan(f[1], &n); err != nil {
			return "usage: track <n>"
		}
		s.enqueue(stepSetTrack(n))
	case "raw":
		if len(f) < 2 {
			return "usage: raw <hex>"
		}
		p, err := hex.DecodeString(strings.Join(f[1:], ""))
		if err != nil {
			return "bad hex: " + err.Error()
		}
		r, ok := parsePayload(p)
		if !ok {
			return "payload too short"
		}
		s.send("raw", r.id, r.args)
	case "help":
		return helpText
	default:
		return "unknown command. " + helpText
	}
	return "ok"
}
