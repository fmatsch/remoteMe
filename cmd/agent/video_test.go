package main

import (
	"bytes"
	"testing"
)

func aud() []byte { return []byte{0, 0, 0, 1, 9, 0xf0} }

func TestSplitAU(t *testing.T) {
	key := append(aud(), []byte{0, 0, 0, 1, 0x67, 1, 2, 0, 0, 0, 1, 0x68, 3, 0, 0, 1, 0x65, 9, 9, 9}...)
	delta := append(aud(), []byte{0, 0, 1, 0x41, 7, 7}...)
	stream := append(append(append([]byte{}, key...), delta...), aud()...) // letztes AUD schließt die zweite AU ab

	var got [][]byte
	var keys []bool
	// Die Eingabe in kleinen Stücken liefern, um Grenzen mitten in Startcodes zu prüfen.
	pr := &chunkReader{data: stream, n: 3}
	splitAU(pr, func(au []byte, k bool) { got = append(got, au); keys = append(keys, k) })

	if len(got) != 2 {
		t.Fatalf("erwartet 2 Access Units, erhalten %d", len(got))
	}
	if !bytes.Equal(got[0], key) || !keys[0] {
		t.Errorf("AU 0 falsch oder nicht als Schlüsselbild erkannt: % x", got[0])
	}
	if !bytes.Equal(got[1], delta) || keys[1] {
		t.Errorf("AU 1 falsch oder fälschlich Schlüsselbild: % x", got[1])
	}
}

type chunkReader struct {
	data []byte
	n    int
}

func (c *chunkReader) Read(p []byte) (int, error) {
	if len(c.data) == 0 {
		return 0, errEOF
	}
	n := min(c.n, len(c.data), len(p))
	copy(p, c.data[:n])
	c.data = c.data[n:]
	return n, nil
}

var errEOF = bytes.ErrTooLarge // beliebiger Fehler beendet den Leser

func TestFlowAck(t *testing.T) {
	var f flow
	e := f.restart()
	f.sent.Store(5)
	f.ack(e, 3)
	if f.inflight() != 2 {
		t.Fatalf("inflight = %d, erwartet 2", f.inflight())
	}
	f.ack(e+1, 5) // falsche Epoche wird ignoriert
	f.ack(e, 2)   // Rückschritt wird ignoriert
	f.ack(e, 9)   // mehr als gesendet wird ignoriert
	if f.inflight() != 2 {
		t.Fatalf("inflight = %d nach ungültigen Bestätigungen, erwartet 2", f.inflight())
	}
	f.ack(e, 5)
	if f.inflight() != 0 {
		t.Fatalf("inflight = %d, erwartet 0", f.inflight())
	}
}

func TestBitrateFor(t *testing.T) {
	if b := bitrateFor(20); b != 1_000_000 {
		t.Errorf("bitrateFor(20) = %d", b)
	}
	if bitrateFor(95) <= bitrateFor(70) {
		t.Error("höhere Qualität muss höhere Bitrate ergeben")
	}
}
