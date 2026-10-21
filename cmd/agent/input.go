package main

// Input setzt Maus- und Tastatureingaben des Viewers auf dem Zielrechner um.
// Koordinaten sind globale Desktop-Koordinaten in Pixeln.
type Input interface {
	Move(x, y int)
	Button(b int, down bool, x, y int) // b: 0 links, 1 mitte, 2 rechts
	Wheel(dx, dy float64)              // in Rastschritten, positiv = nach unten/rechts
	// Key: code = KeyboardEvent.code, key = KeyboardEvent.key,
	// shortcut = Strg/Alt/Meta gedrückt (dann nie als Text eintippen).
	Key(code, key string, down bool, shortcut bool)
	ReleaseAll()
}

// singleRune liefert das Zeichen, wenn key genau ein druckbares Zeichen ist.
func singleRune(key string) (rune, bool) {
	r := []rune(key)
	if len(r) == 1 && r[0] >= 0x20 && r[0] != 0x7f {
		return r[0], true
	}
	return 0, false
}
