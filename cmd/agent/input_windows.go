//go:build windows

package main

import (
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32           = syscall.NewLazyDLL("user32.dll")
	pSendInput       = user32.NewProc("SendInput")
	pSetCursorPos    = user32.NewProc("SetCursorPos")
	pMapVirtualKeyW  = user32.NewProc("MapVirtualKeyW")
	pSetProcessDPIAw = user32.NewProc("SetProcessDPIAware")
)

func init() {
	// Ohne DPI-Awareness stimmen Bildschirmgröße und Mauskoordinaten bei Skalierung nicht überein.
	pSetProcessDPIAw.Call()
}

const (
	inputMouse    = 0
	inputKeyboard = 1

	evLeftDown   = 0x0002
	evLeftUp     = 0x0004
	evRightDown  = 0x0008
	evRightUp    = 0x0010
	evMiddleDown = 0x0020
	evMiddleUp   = 0x0040
	evWheel      = 0x0800
	evHWheel     = 0x1000

	keyExtended = 0x0001
	keyUp       = 0x0002
	keyUnicode  = 0x0004
)

type mouseInput struct {
	dx, dy    int32
	mouseData uint32
	flags     uint32
	time      uint32
	extra     uintptr
}

type keybdInput struct {
	vk, scan uint16
	flags    uint32
	time     uint32
	extra    uintptr
}

// winInput entspricht der INPUT-Struktur (40 Bytes auf 64-Bit-Windows).
type winInput struct {
	typ uint32
	_   uint32
	u   [32]byte
}

func sendInput(in *winInput) {
	pSendInput.Call(1, uintptr(unsafe.Pointer(in)), unsafe.Sizeof(*in))
}

type winInputImpl struct {
	mu   sync.Mutex
	down map[uint16]bool
}

func newInput() Input { return &winInputImpl{down: map[uint16]bool{}} }

func (w *winInputImpl) Move(x, y int) { pSetCursorPos.Call(uintptr(x), uintptr(y)) }

func (w *winInputImpl) mouse(flags uint32, data uint32) {
	var in winInput
	in.typ = inputMouse
	*(*mouseInput)(unsafe.Pointer(&in.u[0])) = mouseInput{flags: flags, mouseData: data}
	sendInput(&in)
}

func (w *winInputImpl) Button(b int, down bool, x, y int) {
	w.Move(x, y)
	var f uint32
	switch b {
	case 0:
		f = evLeftUp
		if down {
			f = evLeftDown
		}
	case 1:
		f = evMiddleUp
		if down {
			f = evMiddleDown
		}
	case 2:
		f = evRightUp
		if down {
			f = evRightDown
		}
	default:
		return
	}
	w.mouse(f, 0)
}

func (w *winInputImpl) Wheel(dx, dy float64) {
	if dy != 0 {
		w.mouse(evWheel, uint32(int32(-dy*120)))
	}
	if dx != 0 {
		w.mouse(evHWheel, uint32(int32(dx*120)))
	}
}

func (w *winInputImpl) key(vk, scan uint16, flags uint32) {
	var in winInput
	in.typ = inputKeyboard
	*(*keybdInput)(unsafe.Pointer(&in.u[0])) = keybdInput{vk: vk, scan: scan, flags: flags}
	sendInput(&in)
}

func (w *winInputImpl) Key(code, key string, down bool, shortcut bool) {
	if r, ok := singleRune(key); ok && !shortcut {
		if !down {
			return
		}
		// Text direkt als Unicode - unabhängig vom Tastaturlayout des Zielrechners.
		var units []uint16
		if r >= 0x10000 {
			r -= 0x10000
			units = []uint16{uint16(0xD800 + (r >> 10)), uint16(0xDC00 + (r & 0x3FF))}
		} else {
			units = []uint16{uint16(r)}
		}
		for _, u := range units {
			w.key(0, u, keyUnicode)
			w.key(0, u, keyUnicode|keyUp)
		}
		return
	}
	vk, ext := winVK(code)
	if vk == 0 {
		return
	}
	scan, _, _ := pMapVirtualKeyW.Call(uintptr(vk), 0)
	var f uint32
	if ext {
		f |= keyExtended
	}
	w.mu.Lock()
	if down {
		w.down[vk] = true
	} else {
		delete(w.down, vk)
		f |= keyUp
	}
	w.mu.Unlock()
	w.key(vk, uint16(scan), f)
}

func (w *winInputImpl) ReleaseAll() {
	w.mu.Lock()
	keys := make([]uint16, 0, len(w.down))
	for vk := range w.down {
		keys = append(keys, vk)
	}
	w.down = map[uint16]bool{}
	w.mu.Unlock()
	for _, vk := range keys {
		scan, _, _ := pMapVirtualKeyW.Call(uintptr(vk), 0)
		w.key(vk, uint16(scan), keyUp)
	}
	w.mouse(evLeftUp, 0)
	w.mouse(evRightUp, 0)
	w.mouse(evMiddleUp, 0)
}

var winKeys = map[string]uint16{
	"Backspace": 0x08, "Tab": 0x09, "Enter": 0x0D, "NumpadEnter": 0x0D, "Pause": 0x13, "CapsLock": 0x14,
	"Escape": 0x1B, "Space": 0x20, "PageUp": 0x21, "PageDown": 0x22, "End": 0x23, "Home": 0x24,
	"ArrowLeft": 0x25, "ArrowUp": 0x26, "ArrowRight": 0x27, "ArrowDown": 0x28, "PrintScreen": 0x2C,
	"Insert": 0x2D, "Delete": 0x2E, "MetaLeft": 0x5B, "MetaRight": 0x5C, "ContextMenu": 0x5D,
	"NumpadMultiply": 0x6A, "NumpadAdd": 0x6B, "NumpadSubtract": 0x6D, "NumpadDecimal": 0x6E,
	"NumpadDivide": 0x6F, "NumLock": 0x90, "ScrollLock": 0x91,
	"ShiftLeft": 0xA0, "ShiftRight": 0xA1, "ControlLeft": 0xA2, "ControlRight": 0xA3,
	"AltLeft": 0xA4, "AltRight": 0xA5,
	"Semicolon": 0xBA, "Equal": 0xBB, "Comma": 0xBC, "Minus": 0xBD, "Period": 0xBE, "Slash": 0xBF,
	"Backquote": 0xC0, "BracketLeft": 0xDB, "Backslash": 0xDC, "BracketRight": 0xDD, "Quote": 0xDE,
	"IntlBackslash": 0xE2,
}

var winExtended = map[uint16]bool{
	0x21: true, 0x22: true, 0x23: true, 0x24: true, 0x25: true, 0x26: true, 0x27: true, 0x28: true,
	0x2C: true, 0x2D: true, 0x2E: true, 0x5B: true, 0x5C: true, 0x5D: true, 0x6F: true, 0x90: true,
	0xA3: true, 0xA5: true,
}

func winVK(code string) (vk uint16, ext bool) {
	if code == "" {
		return 0, false
	}
	if v, ok := winKeys[code]; ok {
		vk = v
	} else if len(code) == 4 && code[:3] == "Key" {
		vk = uint16(code[3]) // A-Z = 0x41-0x5A
	} else if len(code) == 6 && code[:5] == "Digit" {
		vk = uint16(code[5]) // 0-9 = 0x30-0x39
	} else if len(code) == 7 && code[:6] == "Numpad" && code[6] >= '0' && code[6] <= '9' {
		vk = 0x60 + uint16(code[6]-'0')
	} else if code[0] == 'F' && len(code) <= 3 {
		n := 0
		for _, c := range code[1:] {
			if c < '0' || c > '9' {
				return 0, false
			}
			n = n*10 + int(c-'0')
		}
		if n >= 1 && n <= 24 {
			vk = 0x70 + uint16(n-1)
		}
	}
	ext = winExtended[vk] || code == "NumpadEnter"
	return
}
