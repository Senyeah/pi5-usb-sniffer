package main

import (
	"errors"
	"fmt"

	"github.com/oandrew/ipod/hid"
)

// parseReportDescriptor lists each report ID with its direction and size.
// Input reports go from the Apple device to us (AccIn). Output reports go the other way (AccOut).
// Len is in bytes, without the report ID.
func parseReportDescriptor(d []byte) (hid.ReportDefs, error) {
	type key struct {
		id  int
		dir hid.ReportDir
	}
	var (
		id, size, count int
		order           []key
		bits            = map[key]int{}
	)
	add := func(dir hid.ReportDir) error {
		if id == 0 {
			return errors.New("report without a report ID")
		}
		k := key{id, dir}
		if _, ok := bits[k]; !ok {
			order = append(order, k)
		}
		bits[k] += size * count
		return nil
	}
	for i := 0; i < len(d); {
		b := d[i]
		if b == 0xFE { // long item: skip
			if i+1 >= len(d) {
				return nil, errors.New("truncated long item")
			}
			i += 3 + int(d[i+1])
			continue
		}
		n := int(b & 3)
		if n == 3 {
			n = 4
		}
		if i+1+n > len(d) {
			return nil, errors.New("truncated item")
		}
		var v int
		for j := 0; j < n; j++ {
			v |= int(d[i+1+j]) << (8 * j)
		}
		typ, tag := (b>>2)&3, b>>4
		switch {
		case typ == 0 && tag == 0x8: // Input
			if err := add(hid.ReportDirAccIn); err != nil {
				return nil, err
			}
		case typ == 0 && tag == 0x9: // Output
			if err := add(hid.ReportDirAccOut); err != nil {
				return nil, err
			}
		case typ == 1 && tag == 0x7:
			size = v
		case typ == 1 && tag == 0x8:
			id = v
		case typ == 1 && tag == 0x9:
			count = v
		}
		i += 1 + n
	}
	if len(order) == 0 {
		return nil, errors.New("no reports in descriptor")
	}
	defs := make(hid.ReportDefs, 0, len(order))
	for _, k := range order {
		defs = append(defs, hid.ReportDef{ID: k.id, Len: (bits[k] + 7) / 8, Dir: k.dir})
	}
	return defs, nil
}

func describeDefs(defs hid.ReportDefs) string {
	in, out := "", ""
	for _, d := range defs {
		s := fmt.Sprintf(" %#x:%d", d.ID, d.Len)
		if d.Dir == hid.ReportDirAccIn {
			in += s
		} else {
			out += s
		}
	}
	return "in[" + in + " ] out[" + out + " ]"
}
