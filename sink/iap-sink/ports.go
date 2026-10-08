package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// A claimed hub port stops the kernel from configuring the device on it (usb_device_is_owned).
// Linux would send SET_CONFIGURATION 0 or 1 at once. The stereo sent only configuration 2.
// A claim also stops interface drivers (usbhid, snd-usb-audio) from binding. So the session
// releases the port after enumeration and before it sets configuration 2, and claims it again at the end.
const (
	usbdevfsClaimPort   = 0x80045518 // _IOR('U', 24, unsigned int)
	usbdevfsReleasePort = 0x80045519 // _IOR('U', 25, unsigned int)
)

type claimTarget struct {
	hub  string // sysfs name of the hub, for example 1-1
	node string // usbfs node of the hub
	port uint32
}

// planClaims lists the ports to claim: every port of every external hub, except ports with a non-Apple device (the Ethernet chip).
func planClaims(sysRoot, devRoot string) []claimTarget {
	var out []claimTarget
	ents, _ := os.ReadDir(sysRoot)
	for _, e := range ents {
		name := e.Name()
		if strings.Contains(name, ":") || strings.HasPrefix(name, "usb") { // skip interfaces and root hubs
			continue
		}
		p := filepath.Join(sysRoot, name)
		if readSys(p+"/bDeviceClass") != "09" {
			continue
		}
		ports, _ := strconv.Atoi(readSys(p + "/maxchild"))
		bus, _ := strconv.Atoi(readSys(p + "/busnum"))
		dev, _ := strconv.Atoi(readSys(p + "/devnum"))
		node := filepath.Join(devRoot, "bus/usb", fmt.Sprintf("%03d/%03d", bus, dev))
		for port := 1; port <= ports; port++ {
			child := fmt.Sprintf("%s/%s.%d", sysRoot, name, port)
			if v := readSys(child + "/idVendor"); v != "" && v != "05ac" {
				continue
			}
			out = append(out, claimTarget{hub: name, node: node, port: uint32(port)})
		}
	}
	return out
}

// portClaimer keeps the hub nodes open. The kernel releases the claims when they close.
type portClaimer struct {
	sysRoot, devRoot string
	files            map[string]*os.File // hub node -> open file
	nodes            map[string]string   // hub name -> hub node
	done             map[string]bool     // port tried
	held             map[string]bool     // port released by a session: do not claim now
	log              *slog.Logger
}

func newPortClaimer(sysRoot, devRoot string, log *slog.Logger) *portClaimer {
	return &portClaimer{sysRoot: sysRoot, devRoot: devRoot, files: map[string]*os.File{}, nodes: map[string]string{},
		done: map[string]bool{}, held: map[string]bool{}, log: log}
}

// splitPort turns a device name like 1-1.4 into the hub name 1-1 and the port 4.
func splitPort(devName string) (hub string, port int, ok bool) {
	i := strings.LastIndex(devName, ".")
	if i < 0 {
		return "", 0, false
	}
	n, err := strconv.Atoi(devName[i+1:])
	return devName[:i], n, err == nil
}

// release gives the port back to the kernel, so that interface drivers can bind to the device on it.
func (c *portClaimer) release(devName string) {
	hub, port, ok := splitPort(devName)
	if !ok {
		return
	}
	key := fmt.Sprintf("%s/%d", hub, port)
	f := c.files[c.nodes[hub]]
	if f == nil || !c.done[key] {
		return
	}
	p := uint32(port)
	if err := ioctl(f, usbdevfsReleasePort, unsafe.Pointer(&p)); err != nil {
		c.log.Warn("cannot release hub port", "hub", hub, "port", port, "err", err)
		return
	}
	c.held[key] = true
	delete(c.done, key)
	c.log.Info("released hub port for the session", "hub", hub, "port", port)
}

// reclaim lets sync claim the port again, before the next device is plugged in.
func (c *portClaimer) reclaim(devName string) {
	if hub, port, ok := splitPort(devName); ok {
		delete(c.held, fmt.Sprintf("%s/%d", hub, port))
		c.sync()
	}
}

func (c *portClaimer) sync() {
	for _, t := range planClaims(c.sysRoot, c.devRoot) {
		key := fmt.Sprintf("%s/%d", t.hub, t.port)
		if c.done[key] || c.held[key] {
			continue
		}
		c.done[key] = true // one try per port, so a failure does not fill the log
		f := c.files[t.node]
		if f == nil {
			var err error
			if f, err = os.OpenFile(t.node, os.O_RDWR, 0); err != nil {
				c.log.Warn("cannot open hub node, the kernel will configure devices on it", "node", t.node, "err", err)
				continue
			}
			c.files[t.node] = f
		}
		c.nodes[t.hub] = t.node
		port := t.port
		if err := ioctl(f, usbdevfsClaimPort, unsafe.Pointer(&port)); err != nil {
			if !errors.Is(err, unix.EBUSY) {
				c.log.Warn("cannot claim hub port", "hub", t.hub, "port", t.port, "err", err)
			}
			continue
		}
		c.log.Info("claimed hub port: the kernel will not configure devices on it", "hub", t.hub, "port", t.port)
	}
}
