package main

import (
	"encoding/binary"
	"testing"
	"time"
)

func pkt(t time.Time, mm uint16, conf, flags byte) []byte {
	b := make([]byte, packetSize)
	binary.BigEndian.PutUint32(b[0:4], uint32(t.Unix()))
	binary.BigEndian.PutUint16(b[4:6], mm)
	b[6], b[7] = conf, flags
	return b
}

func TestDecodePackets(t *testing.T) {
	now := time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)
	var b []byte
	b = append(b, pkt(now.Add(-10*time.Second), 1834, 230, 1)...) // 183.4 cm, siren on
	b = append(b, pkt(now.Add(-20*time.Second), packetBlind, 0, 0)...)
	b = append(b, pkt(now.Add(-48*time.Hour), 500, 255, 0)...) // too old: dropped
	ps, err := decodePackets(b, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || !ps[0].T.Before(ps[1].T) {
		t.Fatalf("packets: %+v", ps)
	}
	if ps[0].LevelCm != nil || ps[1].LevelCm == nil || *ps[1].LevelCm != 183.4 || !ps[1].Siren || ps[1].Confidence < 0.9 {
		t.Fatalf("decoded: %+v %+v", ps[0], ps[1])
	}
	if _, err := decodePackets(b[:15], now); err == nil {
		t.Fatal("a half packet was accepted")
	}
}

func TestParseCrop(t *testing.T) {
	c, err := parseCrop("980,150,240,500")
	if err != nil || c == nil || c.X != 980 || c.Y != 150 || c.W != 240 || c.H != 500 {
		t.Fatalf("crop %+v %v", c, err)
	}
	if c, err := parseCrop(""); c != nil || err != nil {
		t.Fatal("no crop is the whole picture")
	}
	if _, err := parseCrop("1,2,3"); err == nil {
		t.Fatal("three numbers accepted")
	}
}
