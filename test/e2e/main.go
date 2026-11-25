// e2e verhält sich wie der Viewer: meldet sich an, verbindet sich mit einem Gerät, zählt Bilder
// und speichert JPEG-Bilder vor und nach einer Eingabe. Für automatische Tests (Windows-Dienst).
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	server := flag.String("server", "http://127.0.0.1:8099", "")
	user := flag.String("user", "citest", "")
	pw := flag.String("password", "", "")
	dev := flag.String("device", "", "")
	out := flag.String("out", "out", "")
	flag.Parse()
	os.MkdirAll(*out, 0o755)

	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Jar: jar, Timeout: 20 * time.Second}
	body, _ := json.Marshal(map[string]string{"username": *user, "password": *pw})
	req, _ := http.NewRequest("POST", *server+"/api/login", bytes.NewReader(body))
	req.Header.Set("X-FW", "1")
	resp, err := cl.Do(req)
	if err != nil || resp.StatusCode != 200 {
		log.Fatal("Anmeldung fehlgeschlagen: ", err)
	}
	io.Copy(io.Discard, resp.Body)

	d := websocket.Dialer{Jar: jar}
	ws, _, err := d.Dial("ws"+strings.TrimPrefix(*server, "http")+"/ws/view/"+*dev, nil)
	if err != nil {
		log.Fatal("Viewer-Verbindung: ", err)
	}
	defer ws.Close()

	msgs := make(chan []byte, 256)
	kinds := make(chan int, 256)
	go func() {
		for {
			mt, m, err := ws.ReadMessage()
			if err != nil {
				close(msgs)
				return
			}
			kinds <- mt
			msgs <- m
		}
	}()
	send := func(v any) { ws.WriteJSON(v) }
	var canvas *image.RGBA
	var epoch, got float64
	jpegFrames, h264Frames, keyFrames := 0, 0, 0
	run := func(d time.Duration) {
		end := time.After(d)
		for {
			select {
			case <-end:
				return
			case mt := <-kinds:
				m, ok := <-msgs
				if !ok {
					log.Fatal("Verbindung beendet")
				}
				if mt == websocket.TextMessage {
					var c map[string]any
					json.Unmarshal(m, &c)
					switch c["t"] {
					case "screen":
						fmt.Println("screen:", string(m))
						epoch, _ = c["e"].(float64)
						got = 0
						w, h := int(c["w"].(float64)), int(c["h"].(float64))
						canvas = image.NewRGBA(image.Rect(0, 0, w, h))
					case "nocap", "codec":
						fmt.Println(string(m))
					}
					continue
				}
				switch m[0] {
				case 0x01:
					jpegFrames++
					x, y := int(binary.BigEndian.Uint16(m[1:])), int(binary.BigEndian.Uint16(m[3:]))
					img, err := jpeg.Decode(bytes.NewReader(m[9:]))
					if err == nil && canvas != nil {
						draw.Draw(canvas, img.Bounds().Add(image.Pt(x, y)), img, image.Point{}, draw.Src)
					}
				case 0x02:
					h264Frames++
					if m[1]&1 == 1 {
						keyFrames++
					}
				}
				got++
				send(map[string]any{"t": "ack", "e": epoch, "n": got})
			}
		}
	}
	save := func(name string) {
		if canvas == nil {
			fmt.Println(name, ": kein Bild")
			return
		}
		f, _ := os.Create(filepath.Join(*out, name))
		defer f.Close()
		jpeg.Encode(f, canvas, &jpeg.Options{Quality: 80})
		nonBlack := 0
		for i := 0; i < len(canvas.Pix); i += 4 * 97 {
			if int(canvas.Pix[i])+int(canvas.Pix[i+1])+int(canvas.Pix[i+2]) > 30 {
				nonBlack++
			}
		}
		fmt.Printf("%s gespeichert (%v, %d helle Stichproben)\n", name, canvas.Rect, nonBlack)
	}

	send(map[string]any{"t": "opt", "q": 70, "codec": "jpeg"})
	run(8 * time.Second)
	save("1-start.jpg")
	// Leertaste: am Sperr-/Anmeldebildschirm erscheint damit die Anmeldung
	send(map[string]any{"t": "mm", "x": 200, "y": 200})
	send(map[string]any{"t": "k", "c": "Space", "k": " ", "d": true})
	send(map[string]any{"t": "k", "c": "Space", "k": " ", "d": false})
	run(5 * time.Second)
	save("2-nach-leertaste.jpg")
	send(map[string]any{"t": "sas"})
	run(5 * time.Second)
	save("3-nach-strg-alt-entf.jpg")
	// Auf dem Sicherheitsbildschirm ist "Sperren" vorausgewählt: Eingabetaste sperrt den Rechner.
	key := func(code, k string) {
		send(map[string]any{"t": "k", "c": code, "k": k, "d": true})
		send(map[string]any{"t": "k", "c": code, "k": k, "d": false})
	}
	key("Enter", "Enter")
	run(8 * time.Second)
	save("4-gesperrt.jpg")
	send(map[string]any{"t": "sas"}) // Sperrbildschirm verlangt ggf. Strg+Alt+Entf
	run(8 * time.Second)
	save("5-anmeldung.jpg")
	// Im Passwortfeld tippen: die Punkte müssen erscheinen (Eingabe am gesperrten Rechner)
	for _, c := range "abc" {
		key("Key"+strings.ToUpper(string(c)), string(c))
	}
	run(4 * time.Second)
	save("6-getippt.jpg")
	fmt.Println("JPEG-Bilder:", jpegFrames)

	send(map[string]any{"t": "opt", "q": 70, "codec": "h264"})
	for i := 0; i < 10; i++ { // Bewegung erzeugen
		send(map[string]any{"t": "mm", "x": 100 + i*40, "y": 100 + i*30})
		run(time.Second)
	}
	fmt.Println("H.264-Bilder:", h264Frames, "davon Schlüsselbilder:", keyFrames)
	if jpegFrames == 0 {
		log.Fatal("FEHLER: kein einziges Bild empfangen")
	}
}
