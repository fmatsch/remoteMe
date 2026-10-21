//go:build darwin

package main

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -mmacosx-version-min=13.0 -framework Foundation -framework ScreenCaptureKit -framework VideoToolbox -framework CoreMedia -framework CoreVideo -framework CoreGraphics
#include <stdlib.h>
#include "video_darwin.h"
*/
import "C"

import (
	"errors"
	"sync"
	"unsafe"
)

// Es gibt höchstens einen nativen Strom gleichzeitig (C-Zustand ist global).
var (
	nativeMu   sync.Mutex
	nativeSink func(au []byte, key bool)
)

//export goVideoFrame
func goVideoFrame(data unsafe.Pointer, n C.int, key C.int) {
	nativeMu.Lock()
	sink := nativeSink
	nativeMu.Unlock()
	if sink != nil {
		sink(C.GoBytes(data, n), key != 0)
	}
}

type nativeVideo struct{ name string }

// maxEdge begrenzt die längere Seite der Kodiergröße (Retina-Bildschirme werden verkleinert).
const maxEdge = 2560

func newNativeVideo(p *videoParams) (videoPipe, error) {
	id := C.fwDisplayID(C.int(p.Display))
	if id == 0 {
		return nil, errors.New("Bildschirm nicht gefunden")
	}
	// Punkte -> Pixel: höchstens Faktor 2 (Retina), nie über maxEdge.
	long := max(p.Bounds.Dx(), p.Bounds.Dy())
	scale := min(2.0, float64(maxEdge)/float64(long))
	scale = max(scale, 1.0)
	p.OutW, p.OutH = even(int(float64(p.Bounds.Dx())*scale)), even(int(float64(p.Bounds.Dy())*scale))

	nativeMu.Lock()
	nativeSink = p.Sink
	nativeMu.Unlock()
	var msg [256]C.char
	if C.fwVideoStart(id, C.int(p.OutW), C.int(p.OutH), C.int(p.FPS), C.int(p.Bitrate), &msg[0], 256) != 0 {
		nativeMu.Lock()
		nativeSink = nil
		nativeMu.Unlock()
		return nil, errors.New(C.GoString(&msg[0]))
	}
	return &nativeVideo{name: "VideoToolbox"}, nil
}

func (n *nativeVideo) Name() string       { return n.name }
func (n *nativeVideo) RequestKey()        { C.fwVideoKey() }
func (n *nativeVideo) SetBitrate(bps int) { C.fwVideoBitrate(C.int(bps)) }
func (n *nativeVideo) Alive() bool        { return C.fwVideoAlive() != 0 }
func (n *nativeVideo) Close() {
	C.fwVideoStop()
	nativeMu.Lock()
	nativeSink = nil
	nativeMu.Unlock()
}
