package main

import (
	"testing"

	"github.com/godbus/dbus/v5"
)

func TestReconnectTargets(t *testing.T) {
	b := &bluezPhone{devices: map[dbus.ObjectPath]*deviceData{
		"/org/bluez/hci0/dev_B": {alias: "paired, trusted, away", paired: true, trusted: true},
		"/org/bluez/hci0/dev_A": {alias: "paired, trusted, away too", paired: true, trusted: true},
		"/org/bluez/hci0/dev_C": {alias: "connected already", paired: true, trusted: true, connected: true},
		"/org/bluez/hci0/dev_D": {alias: "not trusted", paired: true},
		"/org/bluez/hci0/dev_E": {alias: "only seen in a scan"},
	}}
	got := b.reconnectTargets()
	if len(got) != 2 || got[0] != "/org/bluez/hci0/dev_A" || got[1] != "/org/bluez/hci0/dev_B" {
		t.Errorf("targets %v, want dev_A and dev_B in this order", got)
	}
}

func TestKeyLost(t *testing.T) {
	if !keyLost(dbus.Error{Name: "org.bluez.Error.Failed", Body: []any{"br-connection-key-missing"}}) {
		t.Error("a missing key is not recognised")
	}
	for _, err := range []error{nil, dbus.Error{Name: "org.bluez.Error.Failed", Body: []any{"br-connection-page-timeout"}}} {
		if keyLost(err) {
			t.Errorf("%v: not a lost key", err)
		}
	}
}

func TestMatchDevices(t *testing.T) {
	b := &bluezPhone{devices: map[dbus.ObjectPath]*deviceData{
		"/org/bluez/hci0/dev_00_11_22_33_44_55": {alias: "Test Phone", paired: true, trusted: true},
		"/org/bluez/hci0/dev_11_22_33_44_55_66": {alias: "iPad", paired: true},
		"/org/bluez/hci0/dev_AA_BB_CC_DD_EE_FF": {alias: "only seen in a scan"},
	}}
	for spec, want := range map[string]int{
		"all": 2, "00:11:22:33:44:55": 1, "00-11-22-33-44-55": 1, "iPad": 1, "Test Phone": 1, "nobody": 0,
	} {
		if got := b.matchDevices(spec); len(got) != want {
			t.Errorf("%q matched %v, want %d", spec, got, want)
		}
	}
}

func TestDelayTargets(t *testing.T) {
	b := &bluezPhone{transports: map[dbus.ObjectPath]*transportData{
		"/t/idle":      {state: "idle", hasDelay: true},
		"/t/active":    {state: "active", hasDelay: true},
		"/t/no-report": {state: "idle"},
		"/t/known":     {state: "idle", hasDelay: true, delay: 5200},
		"/t/sent":      {state: "idle", hasDelay: true, sent: 5200},
	}}
	if got := b.delayTargets(5200); len(got) != 1 || got[0] != "/t/idle" {
		t.Errorf("targets %v, want only /t/idle", got)
	}
}

func TestBluealsaPCM(t *testing.T) {
	if got := bluealsaPCM("/org/bluez/hci0/dev_00_11_22_33_44_55"); got != "/org/bluealsa/hci0/dev_00_11_22_33_44_55/a2dpsnk/source" {
		t.Errorf("got %s", got)
	}
}
