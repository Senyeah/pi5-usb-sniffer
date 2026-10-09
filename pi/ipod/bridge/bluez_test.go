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
