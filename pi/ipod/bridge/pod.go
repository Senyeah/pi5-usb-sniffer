package main

import (
	"crypto/rand"
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

// options tune the iPod. The defaults copy the iPad of session car-10.
type options struct {
	Name        string        // iPod name. Empty: the phone's Bluetooth name.
	Software    [3]byte       // iPod software version
	AuthTimeout time.Duration // pass the authentication when no signature came
	NotifyEvery time.Duration // track time notifications
	TrackSettle time.Duration // the phone sends the data of a new track in steps: wait until it is complete
	AttrEvery   time.Duration // TrackNewAudioAttributes repeat
	AttrTries   int           // TrackNewAudioAttributes repeats per start, until the stereo acks
	SampleRate  uint32
	Trace       *tracer
	Log         *slog.Logger
}

func defaultOptions() options {
	return options{
		Software:    [3]byte{26, 6, 1},
		AuthTimeout: 70 * time.Second,
		NotifyEvery: 500 * time.Millisecond,
		TrackSettle: 800 * time.Millisecond,
		AttrEvery:   500 * time.Millisecond,
		AttrTries:   40,
		SampleRate:  44100,
		Log:         slog.Default(),
	}
}

func ext(cmd uint16) ipod.LingoCmdID { return cid(lExt, cmd) }

func be32(n uint32) []byte { return binary.BigEndian.AppendUint32(nil, n) }

func cstr(s string) []byte { return append([]byte(s), 0) }

// Fixed replies, copied from the iPad in session car-10.
var (
	colorDisplayLimits = []byte{0x00, 0xA6, 0x00, 0x4C, 0x02, 0x00, 0xA6, 0x00, 0x4C, 0x03} // 166x76, formats 2 and 3
	chapterInfoNone    = []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x00, 0x00, 0x00}             // no chapters
	protocolVersions   = map[byte][2]byte{lGeneral: {1, 9}, lExt: {1, 14}, lAudio: {1, 2}}
)

// The stereo wraps "next" to index 0 at the last track count it read, and that count is often older than the
// index. A large list with the index in the middle keeps both ends away.
const (
	virtualCount = 1000
	virtualStart = 500
	maxSkips     = 5 // one SetCurrentPlayingTrack moves at most this many tracks
	skipGap      = 300 * time.Millisecond
	waitingText  = "Waiting"
)

// waitingTrack plays (silence) while no phone has track data: a stopped, empty iPod makes the stereo show "Unsupported".
var waitingTrack = track{Title: waitingText, Artist: waitingText, Album: waitingText, Genre: waitingText, DurationMS: 3600000}

// pod is the iPod: it answers the stereo and takes its data from the phone. One goroutine runs it through ev.
type pod struct {
	o    options
	fw   ipod.FrameWriter
	ph   phone
	ev   chan func()
	done chan struct{}
	stop func(error)

	notify      bool // the stereo asked for play status change notifications
	attrAcked   bool
	attrEver    bool // the stereo acked the attributes once in this session
	attrTries   int
	attrLoop    bool // a repeat of TrackNewAudioAttributes is scheduled
	wasPlaying  bool
	authPassed  bool
	identified  bool
	certBytes   int
	lastErr     string
	txCount     int
	rxCount     int
	rxNames     map[string]int
	lastCommand string

	// A virtual playlist: idx counts the track changes, from virtualStart.
	idx       int32
	cur       track
	hist      []track // hist[0] is the previous track
	pendingTo int32   // the index that a skip goes to, 0: none
	pendingAt time.Time
	pressIdx  int32 // the index when the stereo pressed next or previous
	pressAt   time.Time
	candidate track // a track change that is not settled yet
	candSince time.Time

	// While no phone has track data, the stereo plays waitingTrack. Its play state is the stereo's.
	waitPlaying bool
	inWaiting   bool
	waitPos     uint32
	waitAt      time.Time
	phoneReady  bool // a phone with a player was there at the last check
}

func newPod(o options, fw ipod.FrameWriter, ph phone, stop func(error)) *pod {
	return &pod{
		o: o, fw: fw, ph: ph, stop: stop,
		ev:          make(chan func(), 256),
		done:        make(chan struct{}),
		rxNames:     map[string]int{},
		idx:         virtualStart,
		waitPlaying: true,
	}
}

func (p *pod) post(f func()) {
	select {
	case p.ev <- f:
	case <-p.done:
	}
}

func (p *pod) after(d time.Duration, f func()) *time.Timer {
	return time.AfterFunc(d, func() { p.post(f) })
}

func (p *pod) loop() {
	for {
		select {
		case f := <-p.ev:
			f()
		case <-p.done:
			return
		}
	}
}

// call runs f on the loop and waits for the answer.
func (p *pod) call(f func() string) string {
	out := make(chan string, 1)
	select {
	case p.ev <- func() { out <- f() }:
	case <-p.done:
		return "session ended"
	}
	select {
	case r := <-out:
		return r
	case <-p.done:
		return "session ended"
	}
}

// start follows the phone and sends the track time ticks.
func (p *pod) start() {
	p.post(func() {
		st := p.ph.State()
		p.phoneReady = st.Connected && st.Player
		p.cur = p.view().Track // what the phone plays when the stereo connects is no change
		p.after(p.o.NotifyEvery, p.tick)
	})
	go func() {
		for {
			select {
			case <-p.ph.Changed():
				p.post(p.syncPhone)
			case <-p.done:
				return
			}
		}
	}()
}

func (p *pod) send(name string, id ipod.LingoCmdID, args []byte) {
	frame, err := encodeFrame(id, args)
	if err != nil {
		p.o.Log.Error("encode", "cmd", name, "err", err)
		return
	}
	p.txCount++
	p.o.Trace.write("D>A", name, frame)
	if strings.Contains(name, "time ms") {
		p.o.Log.Debug("D>A", "cmd", name)
	} else {
		p.o.Log.Info("D>A", "cmd", name, "args", hex.EncodeToString(args))
	}
	if err := p.fw.WriteFrame(frame); err != nil {
		if errors.Is(err, syscall.ENODEV) || errors.Is(err, syscall.ESHUTDOWN) || errors.Is(err, syscall.EBADF) ||
			errors.Is(err, io.EOF) || errors.Is(err, io.ErrClosedPipe) {
			p.stop(fmt.Errorf("write %s: %w", name, err))
		} else {
			p.o.Log.Warn("write failed", "cmd", name, "err", err)
		}
	}
}

func (p *pod) ackGeneral(cmd byte, status byte) {
	p.send("iPodAck", cid(lGeneral, 0x02), []byte{status, cmd})
}

func (p *pod) ackExt(cmd uint16, status byte) {
	p.send("iPodAck ExtendedInterface", ext(0x0001), []byte{status, byte(cmd >> 8), byte(cmd)})
}

const (
	ackOK      = 0x00
	ackFailed  = 0x02
	ackUnknown = 0x05
)

func (p *pod) name() string {
	if p.o.Name != "" {
		return p.o.Name
	}
	if n := p.ph.State().Name; n != "" {
		return n
	}
	return "iPod"
}

func waiting(st phoneState) bool { return !st.Connected || st.Track == (track{}) }

// view is what the stereo sees: the phone, or waitingTrack.
func (p *pod) view() phoneState {
	st := p.ph.State()
	if !waiting(st) {
		p.inWaiting = false
		return st
	}
	now := time.Now()
	if !p.inWaiting {
		p.inWaiting, p.waitPos, p.waitAt = true, 0, now
	}
	v := phoneState{Connected: st.Connected, Name: st.Name, Status: "paused", PosMS: p.waitPos, At: p.waitAt, Track: waitingTrack}
	if p.waitPlaying {
		v.Status = "playing"
		if v.position(now) >= waitingTrack.DurationMS {
			p.waitPos, p.waitAt = 0, now
			v.PosMS, v.At = 0, now
		}
	}
	return v
}

// setPlaying keeps the stereo's play state: the state of waitingTrack, and what a phone gets when it connects.
func (p *pod) setPlaying(on bool) {
	if p.inWaiting && on != p.waitPlaying {
		now := time.Now()
		p.waitPos, p.waitAt = p.view().position(now), now
	}
	p.waitPlaying = on
}

// followPhone starts a phone that comes while the stereo plays, and else follows the phone's play state.
func (p *pod) followPhone() {
	st := p.ph.State()
	ready := st.Connected && st.Player
	playing := iapPlayState(st.Status) == 1
	switch {
	case ready && !p.phoneReady:
		if p.waitPlaying && !playing {
			p.o.Log.Info("phone connected while the stereo plays: play")
			go p.logErr("play", p.ph.Control("play"))
		}
	case ready:
		p.waitPlaying = playing
	}
	p.phoneReady = ready
}

// syncPhone follows track changes of the phone. The phone sends the data of a new track in two or more
// steps (the album first, the title later), so a change counts after the data has been the same for TrackSettle.
func (p *pod) syncPhone() {
	p.followPhone()
	st := p.view()
	if st.Track == p.cur {
		p.candidate = track{}
		return
	}
	if st.Track != p.candidate {
		p.candidate, p.candSince = st.Track, time.Now()
		p.after(p.o.TrackSettle+10*time.Millisecond, p.syncPhone)
		return
	}
	if time.Since(p.candSince) < p.o.TrackSettle {
		return
	}
	p.candidate = track{}
	to := p.idx + 1
	if p.pendingTo != 0 && time.Since(p.pendingAt) < 8*time.Second {
		to = p.pendingTo
	}
	p.pendingTo = 0
	if to == p.idx { // a new track needs a new index, or the stereo keeps the old data
		to++
	}
	to = min(max(to, 1), virtualCount-2)
	if to > p.idx {
		p.hist = append([]track{p.cur}, p.hist...)
		if len(p.hist) > 4 {
			p.hist = p.hist[:4]
		}
	} else if len(p.hist) > 0 {
		p.hist = p.hist[1:]
	}
	p.cur, p.idx = st.Track, to
	p.o.Log.Info("track changed", "index", p.idx, "title", p.cur.Title, "artist", p.cur.Artist, "album", p.cur.Album)
	if p.notify {
		p.send("PlayStatusChangeNotification track index", ext(0x27), append([]byte{0x01}, be32(uint32(p.idx))...))
	}
	p.restartAttributes()
}

func (p *pod) tick() {
	p.syncPhone()
	st := p.view()
	if playing := iapPlayState(st.Status) == 1; playing != p.wasPlaying {
		p.wasPlaying = playing
		if playing {
			p.restartAttributes()
		}
	}
	if p.notify && iapPlayState(st.Status) == 1 {
		p.send("PlayStatusChangeNotification track time ms", ext(0x27), append([]byte{0x04}, be32(st.position(time.Now()))...))
	}
	p.after(p.o.NotifyEvery, p.tick)
}

func (p *pod) trackAt(n int32) track {
	switch {
	case p.cur == waitingTrack || n < 0 || n == p.idx:
		return p.cur
	case n == p.idx-1 && len(p.hist) > 0:
		return p.hist[0]
	}
	return track{}
}

func (p *pod) count() uint32 { return virtualCount }

// onRx handles one packet from the stereo.
func (p *pod) onRx(r rx) {
	p.rxCount++
	name := r.id.GoString()
	p.rxNames[name]++
	p.lastCommand = name
	p.o.Trace.write("A>D", name, r.raw)
	if r.id == ext(0x1C) || r.id == ext(0x1E) {
		p.o.Log.Debug("A>D", "cmd", name, "args", hex.EncodeToString(r.args))
	} else {
		p.o.Log.Info("A>D", "cmd", name, "args", hex.EncodeToString(r.args))
	}
	switch r.id.LingoID() {
	case lGeneral:
		p.onGeneral(r)
	case lExt:
		p.onExt(r)
	case lAudio:
		p.onAudio(r)
	}
}

func (p *pod) onGeneral(r rx) {
	cmd := byte(r.id.CmdID())
	a := r.args
	switch cmd {
	case 0x13: // IdentifyDeviceLingoes
		p.identified, p.attrAcked, p.attrTries, p.authPassed, p.certBytes = true, false, 0, false, 0
		p.ackGeneral(0x13, ackOK)
		p.send("GetDevAuthenticationInfo", cid(lGeneral, 0x14), nil)
	case 0x15: // RetDevAuthenticationInfo: version, section, last section, certificate data
		if len(a) < 4 {
			p.ackGeneral(cmd, ackFailed)
			return
		}
		p.certBytes += len(a) - 4
		if a[2] != a[3] {
			p.ackGeneral(cmd, ackOK)
			return
		}
		p.afterCertificate()
	case 0x18: // RetDevAuthenticationSignature. The Pi cannot check it: it has no Apple key.
		p.passAuthentication("signature received")
	case 0x28: // RetAccessoryInfo
	case 0x0F: // RequestLingoProtocolVersion
		if len(a) < 1 {
			p.ackGeneral(cmd, ackFailed)
			return
		}
		v, ok := protocolVersions[a[0]]
		if !ok {
			v = [2]byte{1, 0}
		}
		p.send("ReturnLingoProtocolVersion", cid(lGeneral, 0x10), []byte{a[0], v[0], v[1]})
	case 0x09: // RequestiPodSoftwareVersion
		p.send("ReturniPodSoftwareVersion", cid(lGeneral, 0x0A), p.o.Software[:])
	case 0x05: // EnterRemoteUIMode: the stereo's "extended interface mode"
		p.send("iPodAck pending", cid(lGeneral, 0x02), append([]byte{0x06, cmd}, be32(3000)...))
		p.ackGeneral(cmd, ackOK)
	case 0x07: // RequestiPodName
		p.send("ReturniPodName", cid(lGeneral, 0x08), cstr(p.name()))
	default:
		p.ackGeneral(cmd, ackUnknown)
	}
}

// afterCertificate runs when the last section of the stereo's certificate is in. The Pi does not verify it.
func (p *pod) afterCertificate() {
	p.send("AckDevAuthenticationInfo", cid(lGeneral, 0x16), []byte{0x00})
	p.send("GetAccessorySampleRateCaps", cid(lAudio, 0x02), nil)
	p.send("GetAccessoryInfo", cid(lGeneral, 0x27), []byte{0x00})
	challenge := make([]byte, 21)
	rand.Read(challenge[:20])
	challenge[20] = 0x01
	p.send("GetDevAuthenticationSignature", cid(lGeneral, 0x17), challenge)
	p.after(20*time.Millisecond, p.sendAttributes)
	p.after(p.o.AuthTimeout, func() { p.passAuthentication("no signature in " + p.o.AuthTimeout.String()) })
}

func (p *pod) passAuthentication(why string) {
	if p.authPassed {
		return
	}
	p.authPassed = true
	p.o.Log.Info("authentication passed", "why", why)
	p.send("AckDevAuthenticationStatus", cid(lGeneral, 0x19), []byte{0x00})
}

// sendAttributes repeats TrackNewAudioAttributes until the stereo acks it, as the iPad does.
func (p *pod) sendAttributes() {
	if p.attrAcked || p.attrTries >= p.o.AttrTries {
		p.attrLoop = false
		return
	}
	p.attrTries++
	args := append(append(be32(p.o.SampleRate), be32(0)...), be32(0)...) // sample rate, sound check, volume adjustment
	p.send("TrackNewAudioAttributes", cid(lAudio, 0x04), args)
	p.attrLoop = true
	p.after(p.o.AttrEvery, p.sendAttributes)
}

// restartAttributes sends the attributes again at a new track or a new start of play, until the stereo has acked
// once. The car stereo acks once per connection; more repeats after that may disturb its audio.
func (p *pod) restartAttributes() {
	if !p.identified || p.attrLoop || p.attrEver {
		return
	}
	p.attrAcked, p.attrTries = false, 0
	p.sendAttributes()
}

func (p *pod) onAudio(r rx) {
	switch r.id.CmdID() {
	case 0x00: // AccessoryAck: status, command
		if len(r.args) >= 2 && r.args[1] == 0x04 {
			p.attrAcked, p.attrEver = true, true
		}
	}
}

func (p *pod) onExt(r rx) {
	cmd := r.id.CmdID()
	a := r.args
	st := p.view()
	switch cmd {
	case 0x39: // GetColorDisplayImageLimits
		p.send("ReturnColorDisplayImageLimits", ext(0x3A), colorDisplayLimits)
	case 0x16: // ResetDBSelection
		p.ackExt(cmd, ackOK)
	case 0x2F: // GetRepeat
		p.send("ReturnRepeat", ext(0x30), []byte{repeatToIAP(st.Repeat)})
	case 0x31: // SetRepeat
		if len(a) >= 1 {
			go p.logErr("SetRepeat", p.ph.SetRepeat(repeatFromIAP(a[0])))
		}
		p.ackExt(cmd, ackOK)
	case 0x2C: // GetShuffle
		p.send("ReturnShuffle", ext(0x2D), []byte{shuffleToIAP(st.Shuffle)})
	case 0x2E: // SetShuffle
		if len(a) >= 1 {
			go p.logErr("SetShuffle", p.ph.SetShuffle(shuffleFromIAP(a[0])))
		}
		p.ackExt(cmd, ackOK)
	case 0x1C: // GetPlayStatus: length, position, state
		pos := st.position(time.Now())
		p.send("ReturnPlayStatus", ext(0x1D), append(append(be32(st.Track.DurationMS), be32(pos)...), iapPlayState(st.Status)))
	case 0x18: // GetNumberCategorizedDBRecords: category 5 is track
		n := uint32(0)
		if len(a) >= 1 && a[0] == 0x05 {
			n = p.count()
		}
		p.send("ReturnNumberCategorizedDBRecords", ext(0x19), be32(n))
	case 0x26: // SetPlayStatusChangeNotification: one byte, or a 4-byte mask
		p.notify = false
		for _, b := range a {
			if b != 0 {
				p.notify = true
			}
		}
		p.ackExt(cmd, ackOK)
	case 0x09: // GetAudiobookSpeed
		p.send("ReturnAudiobookSpeed", ext(0x0A), []byte{0x00})
	case 0x1E: // GetCurrentPlayingTrackIndex
		p.send("ReturnCurrentPlayingTrackIndex", ext(0x1F), be32(uint32(p.idx)))
	case 0x35: // GetNumPlayingTracks
		p.send("ReturnNumPlayingTracks", ext(0x36), be32(p.count()))
	case 0x14: // RequestiPodName
		p.send("ReturniPodName", ext(0x15), cstr(p.name()))
	case 0x28: // PlayCurrentSelection: the stereo starts play like this when the iPod is stopped
		p.setPlaying(true)
		if ph := p.ph.State(); ph.Connected && iapPlayState(ph.Status) != 1 {
			go p.logErr("play", p.ph.Control("play"))
		}
		p.ackExt(cmd, ackOK)
	case 0x29: // PlayControl
		if len(a) < 1 {
			p.ackExt(cmd, ackFailed)
			return
		}
		p.ackExt(cmd, p.playControl(a[0]))
	case 0x02: // GetCurrentPlayingTrackChapterInfo
		p.send("ReturnCurrentPlayingTrackChapterInfo", ext(0x03), chapterInfoNone)
	case 0x0C: // GetIndexedPlayingTrackInfo: type, track, chapter
		p.trackInfo(a, st)
	case 0x20, 0x22, 0x24: // title, artist and album of a track
		if len(a) < 4 {
			p.ackExt(cmd, ackFailed)
			return
		}
		t := p.trackAt(int32(binary.BigEndian.Uint32(a)))
		s := map[uint16]struct {
			name  string
			value string
		}{0x20: {"ReturnIndexedPlayingTrackTitle", t.Title}, 0x22: {"ReturnIndexedPlayingTrackArtistName", t.Artist}, 0x24: {"ReturnIndexedPlayingTrackAlbumName", t.Album}}[cmd]
		p.send(s.name, ext(cmd+1), cstr(s.value))
	case 0x37: // SetCurrentPlayingTrack: the stereo's next and previous buttons, as an index
		if len(a) < 4 {
			p.ackExt(cmd, ackFailed)
			return
		}
		p.setTrack(int32(binary.BigEndian.Uint32(a)))
		p.ackExt(cmd, ackOK)
	default:
		p.ackExt(cmd, ackUnknown)
	}
}

func (p *pod) logErr(what string, err error) {
	if err != nil {
		p.o.Log.Warn("phone command failed", "cmd", what, "err", err)
	}
}

// stepsTo is the number of tracks from index from to index n, negative for back. The stereo wraps at the end
// of its list, so the short way round counts. At most maxSkips.
func stepsTo(from, n int32) int32 {
	d := n - from
	if d > virtualCount/2 {
		d -= virtualCount
	} else if d < -virtualCount/2 {
		d += virtualCount
	}
	return max(min(d, maxSkips), -maxSkips)
}

// setTrack goes to the index that the stereo asks for, with next or previous commands to the phone.
func (p *pod) setTrack(n int32) {
	if n == p.pressIdx && time.Since(p.pressAt) < 3*time.Second {
		return // the back button sends "previous", then the index it had: the skip is under way already
	}
	if d := stepsTo(p.idx, n); d != 0 && p.ph.State().Connected {
		p.skip(d)
	}
}

// skip asks the phone for n tracks forward, or back for n < 0. The track change comes back from the phone.
func (p *pod) skip(n int32) {
	action, steps := "next", n
	if n < 0 {
		action, steps = "previous", -n
	}
	p.pendingTo, p.pendingAt = p.idx+n, time.Now()
	go func() {
		for i := int32(0); i < steps; i++ {
			if i > 0 {
				time.Sleep(skipGap)
			}
			if err := p.ph.Control(action); err != nil {
				p.logErr(action, err)
				return
			}
		}
	}()
}

func (p *pod) playControl(action byte) byte {
	playing := iapPlayState(p.view().Status) == 1 // as the stereo sees it
	switch action {
	case 0x01:
		p.setPlaying(!playing)
	case 0x02, 0x0B:
		p.setPlaying(false)
	case 0x0A:
		p.setPlaying(true)
	}
	st := p.ph.State()
	if !st.Connected { // an error reply makes the stereo show "ERROR": waitingTrack follows the stereo instead
		return ackOK
	}
	run := func(a string) { go p.logErr(a, p.ph.Control(a)) }
	switch action {
	case 0x01: // toggle
		if iapPlayState(st.Status) == 1 {
			run("pause")
		} else {
			run("play")
		}
	case 0x02:
		run("stop")
	case 0x03, 0x08:
		p.pressIdx, p.pressAt = p.idx, time.Now()
		p.skip(1)
	case 0x04, 0x09:
		p.pressIdx, p.pressAt = p.idx, time.Now()
		p.skip(-1)
	case 0x05:
		run("fastforward")
	case 0x06:
		run("rewind")
	case 0x07, 0x0C, 0x0D: // end fast forward/rewind, chapters: nothing to do
	case 0x0A:
		run("play")
	case 0x0B:
		run("pause")
	default:
		return ackFailed
	}
	return ackOK
}

// trackInfo answers GetIndexedPlayingTrackInfo. The stereo asks for the current track's length with track index 0.
func (p *pod) trackInfo(a []byte, st phoneState) {
	if len(a) < 7 {
		p.ackExt(0x0C, ackFailed)
		return
	}
	kind := a[0]
	t := p.trackAt(int32(binary.BigEndian.Uint32(a[1:5])))
	switch kind {
	case 0x00: // capabilities, length, chapters
		args := append([]byte{kind}, be32(0x00000004)...)
		args = append(args, be32(p.cur.DurationMS)...)
		args = append(args, 0x00, 0x00)
		p.send("ReturnIndexedPlayingTrackInfo capabilities", ext(0x0D), args)
	case 0x05: // genre
		p.send("ReturnIndexedPlayingTrackInfo genre", ext(0x0D), append([]byte{kind}, cstr(t.Genre)...))
	case 0x06: // composer
		c := ""
		if t == waitingTrack {
			c = waitingText
		}
		p.send("ReturnIndexedPlayingTrackInfo composer", ext(0x0D), append([]byte{kind}, cstr(c)...))
	default:
		p.ackExt(0x0C, ackFailed)
	}
}

func shuffleToIAP(s string) byte {
	switch s {
	case "alltracks":
		return 1
	case "group":
		return 2
	}
	return 0
}

func shuffleFromIAP(b byte) string {
	switch b {
	case 1:
		return "alltracks"
	case 2:
		return "group"
	}
	return "off"
}

func repeatToIAP(s string) byte {
	switch s {
	case "singletrack":
		return 1
	case "alltracks", "group":
		return 2
	}
	return 0
}

func repeatFromIAP(b byte) string {
	switch b {
	case 1:
		return "singletrack"
	case 2:
		return "alltracks"
	}
	return "off"
}

type podStatus struct {
	Phone      phoneState     `json:"phone"`
	Sees       phoneState     `json:"stereo_sees"`
	Index      int32          `json:"index"`
	NumTracks  uint32         `json:"num_tracks"`
	Notify     bool           `json:"notifications"`
	Identified bool           `json:"stereo_identified"`
	AuthPassed bool           `json:"authentication_passed"`
	CertBytes  int            `json:"stereo_certificate_bytes"`
	AttrAcked  bool           `json:"audio_attributes_acked"`
	Tx         int            `json:"packets_sent"`
	Rx         int            `json:"packets_received"`
	RxByName   map[string]int `json:"received_by_name"`
}

func (p *pod) status() string {
	b, _ := json.MarshalIndent(podStatus{
		Phone: p.ph.State(), Sees: p.view(), Index: p.idx, NumTracks: p.count(), Notify: p.notify,
		Identified: p.identified, AuthPassed: p.authPassed, CertBytes: p.certBytes, AttrAcked: p.attrAcked,
		Tx: p.txCount, Rx: p.rxCount, RxByName: p.rxNames,
	}, "", "  ")
	return string(b)
}
