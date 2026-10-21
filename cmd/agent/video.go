package main

import (
	"context"
	"encoding/binary"
	"errors"
	"image"
	"log"
	"os"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"github.com/kbinani/screenshot"
)

// Agent -> Viewer: [0x02][flags u8: Bit0 = Schlüsselbild][seq u32][H.264 Access Unit im Annex-B-Format]
const frameH264 = 0x02

const (
	codecJPEG int32 = iota
	codecH264
)

const (
	maxInflightJPEG = 4
	maxInflightH264 = 8
	minBitrate      = 300_000
	videoFPS        = 30
)

// videoParams beschreibt einen H.264-Videostrom des gewählten Bildschirms.
type videoParams struct {
	Display    int
	Bounds     image.Rectangle // Bildschirm in Punkten (Koordinatensystem der Eingabe)
	OutW, OutH int             // Kodiergröße (gerade Zahlen)
	FPS        int
	Bitrate    int
	Sink       func(au []byte, key bool)
}

// videoPipe ist ein laufender Encoder (nativ oder ffmpeg).
type videoPipe interface {
	RequestKey()
	SetBitrate(bps int)
	Alive() bool
	Name() string
	Close()
}

var errNoVideo = errors.New("kein H.264-Encoder verfügbar")

func even(n int) int { return n &^ 1 }

// flow: Flusssteuerung. Der Viewer bestätigt verarbeitete Bilder kumulativ ({t:"ack",e,n}),
// der Agent sendet nur, solange nicht zu viele Bilder unbestätigt unterwegs sind.
type flow struct {
	epoch atomic.Uint32
	sent  atomic.Uint32
	acked atomic.Uint32
}

func (f *flow) restart() uint32 {
	e := f.epoch.Add(1)
	f.sent.Store(0)
	f.acked.Store(0)
	return e
}
func (f *flow) inflight() int { return int(int32(f.sent.Load() - f.acked.Load())) }
func (f *flow) ack(e, n uint32) {
	if e != f.epoch.Load() {
		return
	}
	if int32(n-f.acked.Load()) > 0 && int32(f.sent.Load()-n) >= 0 {
		f.acked.Store(n)
	}
}

// bitrateFor leitet aus der Qualitätsstufe (20..95) die Höchstbitrate ab.
func bitrateFor(q int32) int {
	return 1_000_000 + int(q-20)*180_000 // 20 -> 1 MBit/s, 70 -> 10 MBit/s, 95 -> 14,5 MBit/s
}

// captureLoop wählt je nach Wunsch des Viewers H.264 oder JPEG-Kacheln.
func (a *Agent) captureLoop(ctx context.Context) {
	h264Failed := false
	for ctx.Err() == nil {
		if a.codec.Load() == codecH264 && !h264Failed {
			err := a.videoLoop(ctx)
			if err != nil && ctx.Err() == nil {
				h264Failed = true
				log.Println("H.264 nicht möglich, wechsle zu JPEG:", err)
				a.sendJSON(map[string]any{"t": "codec", "c": "jpeg", "why": err.Error()})
			}
			continue
		}
		a.jpegLoop(ctx, func() bool { return a.codec.Load() == codecH264 && !h264Failed })
	}
}

func (a *Agent) displayBounds() (int, image.Rectangle, bool) {
	n := screenshot.NumActiveDisplays()
	if n == 0 {
		return 0, image.Rectangle{}, false
	}
	idx := int(a.disp.Load())
	if idx < 0 || idx >= n {
		idx = 0
	}
	return idx, screenshot.GetDisplayBounds(idx), true
}

// videoLoop läuft, solange Bildschirm, Qualität und Codec unverändert bleiben.
func (a *Agent) videoLoop(ctx context.Context) error {
	idx, b, ok := a.displayBounds()
	if !ok {
		time.Sleep(500 * time.Millisecond)
		return nil
	}
	q := a.quality.Load()
	maxBps := bitrateFor(q)
	curBps := maxBps * 6 / 10

	e := a.fl.restart()
	var needKey atomic.Bool
	var drops atomic.Int32
	var pipe videoPipe

	sink := func(au []byte, key bool) {
		if needKey.Load() {
			if !key {
				return
			}
			needKey.Store(false)
		}
		if a.fl.inflight() >= maxInflightH264 {
			drops.Add(1)
			needKey.Store(true) // ohne Vorgänger sind Folgebilder wertlos
			return
		}
		msg := make([]byte, 6+len(au))
		msg[0] = frameH264
		if key {
			msg[1] = 1
		}
		binary.BigEndian.PutUint32(msg[2:], a.fl.sent.Load())
		copy(msg[6:], au)
		if a.send(websocket.BinaryMessage, msg) == nil {
			a.fl.sent.Add(1)
		}
	}

	p := videoParams{Display: idx, Bounds: b, FPS: videoFPS, Bitrate: curBps, Sink: sink}
	var err error
	pipe, err = openVideo(ctx, &p)
	if err != nil {
		return err
	}
	defer pipe.Close()
	log.Printf("H.264-Strom: %s, %dx%d, %d kBit/s", pipe.Name(), p.OutW, p.OutH, curBps/1000)
	a.screenInfo(idx, map[string]any{"codec": "h264", "vw": p.OutW, "vh": p.OutH, "e": e})

	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	var lastKeyReq, lastAdapt time.Time
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}
		if a.keyReq.Swap(false) {
			pipe.RequestKey()
		}
		if !pipe.Alive() {
			return errors.New("Encoder wurde beendet")
		}
		if a.codec.Load() != codecH264 || a.quality.Load() != q {
			return nil
		}
		if i2, b2, ok := a.displayBounds(); !ok || i2 != idx || b2 != b {
			return nil
		}
		if needKey.Load() && a.fl.inflight() <= 2 && time.Since(lastKeyReq) > 300*time.Millisecond {
			lastKeyReq = time.Now()
			pipe.RequestKey()
		}
		if time.Since(lastAdapt) > time.Second {
			lastAdapt = time.Now()
			old := curBps
			if drops.Swap(0) > 0 {
				curBps = max(minBitrate, curBps*7/10)
			} else if a.fl.inflight() <= 1 {
				curBps = min(maxBps, curBps*11/10)
			}
			if curBps != old {
				pipe.SetBitrate(curBps)
			}
		}
	}
}

// openVideo versucht erst den nativen Encoder, dann ffmpeg.
func openVideo(ctx context.Context, p *videoParams) (videoPipe, error) {
	if os.Getenv("REMOTEME_VIDEO") != "ffmpeg" { // Umschalter zum Testen des ffmpeg-Wegs
		if pipe, err := newNativeVideo(p); err == nil {
			return pipe, nil
		} else if !errors.Is(err, errNoVideo) {
			log.Println("Nativer H.264-Encoder:", err)
		}
	}
	if pipe, err := newFFmpegVideo(ctx, p); err == nil {
		return pipe, nil
	} else {
		return nil, err
	}
}
