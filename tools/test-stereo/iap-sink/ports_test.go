package main

import "testing"

func TestPlanClaimsSkipsEthernetChip(t *testing.T) {
	root := t.TempDir()
	// Root hub: never claimed.
	write(t, root+"/usb1/bDeviceClass", "09\n")
	write(t, root+"/usb1/maxchild", "1\n")
	// The Pi 3 B hub (LAN9514): port 1 is the Ethernet chip, ports 2 to 5 are the USB-A sockets.
	write(t, root+"/1-1/bDeviceClass", "09\n")
	write(t, root+"/1-1/maxchild", "5\n")
	write(t, root+"/1-1/busnum", "1\n")
	write(t, root+"/1-1/devnum", "2\n")
	write(t, root+"/1-1.1/idVendor", "0424\n")
	write(t, root+"/1-1.4/idVendor", "05ac\n") // an Apple device may sit on a claimed port
	// A non-hub device is ignored.
	write(t, root+"/1-1.4/bDeviceClass", "00\n")

	got := planClaims(root, "/dev")
	var ports []int
	for _, c := range got {
		if c.hub != "1-1" || c.node != "/dev/bus/usb/001/002" {
			t.Errorf("unexpected target %+v", c)
		}
		ports = append(ports, int(c.port))
	}
	if !eq(ports, []int{2, 3, 4, 5}) {
		t.Errorf("ports %v, want [2 3 4 5]", ports)
	}
}

func TestSplitPort(t *testing.T) {
	for in, want := range map[string]struct {
		hub  string
		port int
		ok   bool
	}{
		"1-1.4":   {"1-1", 4, true},
		"1-1.3.2": {"1-1.3", 2, true},
		"1-1":     {"", 0, false},
	} {
		hub, port, ok := splitPort(in)
		if hub != want.hub || port != want.port || ok != want.ok {
			t.Errorf("%s: got %q %d %v", in, hub, port, ok)
		}
	}
}
