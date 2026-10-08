package main

import (
	"encoding/hex"
	"testing"

	"github.com/oandrew/ipod/hid"
)

// Report descriptors from oandrew/ipod-gadget gadget/ipod.h: what an iPhone or iPad sends at each USB speed.
const fullSpeedDescHex = ("0600ff0901a1017508268000150009018501950c82020109018502950e820201" +
	"09018503951482020109018504953f8202010901850595089202010901850695" +
	"0a92020109018507950e92020109018508951492020109018509953f920201c0")

const highSpeedDescHex = ("0600ff0901a10175082680001500090185019505820201090185029509820201" +
	"09018503950d8202010901850495118202010901850595198202010901850695" +
	"3182020109018507955f8202010901850895c182020109018509960101820201" +
	"0901850a9681018202010901850b9601028202010901850c96ff028202010901" +
	"850d95059202010901850e95099202010901850f950d92020109018510951192" +
	"020109018511951992020109018512953192020109018513955f920201090185" +
	"1495c19202010901851595ff920201c0")

func mustHex(t testing.TB, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func lens(defs hid.ReportDefs, dir hid.ReportDir) (ids, ls []int) {
	for _, d := range defs {
		if d.Dir == dir {
			ids = append(ids, d.ID)
			ls = append(ls, d.Len)
		}
	}
	return
}

func eq(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParseFullSpeedDescriptor(t *testing.T) {
	d := mustHex(t, fullSpeedDescHex)
	if len(d) != 96 {
		t.Fatalf("descriptor is %d bytes, want 96", len(d))
	}
	defs, err := parseReportDescriptor(d)
	if err != nil {
		t.Fatal(err)
	}
	ids, ls := lens(defs, hid.ReportDirAccIn)
	if !eq(ids, []int{1, 2, 3, 4}) || !eq(ls, []int{12, 14, 20, 63}) {
		t.Errorf("inputs: ids %v lens %v", ids, ls)
	}
	ids, ls = lens(defs, hid.ReportDirAccOut)
	if !eq(ids, []int{5, 6, 7, 8, 9}) || !eq(ls, []int{8, 10, 14, 20, 63}) {
		t.Errorf("outputs: ids %v lens %v", ids, ls)
	}
}

func TestParseHighSpeedDescriptor(t *testing.T) {
	d := mustHex(t, highSpeedDescHex)
	if len(d) != 208 {
		t.Fatalf("descriptor is %d bytes, want 208", len(d))
	}
	defs, err := parseReportDescriptor(d)
	if err != nil {
		t.Fatal(err)
	}
	ids, ls := lens(defs, hid.ReportDirAccIn)
	if !eq(ids, []int{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}) ||
		!eq(ls, []int{5, 9, 13, 17, 25, 49, 95, 193, 257, 385, 513, 767}) {
		t.Errorf("inputs: ids %v lens %v", ids, ls)
	}
	ids, ls = lens(defs, hid.ReportDirAccOut)
	if !eq(ids, []int{13, 14, 15, 16, 17, 18, 19, 20, 21}) || !eq(ls, []int{5, 9, 13, 17, 25, 49, 95, 193, 255}) {
		t.Errorf("outputs: ids %v lens %v", ids, ls)
	}
}

func TestParseRejectsBadDescriptors(t *testing.T) {
	for name, d := range map[string][]byte{
		"empty":     nil,
		"truncated": {0x85},
		"no id":     {0x75, 0x08, 0x95, 0x04, 0x81, 0x02},
	} {
		if _, err := parseReportDescriptor(d); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}
