package main

import (
	"strings"
	"time"
)

// track is the metadata of the Bluetooth phone's current track (AVRCP).
type track struct {
	Title, Artist, Album, Genre string
	DurationMS                  uint32
	Number                      uint32
}

// phoneState is a snapshot of the phone's AVRCP player.
type phoneState struct {
	Connected bool
	Name      string // the phone's Bluetooth name
	Status    string // playing, paused, stopped, forward-seek, reverse-seek, error
	PosMS     uint32 // position at time At
	At        time.Time
	Track     track
	Shuffle   string // off, alltracks, group
	Repeat    string // off, singletrack, alltracks, group
}

// position extrapolates the position while the phone plays.
func (s phoneState) position(now time.Time) uint32 {
	pos := s.PosMS
	if s.Status == "playing" && !s.At.IsZero() && now.After(s.At) {
		pos += uint32(now.Sub(s.At) / time.Millisecond)
	}
	if d := s.Track.DurationMS; d > 0 && pos > d {
		pos = d
	}
	return pos
}

// iapPlayState maps the AVRCP status to the iAP1 play state: 0 stopped, 1 playing, 2 paused.
func iapPlayState(status string) byte {
	switch strings.ToLower(status) {
	case "playing", "forward-seek", "reverse-seek":
		return 1
	case "paused":
		return 2
	}
	return 0
}

// phone is the Bluetooth side. The real one talks to BlueZ. Tests use a fake.
type phone interface {
	State() phoneState
	// Control sends an AVRCP command: play, pause, stop, next, previous, fastforward, rewind.
	Control(action string) error
	SetShuffle(mode string) error
	SetRepeat(mode string) error
	// Changed signals a change of the state. Signals are merged.
	Changed() <-chan struct{}
}
