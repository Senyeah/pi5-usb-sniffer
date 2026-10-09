package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	bluezName    = "org.bluez"
	ifPlayer     = "org.bluez.MediaPlayer1"
	ifDevice     = "org.bluez.Device1"
	ifAdapter    = "org.bluez.Adapter1"
	ifProps      = "org.freedesktop.DBus.Properties"
	ifObjMgr     = "org.freedesktop.DBus.ObjectManager"
	agentPath    = dbus.ObjectPath("/ipod/agent")
	agentCap     = "NoInputNoOutput" // "Just Works" pairing: the Pi has no screen or keys
	pairingSecs  = 600
	adapterPath  = dbus.ObjectPath("/org/bluez/hci0")
	signalBuffer = 128

	// The phone is an A2DP source. The Pi connects its sink profile to this remote service.
	uuidA2DPSource = "0000110a-0000-1000-8000-00805f9b34fb"
)

type playerData struct {
	path    dbus.ObjectPath
	device  dbus.ObjectPath
	status  string
	posMS   uint32
	at      time.Time
	track   track
	shuffle string
	repeat  string
	updated time.Time
}

type deviceData struct {
	alias     string
	connected bool
	paired    bool
	trusted   bool
}

// bluezPhone follows the AVRCP player of the connected phone through BlueZ.
type bluezPhone struct {
	conn    *dbus.Conn
	log     *slog.Logger
	mu      sync.Mutex
	players map[dbus.ObjectPath]*playerData
	devices map[dbus.ObjectPath]*deviceData
	changed chan struct{}
	ready   chan struct{} // closed when the first scan of BlueZ is done
	once    sync.Once
	pairing time.Time // the pairing window ends at this time
}

func newBluezPhone(log *slog.Logger) (*bluezPhone, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, err
	}
	return &bluezPhone{
		conn: conn, log: log,
		players: map[dbus.ObjectPath]*playerData{},
		devices: map[dbus.ObjectPath]*deviceData{},
		changed: make(chan struct{}, 1),
		ready:   make(chan struct{}),
	}, nil
}

func (b *bluezPhone) Changed() <-chan struct{} { return b.changed }

func (b *bluezPhone) notify() {
	select {
	case b.changed <- struct{}{}:
	default:
	}
}

func str(v dbus.Variant) string { s, _ := v.Value().(string); return s }

func u32(v dbus.Variant) uint32 {
	switch n := v.Value().(type) {
	case uint32:
		return n
	case int32:
		return uint32(n)
	}
	return 0
}

func (b *bluezPhone) applyPlayer(path dbus.ObjectPath, props map[string]dbus.Variant) {
	p := b.players[path]
	if p == nil {
		p = &playerData{path: path}
		b.players[path] = p
	}
	now := time.Now()
	for k, v := range props {
		switch k {
		case "Device":
			if d, ok := v.Value().(dbus.ObjectPath); ok {
				p.device = d
			}
		case "Status":
			// Keep the position right across a change of the status.
			p.posMS, p.at = p.positionAt(now), now
			p.status = str(v)
		case "Position":
			p.posMS, p.at = u32(v), now
		case "Shuffle":
			p.shuffle = str(v)
		case "Repeat":
			p.repeat = str(v)
		case "Track":
			if m, ok := v.Value().(map[string]dbus.Variant); ok {
				p.track = track{
					Title: str(m["Title"]), Artist: str(m["Artist"]), Album: str(m["Album"]), Genre: str(m["Genre"]),
					DurationMS: u32(m["Duration"]), Number: u32(m["TrackNumber"]),
				}
			}
		}
	}
	p.updated = now
}

func (p *playerData) positionAt(now time.Time) uint32 {
	if p.status == "playing" && !p.at.IsZero() {
		return p.posMS + uint32(now.Sub(p.at)/time.Millisecond)
	}
	return p.posMS
}

func (b *bluezPhone) applyDevice(path dbus.ObjectPath, props map[string]dbus.Variant) {
	d := b.devices[path]
	if d == nil {
		d = &deviceData{}
		b.devices[path] = d
	}
	was := *d
	for k, v := range props {
		on, _ := v.Value().(bool)
		switch k {
		case "Alias":
			d.alias = str(v)
		case "Connected":
			d.connected = on
		case "Paired":
			d.paired = on
		case "Trusted":
			d.trusted = on
		}
	}
	if d.paired && !was.paired {
		b.log.Info("phone paired", "device", path, "alias", d.alias)
	}
	if d.connected != was.connected {
		b.log.Info("phone connection", "device", path, "alias", d.alias, "connected", d.connected)
	}
}

// Run reads the current objects, then follows the signals until the connection closes.
func (b *bluezPhone) Run() error {
	if err := b.conn.AddMatchSignal(dbus.WithMatchSender(bluezName)); err != nil {
		return err
	}
	ch := make(chan *dbus.Signal, signalBuffer)
	b.conn.Signal(ch)

	var managed map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := b.conn.Object(bluezName, "/").Call(ifObjMgr+".GetManagedObjects", 0).Store(&managed); err != nil {
		return fmt.Errorf("GetManagedObjects: %w", err)
	}
	b.mu.Lock()
	for path, ifaces := range managed {
		b.applyInterfaces(path, ifaces)
	}
	b.mu.Unlock()
	b.once.Do(func() { close(b.ready) })
	b.notify()

	for sig := range ch {
		b.mu.Lock()
		switch sig.Name {
		case ifProps + ".PropertiesChanged":
			if len(sig.Body) >= 2 {
				iface, _ := sig.Body[0].(string)
				props, _ := sig.Body[1].(map[string]dbus.Variant)
				b.applyInterfaces(sig.Path, map[string]map[string]dbus.Variant{iface: props})
			}
		case ifObjMgr + ".InterfacesAdded":
			if len(sig.Body) >= 2 {
				path, _ := sig.Body[0].(dbus.ObjectPath)
				ifaces, _ := sig.Body[1].(map[string]map[string]dbus.Variant)
				b.applyInterfaces(path, ifaces)
			}
		case ifObjMgr + ".InterfacesRemoved":
			if len(sig.Body) >= 2 {
				path, _ := sig.Body[0].(dbus.ObjectPath)
				names, _ := sig.Body[1].([]string)
				for _, n := range names {
					switch n {
					case ifPlayer:
						delete(b.players, path)
					case ifDevice:
						delete(b.devices, path)
					}
				}
			}
		}
		b.mu.Unlock()
		b.notify()
	}
	return errors.New("D-Bus signal channel closed")
}

// applyInterfaces needs b.mu.
func (b *bluezPhone) applyInterfaces(path dbus.ObjectPath, ifaces map[string]map[string]dbus.Variant) {
	if props, ok := ifaces[ifPlayer]; ok {
		b.applyPlayer(path, props)
	}
	if props, ok := ifaces[ifDevice]; ok {
		b.applyDevice(path, props)
		if d := b.devices[path]; d != nil && d.paired && !d.trusted {
			// A trusted phone connects again by itself and is allowed to use the audio service.
			go b.setProp(path, ifDevice, "Trusted", true)
		}
	}
}

func (b *bluezPhone) setProp(path dbus.ObjectPath, iface, name string, v any) error {
	if err := b.conn.Object(bluezName, path).Call(ifProps+".Set", 0, iface, name, dbus.MakeVariant(v)).Err; err != nil {
		return fmt.Errorf("set %s=%v: %v", name, v, err)
	}
	return nil
}

// active picks the player to follow: a playing one, else the latest of a connected phone. It needs b.mu.
func (b *bluezPhone) active() *playerData {
	var list []*playerData
	for _, p := range b.players {
		if d := b.devices[p.device]; d != nil && d.connected {
			list = append(list, p)
		}
	}
	if len(list) == 0 {
		return nil
	}
	sort.Slice(list, func(i, j int) bool {
		pi, pj := list[i].status == "playing", list[j].status == "playing"
		if pi != pj {
			return pi
		}
		return list[i].updated.After(list[j].updated)
	})
	return list[0]
}

func (b *bluezPhone) State() phoneState {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.active()
	if p == nil {
		// A connected phone without a player (it has not started one): still connected.
		for _, d := range b.devices {
			if d.connected {
				return phoneState{Connected: true, Name: d.alias, Status: "stopped"}
			}
		}
		return phoneState{Status: "stopped"}
	}
	name := ""
	if d := b.devices[p.device]; d != nil {
		name = d.alias
	}
	return phoneState{
		Connected: true, Name: name, Status: p.status, PosMS: p.posMS, At: p.at, Track: p.track,
		Shuffle: p.shuffle, Repeat: p.repeat,
	}
}

var playerMethods = map[string]string{
	"play": "Play", "pause": "Pause", "stop": "Stop", "next": "Next", "previous": "Previous",
	"fastforward": "FastForward", "rewind": "Rewind",
}

func (b *bluezPhone) activePath() (dbus.ObjectPath, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	p := b.active()
	if p == nil {
		return "", errors.New("no phone with a media player is connected")
	}
	return p.path, nil
}

func (b *bluezPhone) Control(action string) error {
	method, ok := playerMethods[action]
	if !ok {
		return fmt.Errorf("unknown action %q", action)
	}
	path, err := b.activePath()
	if err != nil {
		return err
	}
	return b.conn.Object(bluezName, path).Call(ifPlayer+"."+method, 0).Err
}

func (b *bluezPhone) SetShuffle(mode string) error {
	path, err := b.activePath()
	if err != nil {
		return err
	}
	return b.setProp(path, ifPlayer, "Shuffle", mode)
}

func (b *bluezPhone) SetRepeat(mode string) error {
	path, err := b.activePath()
	if err != nil {
		return err
	}
	return b.setProp(path, ifPlayer, "Repeat", mode)
}

// Adapter: name, power and the pairing window.

func (b *bluezPhone) SetupAdapter(alias string) error {
	for name, v := range map[string]any{"Powered": true, "Alias": alias, "Pairable": false, "Discoverable": false} {
		if err := b.setProp(adapterPath, ifAdapter, name, v); err != nil {
			return err
		}
	}
	return nil
}

// OpenPairing makes the Pi visible and pairable for secs seconds.
func (b *bluezPhone) OpenPairing(secs uint32) error {
	// An ordered list: the timeouts first, then the switches.
	for _, kv := range []struct {
		name string
		v    any
	}{{"PairableTimeout", secs}, {"DiscoverableTimeout", secs}, {"Pairable", true}, {"Discoverable", true}} {
		if err := b.setProp(adapterPath, ifAdapter, kv.name, kv.v); err != nil {
			return err
		}
	}
	b.mu.Lock()
	b.pairing = time.Now().Add(time.Duration(secs) * time.Second)
	b.mu.Unlock()
	return nil
}

func (b *bluezPhone) pairingOpen() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return time.Now().Before(b.pairing)
}

// HasPairedDevice tells whether a phone is paired already.
func (b *bluezPhone) HasPairedDevice() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, d := range b.devices {
		if d.paired {
			return true
		}
	}
	return false
}

func (b *bluezPhone) Devices() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []string
	for path, d := range b.devices {
		out = append(out, fmt.Sprintf("%s alias=%q paired=%v trusted=%v connected=%v", strings.TrimPrefix(string(path), "/org/bluez/hci0/"), d.alias, d.paired, d.trusted, d.connected))
	}
	sort.Strings(out)
	return out
}

// agent answers the pairing questions of BlueZ. It accepts only while the pairing window is open,
// except for phones that are trusted already.
type agent struct{ b *bluezPhone }

func (a *agent) reject(what string) *dbus.Error {
	a.b.log.Warn("pairing request rejected", "what", what)
	return dbus.NewError("org.bluez.Error.Rejected", []any{what})
}

func (a *agent) Release() *dbus.Error { return nil }

func (a *agent) RequestPinCode(device dbus.ObjectPath) (string, *dbus.Error) {
	if !a.b.pairingOpen() {
		return "", a.reject("RequestPinCode")
	}
	return "0000", nil
}

func (a *agent) DisplayPinCode(device dbus.ObjectPath, pin string) *dbus.Error { return nil }

func (a *agent) RequestPasskey(device dbus.ObjectPath) (uint32, *dbus.Error) {
	if !a.b.pairingOpen() {
		return 0, a.reject("RequestPasskey")
	}
	return 0, nil
}

func (a *agent) DisplayPasskey(device dbus.ObjectPath, passkey uint32, entered uint16) *dbus.Error {
	return nil
}

func (a *agent) RequestConfirmation(device dbus.ObjectPath, passkey uint32) *dbus.Error {
	if !a.b.pairingOpen() {
		return a.reject("RequestConfirmation")
	}
	a.b.log.Info("pairing: confirmed", "device", device, "passkey", passkey)
	return nil
}

func (a *agent) RequestAuthorization(device dbus.ObjectPath) *dbus.Error {
	if !a.b.pairingOpen() {
		return a.reject("RequestAuthorization")
	}
	return nil
}

func (a *agent) AuthorizeService(device dbus.ObjectPath, uuid string) *dbus.Error {
	a.b.mu.Lock()
	d := a.b.devices[device]
	trusted := d != nil && d.trusted
	a.b.mu.Unlock()
	if !trusted && !a.b.pairingOpen() {
		return a.reject("AuthorizeService " + uuid)
	}
	return nil
}

func (a *agent) Cancel() *dbus.Error { return nil }

// RegisterAgent exports the pairing agent and makes it the default one.
func (b *bluezPhone) RegisterAgent() error {
	if err := b.conn.Export(&agent{b: b}, agentPath, "org.bluez.Agent1"); err != nil {
		return err
	}
	mgr := b.conn.Object(bluezName, "/org/bluez")
	if err := mgr.Call("org.bluez.AgentManager1.RegisterAgent", 0, agentPath, agentCap).Err; err != nil {
		return fmt.Errorf("RegisterAgent: %w", err)
	}
	return mgr.Call("org.bluez.AgentManager1.RequestDefaultAgent", 0, agentPath).Err
}

// reconnectTargets lists the phones that are paired and trusted but not connected.
func (b *bluezPhone) reconnectTargets() []dbus.ObjectPath {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out []dbus.ObjectPath
	for path, d := range b.devices {
		if d.paired && d.trusted && !d.connected {
			out = append(out, path)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Reconnect connects to the paired phones by itself, as a car kit does. iPhones do not always connect to
// an audio device that was off (BlueZ only retries after a lost link). It tries every few seconds,
// slower after a minute without success, for example when the phone is out of range.
func (b *bluezPhone) Reconnect(ctx context.Context, every time.Duration) {
	start := time.Now()
	failing := map[dbus.ObjectPath]bool{}
	for ctx.Err() == nil {
		wait := every
		if time.Since(start) > time.Minute {
			wait = 3 * every
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		for _, path := range b.reconnectTargets() {
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			// Ask for the classic A2DP connection. Device1.Connect() lets BlueZ pick a bearer: for a dual-mode phone
			// after a restart it scans for Low Energy and stays "In Progress" for good, without paging the phone.
			err := b.conn.Object(bluezName, path).CallWithContext(cctx, ifDevice+".ConnectProfile", 0, uuidA2DPSource).Err
			if dbusErr, ok := err.(dbus.Error); ok && dbusErr.Name == "org.bluez.Error.AlreadyConnected" {
				err = nil
			}
			cancel()
			if err == nil {
				b.log.Info("connected to the phone", "device", path)
				delete(failing, path)
				continue
			}
			if !failing[path] { // one line for a phone that is away, not one per try
				b.log.Info("cannot connect to the phone yet, trying again", "device", path, "err", err)
				failing[path] = true
			}
		}
	}
}
