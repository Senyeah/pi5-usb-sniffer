package main

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeSysfs builds the parts of /sys/bus/usb/devices that the code reads.
func fakeSysfs(t *testing.T) (root string) {
	root = t.TempDir()
	write(t, root+"/usb1/idVendor", "1d6b\n")
	write(t, root+"/1-1/idVendor", "0424\n")
	write(t, root+"/1-1.2/idVendor", "05ac\n")
	write(t, root+"/1-1.2/idProduct", "12ab\n")
	write(t, root+"/1-1.2/bConfigurationValue", "1\n")
	write(t, root+"/1-1.2/speed", "12\n")
	// Configuration 1 has no HID interface. Configuration 2 has audio, audio and HID.
	write(t, root+"/1-1.2/1-1.2:1.0/bInterfaceClass", "06\n")
	write(t, root+"/1-1.2/1-1.2:2.0/bInterfaceClass", "01\n")
	write(t, root+"/1-1.2/1-1.2:2.2/bInterfaceClass", "03\n")
	write(t, root+"/1-1.2/1-1.2:2.2/0003:05AC:12AB.0001/hidraw/hidraw3/dev", "247:3\n")
	return root
}

func TestFindDevice(t *testing.T) {
	root := fakeSysfs(t)
	d := findDevice(root, "05ac")
	if d == nil || d.Name != "1-1.2" || d.PID != "12ab" {
		t.Fatalf("found %+v", d)
	}
	if findDevice(root, "dead") != nil {
		t.Error("found a device that does not exist")
	}
}

func TestSelectConfigAndHIDRaw(t *testing.T) {
	root := fakeSysfs(t)
	d := findDevice(root, "05ac")
	if err := selectConfig(d, 2); err != nil {
		t.Fatal(err)
	}
	if got := readSys(d.Path + "/bConfigurationValue"); got != "2" {
		t.Errorf("configuration is %q, want 2", got)
	}
	node, err := findHIDRaw(d, 2, "/dev")
	if err != nil || node != "/dev/hidraw3" {
		t.Errorf("node %q err %v", node, err)
	}
	if _, err := findHIDRaw(d, 1, "/dev"); err == nil {
		t.Error("configuration 1 has no HID interface, but a node was found")
	}
}
