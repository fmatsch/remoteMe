//go:build linux

package main

import (
	"log"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

// Linux: Eingaben über die XTEST-Erweiterung von X11 (Wayland-Sitzungen werden nicht unterstützt).
type x11Input struct {
	mu     sync.Mutex
	c      *xgb.Conn
	root   xproto.Window
	min    xproto.Keycode
	perKey int
	syms   []xproto.Keysym
	down   map[xproto.Keycode]bool
	shift  xproto.Keycode
}

func newInput() Input {
	x := &x11Input{down: map[xproto.Keycode]bool{}}
	c, err := xgb.NewConn()
	if err != nil {
		log.Println("X11 nicht erreichbar, Fernsteuerung nicht möglich:", err)
		return noInput{}
	}
	if err := xtest.Init(c); err != nil {
		log.Println("XTEST fehlt:", err)
		return noInput{}
	}
	setup := xproto.Setup(c)
	x.c, x.root = c, setup.DefaultScreen(c).Root
	x.min = setup.MinKeycode
	n := byte(setup.MaxKeycode - setup.MinKeycode + 1)
	km, err := xproto.GetKeyboardMapping(c, setup.MinKeycode, n).Reply()
	if err != nil {
		log.Println("Tastaturbelegung:", err)
		return noInput{}
	}
	x.perKey, x.syms = int(km.KeysymsPerKeycode), km.Keysyms
	x.shift = x.keycodeFor(0xffe1)
	return x
}

type noInput struct{}

func (noInput) Move(x, y int)                  {}
func (noInput) Button(int, bool, int, int)     {}
func (noInput) Wheel(dx, dy float64)           {}
func (noInput) Key(string, string, bool, bool) {}
func (noInput) ReleaseAll()                    {}

func (x *x11Input) fake(typ byte, detail byte, rx, ry int16) {
	xtest.FakeInput(x.c, typ, detail, 0, x.root, rx, ry, 0)
	x.c.Sync()
}

func (x *x11Input) Move(px, py int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.fake(xproto.MotionNotify, 0, int16(px), int16(py))
}

func (x *x11Input) Button(b int, down bool, px, py int) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.fake(xproto.MotionNotify, 0, int16(px), int16(py))
	btn := [...]byte{1, 2, 3}
	if b < 0 || b > 2 {
		return
	}
	t := byte(xproto.ButtonRelease)
	if down {
		t = xproto.ButtonPress
	}
	x.fake(t, btn[b], 0, 0)
}

func (x *x11Input) Wheel(dx, dy float64) {
	x.mu.Lock()
	defer x.mu.Unlock()
	click := func(btn byte, n int) {
		for i := 0; i < n; i++ {
			x.fake(xproto.ButtonPress, btn, 0, 0)
			x.fake(xproto.ButtonRelease, btn, 0, 0)
		}
	}
	rnd := func(f float64) int {
		if f < 0 {
			f = -f
		}
		n := int(f + 0.5)
		if n == 0 && f > 0 {
			n = 1
		}
		return n
	}
	if dy > 0 {
		click(5, rnd(dy))
	} else if dy < 0 {
		click(4, rnd(dy))
	}
	if dx > 0 {
		click(7, rnd(dx))
	} else if dx < 0 {
		click(6, rnd(dx))
	}
}

// keycodeFor sucht den Tastencode zu einem Keysym; level ist 0 (ohne) oder 1 (mit Shift).
func (x *x11Input) keycodeFor(sym xproto.Keysym) xproto.Keycode {
	kc, _ := x.lookup(sym)
	return kc
}

func (x *x11Input) lookup(sym xproto.Keysym) (xproto.Keycode, int) {
	for i, s := range x.syms {
		if s == sym {
			return x.min + xproto.Keycode(i/x.perKey), i % x.perKey
		}
	}
	return 0, 0
}

func (x *x11Input) Key(code, key string, down bool, shortcut bool) {
	var sym xproto.Keysym
	if r, ok := singleRune(key); ok {
		sym = xproto.Keysym(r)
		if r > 0xff {
			sym = xproto.Keysym(0x01000000 + uint32(r))
		}
	} else if s, ok := x11Keys[code]; ok {
		sym = s
	} else {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	kc, level := x.lookup(sym)
	if kc == 0 {
		return
	}
	needShift := level == 1 && x.shift != 0 && !x.down[x.shift]
	if down {
		if needShift {
			x.fake(xproto.KeyPress, byte(x.shift), 0, 0)
		}
		x.fake(xproto.KeyPress, byte(kc), 0, 0)
		x.down[kc] = true
		if needShift {
			// Shift-Taste nach dem Drücken wieder lösen, damit sie nicht hängen bleibt
			defer x.fake(xproto.KeyRelease, byte(x.shift), 0, 0)
		}
	} else {
		x.fake(xproto.KeyRelease, byte(kc), 0, 0)
		delete(x.down, kc)
	}
}

func (x *x11Input) ReleaseAll() {
	x.mu.Lock()
	defer x.mu.Unlock()
	for kc := range x.down {
		x.fake(xproto.KeyRelease, byte(kc), 0, 0)
	}
	x.down = map[xproto.Keycode]bool{}
	for b := byte(1); b <= 3; b++ {
		x.fake(xproto.ButtonRelease, b, 0, 0)
	}
}

var x11Keys = func() map[string]xproto.Keysym {
	m := map[string]xproto.Keysym{
		"Backspace": 0xff08, "Tab": 0xff09, "Enter": 0xff0d, "NumpadEnter": 0xff8d, "Pause": 0xff13,
		"ScrollLock": 0xff14, "Escape": 0xff1b, "Space": 0x20, "Home": 0xff50, "ArrowLeft": 0xff51,
		"ArrowUp": 0xff52, "ArrowRight": 0xff53, "ArrowDown": 0xff54, "PageUp": 0xff55, "PageDown": 0xff56,
		"End": 0xff57, "PrintScreen": 0xff61, "Insert": 0xff63, "ContextMenu": 0xff67, "NumLock": 0xff7f,
		"Delete": 0xffff, "CapsLock": 0xffe5,
		"ShiftLeft": 0xffe1, "ShiftRight": 0xffe2, "ControlLeft": 0xffe3, "ControlRight": 0xffe4,
		"AltLeft": 0xffe9, "AltRight": 0xffea, "MetaLeft": 0xffeb, "MetaRight": 0xffec,
		"NumpadMultiply": 0xffaa, "NumpadAdd": 0xffab, "NumpadSubtract": 0xffad,
		"NumpadDecimal": 0xffae, "NumpadDivide": 0xffaf,
		"Semicolon": ';', "Equal": '=', "Comma": ',', "Minus": '-', "Period": '.', "Slash": '/',
		"Backquote": '`', "BracketLeft": '[', "Backslash": '\\', "BracketRight": ']', "Quote": '\'',
	}
	for i := 0; i < 10; i++ {
		m["Digit"+string(rune('0'+i))] = xproto.Keysym('0' + i)
		m["Numpad"+string(rune('0'+i))] = xproto.Keysym(0xffb0 + i)
	}
	for c := 'A'; c <= 'Z'; c++ {
		m["Key"+string(c)] = xproto.Keysym(c + 32)
	}
	for i := 1; i <= 24; i++ {
		m["F"+itoa(i)] = xproto.Keysym(0xffbd + i)
	}
	return m
}()

func itoa(i int) string {
	if i >= 10 {
		return string(rune('0'+i/10)) + string(rune('0'+i%10))
	}
	return string(rune('0' + i))
}
