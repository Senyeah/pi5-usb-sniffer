package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

type usbDev struct {
	Path string // sysfs directory, for example /sys/bus/usb/devices/1-1.2
	Name string
	VID  string
	PID  string
}

func readSys(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(string(b))
}

// findDevice returns the first USB device with this vendor ID, or nil.
func findDevice(sysRoot, vid string) *usbDev {
	ents, _ := os.ReadDir(sysRoot)
	for _, e := range ents {
		name := e.Name()
		if strings.Contains(name, ":") || strings.HasPrefix(name, "usb") {
			continue
		}
		p := filepath.Join(sysRoot, name)
		if readSys(p+"/idVendor") == vid {
			return &usbDev{Path: p, Name: name, VID: vid, PID: readSys(p + "/idProduct")}
		}
	}
	return nil
}

// selectConfig does SET_CONFIGURATION like the stereo does. Linux picks configuration 1 (PTP) by itself.
func selectConfig(d *usbDev, want int) error {
	cur, _ := strconv.Atoi(readSys(d.Path + "/bConfigurationValue"))
	if cur == want {
		return nil
	}
	return os.WriteFile(d.Path+"/bConfigurationValue", []byte(strconv.Itoa(want)), 0o644)
}

// findHIDRaw returns the /dev node of the HID interface in configuration cfg.
func findHIDRaw(d *usbDev, cfg int, devRoot string) (string, error) {
	ifs, _ := filepath.Glob(fmt.Sprintf("%s/%s:%d.*", d.Path, d.Name, cfg))
	for _, ifp := range ifs {
		if readSys(ifp+"/bInterfaceClass") != "03" {
			continue
		}
		raws, _ := filepath.Glob(ifp + "/*/hidraw/hidraw*")
		if len(raws) > 0 {
			return filepath.Join(devRoot, filepath.Base(raws[0])), nil
		}
	}
	return "", fmt.Errorf("no hidraw node under %s configuration %d", d.Path, cfg)
}

const (
	hidiocgrdescsize = 0x80044801 // _IOR('H', 1, int)
	hidiocgrdesc     = 0x90044802 // _IOR('H', 2, struct hidraw_report_descriptor)
)

type hidrawDesc struct {
	Size  uint32
	Value [4096]byte
}

func ioctl(f *os.File, req uintptr, arg unsafe.Pointer) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var errno unix.Errno
	if err := rc.Control(func(fd uintptr) {
		_, _, errno = unix.Syscall(unix.SYS_IOCTL, fd, req, uintptr(arg))
	}); err != nil {
		return err
	}
	if errno != 0 {
		return errno
	}
	return nil
}

func readReportDescriptor(f *os.File) ([]byte, error) {
	var n int32
	if err := ioctl(f, hidiocgrdescsize, unsafe.Pointer(&n)); err != nil {
		return nil, fmt.Errorf("HIDIOCGRDESCSIZE: %w", err)
	}
	if n <= 0 || n > 4096 {
		return nil, fmt.Errorf("report descriptor size %d", n)
	}
	var d hidrawDesc
	d.Size = uint32(n)
	if err := ioctl(f, hidiocgrdesc, unsafe.Pointer(&d)); err != nil {
		return nil, fmt.Errorf("HIDIOCGRDESC: %w", err)
	}
	return append([]byte(nil), d.Value[:n]...), nil
}

// waitBound waits until every interface of the configuration has a driver, or the time is over.
func waitBound(d *usbDev, cfg int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for {
		ifs, _ := filepath.Glob(fmt.Sprintf("%s/%s:%d.*", d.Path, d.Name, cfg))
		bound := len(ifs) > 0
		for _, i := range ifs {
			if _, err := os.Readlink(i + "/driver"); err != nil {
				bound = false
			}
		}
		if bound {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
}
