//go:build darwin

package main

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics
#include <ApplicationServices/ApplicationServices.h>

static void fwMouse(int type, int button, double x, double y) {
	CGEventRef e = CGEventCreateMouseEvent(NULL, (CGEventType)type, CGPointMake(x, y), (CGMouseButton)button);
	if (e) { CGEventPost(kCGHIDEventTap, e); CFRelease(e); }
}
static void fwKey(int code, int down) {
	CGEventRef e = CGEventCreateKeyboardEvent(NULL, (CGKeyCode)code, down != 0);
	if (e) { CGEventPost(kCGHIDEventTap, e); CFRelease(e); }
}
static void fwText(const unsigned short *s, int n) {
	for (int d = 1; d >= 0; d--) {
		CGEventRef e = CGEventCreateKeyboardEvent(NULL, 0, d);
		if (e) { CGEventKeyboardSetUnicodeString(e, n, s); CGEventPost(kCGHIDEventTap, e); CFRelease(e); }
	}
}
static void fwScroll(int dy, int dx) {
	CGEventRef e = CGEventCreateScrollWheelEvent(NULL, kCGScrollEventUnitPixel, 2, dy, dx);
	if (e) { CGEventPost(kCGHIDEventTap, e); CFRelease(e); }
}
static int fwTrusted(void) { return AXIsProcessTrusted(); }
*/
import "C"

import (
	"log"
	"sync"
	"unicode/utf16"
)

// macOS: benötigt die Freigaben "Bedienungshilfen" und "Bildschirmaufnahme" in den Systemeinstellungen.
type macInput struct {
	mu      sync.Mutex
	buttons [3]bool
	down    map[int]bool
}

func newInput() Input {
	if C.fwTrusted() == 0 {
		log.Println("Hinweis: Fernwartung braucht unter Systemeinstellungen > Datenschutz > Bedienungshilfen und Bildschirmaufnahme eine Freigabe.")
	}
	return &macInput{down: map[int]bool{}}
}

var macMouse = [3][3]C.int{
	{1, 2, 5},    // links: down, up, (drag=6)
	{25, 26, 27}, // mitte
	{3, 4, 7},    // rechts
}

func (m *macInput) Move(x, y int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.move(x, y)
}

func (m *macInput) move(x, y int) {
	typ, btn := C.int(5), C.int(0) // kCGEventMouseMoved
	switch {
	case m.buttons[0]:
		typ, btn = 6, 0
	case m.buttons[2]:
		typ, btn = 7, 1
	case m.buttons[1]:
		typ, btn = 27, 2
	}
	C.fwMouse(typ, btn, C.double(x), C.double(y))
}

func (m *macInput) Button(b int, down bool, x, y int) {
	if b < 0 || b > 2 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	btn := C.int(b)
	if b == 1 {
		btn = 2
	} else if b == 2 {
		btn = 1
	}
	t := macMouse[b][1]
	if down {
		t = macMouse[b][0]
	}
	m.buttons[b] = down
	C.fwMouse(t, btn, C.double(x), C.double(y))
}

func (m *macInput) Wheel(dx, dy float64) {
	C.fwScroll(C.int(-dy*40), C.int(-dx*40))
}

func (m *macInput) Key(code, key string, down bool, shortcut bool) {
	if r, ok := singleRune(key); ok && !shortcut {
		if down {
			u := utf16.Encode([]rune{r})
			C.fwText((*C.ushort)(&u[0]), C.int(len(u)))
		}
		return
	}
	kc, ok := macKeys[code]
	if !ok {
		return
	}
	m.mu.Lock()
	if down {
		m.down[kc] = true
	} else {
		delete(m.down, kc)
	}
	m.mu.Unlock()
	d := C.int(0)
	if down {
		d = 1
	}
	C.fwKey(C.int(kc), d)
}

func (m *macInput) ReleaseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for kc := range m.down {
		C.fwKey(C.int(kc), 0)
	}
	m.down = map[int]bool{}
	m.buttons = [3]bool{}
}

var macKeys = map[string]int{
	"KeyA": 0, "KeyS": 1, "KeyD": 2, "KeyF": 3, "KeyH": 4, "KeyG": 5, "KeyZ": 6, "KeyX": 7, "KeyC": 8, "KeyV": 9,
	"IntlBackslash": 10, "KeyB": 11, "KeyQ": 12, "KeyW": 13, "KeyE": 14, "KeyR": 15, "KeyY": 16, "KeyT": 17,
	"Digit1": 18, "Digit2": 19, "Digit3": 20, "Digit4": 21, "Digit6": 22, "Digit5": 23, "Equal": 24, "Digit9": 25,
	"Digit7": 26, "Minus": 27, "Digit8": 28, "Digit0": 29, "BracketRight": 30, "KeyO": 31, "KeyU": 32,
	"BracketLeft": 33, "KeyI": 34, "KeyP": 35, "Enter": 36, "KeyL": 37, "KeyJ": 38, "Quote": 39, "KeyK": 40,
	"Semicolon": 41, "Backslash": 42, "Comma": 43, "Slash": 44, "KeyN": 45, "KeyM": 46, "Period": 47, "Tab": 48,
	"Space": 49, "Backquote": 50, "Backspace": 51, "Escape": 53, "MetaRight": 54, "MetaLeft": 55,
	"ShiftLeft": 56, "CapsLock": 57, "AltLeft": 58, "ControlLeft": 59, "ShiftRight": 60, "AltRight": 61,
	"ControlRight": 62, "NumpadDecimal": 65, "NumpadMultiply": 67, "NumpadAdd": 69, "NumLock": 71,
	"NumpadDivide": 75, "NumpadEnter": 76, "NumpadSubtract": 78,
	"Numpad0": 82, "Numpad1": 83, "Numpad2": 84, "Numpad3": 85, "Numpad4": 86, "Numpad5": 87, "Numpad6": 88,
	"Numpad7": 89, "Numpad8": 91, "Numpad9": 92,
	"F5": 96, "F6": 97, "F7": 98, "F3": 99, "F8": 100, "F9": 101, "F11": 103, "F10": 109, "F12": 111,
	"Home": 115, "PageUp": 116, "Delete": 117, "F4": 118, "End": 119, "F2": 120, "PageDown": 121, "F1": 122,
	"ArrowLeft": 123, "ArrowRight": 124, "ArrowDown": 125, "ArrowUp": 126, "ContextMenu": 110,
}
