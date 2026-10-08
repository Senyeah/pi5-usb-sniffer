package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/oandrew/ipod"
)

// These tests use a private capture. Set IAP_CAPTURE_TIMELINE to the decoder's timeline.jsonl
// and IAP_REAL_CERT to the stereo's accessory-cert-N.p7b. Without them the tests skip.

type timelineEvent struct {
	Dir   string `json:"dir"`
	Layer string `json:"layer"`
	Name  string `json:"name"`
	Raw   string `json:"raw"`
}

func stereoPackets(t *testing.T) []timelineEvent {
	path := os.Getenv("IAP_CAPTURE_TIMELINE")
	if path == "" {
		t.Skip("IAP_CAPTURE_TIMELINE is not set")
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []timelineEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var e timelineEvent
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Layer == "iap1" && e.Dir == "A>D" {
			out = append(out, e)
		}
	}
	return out
}

// Every packet the real stereo sent must survive decode, then encode, byte for byte.
func TestFramingReproducesEveryStereoPacket(t *testing.T) {
	events := stereoPackets(t)
	if len(events) == 0 {
		t.Fatal("no stereo packets in the capture")
	}
	for _, e := range events {
		raw, err := hex.DecodeString(e.Raw)
		if err != nil {
			t.Fatal(err)
		}
		payload, err := ipod.NewPacketReader(raw).ReadPacket()
		if err != nil {
			t.Fatalf("%s: %v", e.Name, err)
		}
		r, ok := parsePayload(payload)
		if !ok {
			t.Fatalf("%s: payload too short", e.Name)
		}
		got, err := encodeFrame(r.id, r.args)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, raw) {
			t.Errorf("%s:\n got %x\nwant %x", e.Name, got, raw)
		}
	}
	t.Logf("%d stereo packets reproduced", len(events))
}

// The certificate sections must equal the two RetDevAuthenticationInfo packets of the first connection.
func TestCertificateSectionsMatchCapture(t *testing.T) {
	events := stereoPackets(t)
	certPath := os.Getenv("IAP_REAL_CERT")
	if certPath == "" {
		t.Skip("IAP_REAL_CERT is not set")
	}
	cert, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, e := range events {
		if strings.HasSuffix(e.Name, "RetDevAuthenticationInfo") {
			want = append(want, e.Raw)
		}
	}
	if len(want) < 2 {
		t.Fatal("capture has no certificate sections")
	}

	o := testOptions(cert)
	rec := &recordingFrames{}
	s := newStereo(o, rec, func(error) {})
	go s.loop()
	defer close(s.done)
	s.post(s.sendCert)
	// The step engine waits for the iPod's ACK between sections.
	waitFor(t, "first section", func() bool { return rec.count() >= 1 })
	got := rec.frames()
	if hex.EncodeToString(got[0]) != want[0] {
		t.Errorf("section 0 differs:\n got %x\nwant %s", got[0], want[0])
	}
	s.post(func() { s.onRx(rx{id: cid(lGeneral, 0x02), args: []byte{0x00, 0x15}}) })
	waitFor(t, "second section", func() bool { return rec.count() >= 2 })
	got = rec.frames()
	if hex.EncodeToString(got[1]) != want[1] {
		t.Errorf("section 1 differs:\n got %x\nwant %s", got[1], want[1])
	}
}
