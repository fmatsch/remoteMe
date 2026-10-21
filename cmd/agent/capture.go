package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kbinani/screenshot"
)

const (
	tile       = 64
	frameVideo = 0x01 // Agent -> Viewer: [0x01][x u16][y u16][w u16][h u16][jpeg]
	minFrame   = 33 * time.Millisecond
)

var (
	boundsMu sync.Mutex
	curBound image.Rectangle
)

// abs rechnet Koordinaten des gewählten Bildschirms in globale Desktop-Koordinaten um.
func (a *Agent) abs(x, y float64) (int, int) {
	boundsMu.Lock()
	b := curBound
	boundsMu.Unlock()
	return b.Min.X + int(x), b.Min.Y + int(y)
}

// changedRect liefert das kleinste Rechteck (in Kachelgrenzen), das alle Änderungen enthält.
func changedRect(prev, cur *image.RGBA) image.Rectangle {
	w, h := cur.Rect.Dx(), cur.Rect.Dy()
	minX, minY, maxX, maxY := w, h, -1, -1
	for ty := 0; ty < h; ty += tile {
		th := min(tile, h-ty)
		for tx := 0; tx < w; tx += tile {
			tw := min(tile, w-tx)
			diff := false
			for y := ty; y < ty+th; y++ {
				o := y*cur.Stride + tx*4
				if !bytes.Equal(prev.Pix[o:o+tw*4], cur.Pix[o:o+tw*4]) {
					diff = true
					break
				}
			}
			if diff {
				minX, minY = min(minX, tx), min(minY, ty)
				maxX, maxY = max(maxX, tx+tw), max(maxY, ty+th)
			}
		}
	}
	if maxX < 0 {
		return image.Rectangle{}
	}
	return image.Rect(minX, minY, maxX, maxY)
}

func (a *Agent) screenInfo(idx int, extra map[string]any) {
	n := screenshot.NumActiveDisplays()
	var ds []map[string]int
	for i := 0; i < n; i++ {
		b := screenshot.GetDisplayBounds(i)
		ds = append(ds, map[string]int{"w": b.Dx(), "h": b.Dy()})
	}
	b := screenshot.GetDisplayBounds(idx)
	m := map[string]any{"t": "screen", "w": b.Dx(), "h": b.Dy(), "i": idx, "displays": ds, "codec": "jpeg", "e": a.fl.epoch.Load()}
	for k, v := range extra {
		m[k] = v
	}
	a.sendJSON(m)
}

// jpegLoop sendet geänderte Bildbereiche als JPEG, bis stop() wahr wird.
func (a *Agent) jpegLoop(ctx context.Context, stop func() bool) {
	a.fl.restart()
	var prev *image.RGBA
	var buf bytes.Buffer
	cur := -1
	var curB image.Rectangle
	failed := 0
	for ctx.Err() == nil && !stop() {
		started := time.Now()
		if a.fl.inflight() >= maxInflightJPEG { // Viewer/Leitung kommt nicht nach
			time.Sleep(5 * time.Millisecond)
			continue
		}
		n := screenshot.NumActiveDisplays()
		if n == 0 {
			time.Sleep(500 * time.Millisecond)
			continue
		}
		idx := int(a.disp.Load())
		if idx < 0 || idx >= n {
			idx = 0
		}
		b := screenshot.GetDisplayBounds(idx)
		if idx != cur || b != curB {
			cur, curB, prev = idx, b, nil
			boundsMu.Lock()
			curBound = b
			boundsMu.Unlock()
			a.fl.restart()
			prev = nil
			a.screenInfo(idx, nil)
		}
		img, err := screenshot.CaptureRect(b)
		if err != nil {
			if failed++; failed == 1 || failed%50 == 0 {
				log.Println("Bildschirmaufnahme fehlgeschlagen:", err)
			}
			time.Sleep(200 * time.Millisecond)
			continue
		}
		failed = 0

		r := img.Rect
		if prev != nil && prev.Rect == img.Rect {
			r = changedRect(prev, img)
		}
		prev = img
		if r.Empty() {
			time.Sleep(15 * time.Millisecond)
			continue
		}
		// Bei großen Änderungen lohnt sich ein Gesamtbild.
		if r.Dx()*r.Dy()*10 > img.Rect.Dx()*img.Rect.Dy()*7 {
			r = img.Rect
		}

		buf.Reset()
		buf.Write([]byte{frameVideo, 0, 0, 0, 0, 0, 0, 0, 0})
		hdr := buf.Bytes()
		binary.BigEndian.PutUint16(hdr[1:], uint16(r.Min.X-img.Rect.Min.X))
		binary.BigEndian.PutUint16(hdr[3:], uint16(r.Min.Y-img.Rect.Min.Y))
		binary.BigEndian.PutUint16(hdr[5:], uint16(r.Dx()))
		binary.BigEndian.PutUint16(hdr[7:], uint16(r.Dy()))
		sub := img.SubImage(r)
		if err := jpeg.Encode(&buf, sub, &jpeg.Options{Quality: int(a.quality.Load())}); err != nil {
			continue
		}
		if a.send(websocket.BinaryMessage, buf.Bytes()) != nil {
			return
		}
		a.fl.sent.Add(1)
		if d := minFrame - time.Since(started); d > 0 {
			time.Sleep(d)
		}
	}
}
