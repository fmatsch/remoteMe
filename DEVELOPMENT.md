# remoteMe – Entwicklungsdokumentation

Stand: v1.0 (erster lauffähiger Stand, entwickelt und getestet unter Windows 11). Dieses Dokument ist so geschrieben, dass du ohne Vorwissen auf dem Mac weiterarbeiten kannst.

## 1. Auf dem Mac loslegen

```bash
# Voraussetzungen (einmalig)
xcode-select --install          # C-Compiler, nötig für den macOS-Agent (cgo)
brew install go                 # Go ≥ 1.22 (entwickelt mit 1.27)

git clone https://github.com/fmatsch/remoteMe.git
cd remoteMe
go build ./...                  # alles bauen – das ist der erste Test, ob cgo/macOS-Code kompiliert
```

**Wichtig: Der macOS-Agent (`cmd/agent/input_darwin.go`) wurde unter Windows geschrieben und noch nie kompiliert.** Rechne beim ersten `go build` auf dem Mac mit kleinen Compilerfehlern (cgo-Typen, `C.int`-Konvertierungen). Das ist die erste Aufgabe.

### Lokal alles ausprobieren (Server + Agent auf demselben Mac)

```bash
mkdir -p /tmp/rm && go run ./cmd/server -addr 127.0.0.1:8080 -data /tmp/rm/data.json
# anderes Terminal:
go run ./cmd/agent -server http://127.0.0.1:8080
```

1. `http://127.0.0.1:8080` im Browser öffnen → Benutzer registrieren (der erste Benutzer darf das immer).
2. Der Agent öffnet `http://127.0.0.1:17321`. Server ist vorbelegt; Benutzername eintragen → **Verbinden**. Es erscheint ein 6-stelliger Code.
3. Im Browser beim Gerät „wartet auf Freigabe" den Code eingeben → **Verbinden**.

Beim Verbinden mit dem eigenen Mac siehst du dein eigenes Bild (Spiegel-im-Spiegel) – für Eingabetests besser zwei Macs, einen Mac + Windows/Linux-Rechner, oder eine VM nehmen.

### macOS-Berechtigungen (ohne die geht nichts)

Systemeinstellungen → Datenschutz & Sicherheit:

* **Bildschirm- & Systemaudioaufnahme** – für die Bildschirmaufnahme (`kbinani/screenshot`)
* **Bedienungshilfen** – für Maus-/Tastatureingaben (`CGEventPost`)

Die Berechtigung gilt für das **Programm, das den Agent startet** (Terminal/iTerm/IDE) bzw. später für die Agent-Binary selbst. Nach Erteilung den Agent neu starten. `AXIsProcessTrusted()` wird beim Start geprüft und eine Warnung in `agent.log` geschrieben (`~/Library/Application Support/remoteMe/agent.log`).

### Release-Binaries

```powershell
.\build.ps1 [-Server https://deine-adresse]   # Windows: Server+Agent für Windows und Linux nach dist\
```

Auf dem Mac: `CGO_ENABLED=1 go build -trimpath -ldflags "-s -w -X main.defaultServer=https://…" -o dist/remoteme-agent-macos ./cmd/agent`. Server ist reines Go und lässt sich für jede Plattform bauen. Das GitHub-Actions-Workflow `.github/workflows/build.yml` baut die Agents für Windows, Linux und macOS (arm64 + amd64) automatisch bei jedem Push.

## 2. Architektur

```
 Browser (Viewer)  <──HTTPS/WSS──>  Relay-Server  <──WSS──  Agent (Zielrechner)
   internal/server/web/index.html     internal/server       cmd/agent
```

* **Relay-Server** (`cmd/server`, `internal/server`): Benutzer, Geräte, Anmeldung, Kopplung. Reicht den Datenstrom zwischen genau einem Viewer und dem Agent eines Geräts durch (transparentes Weiterleiten, keine Auswertung der Bild-/Eingabedaten). Statische Weboberfläche ist per `go:embed` eingebettet. Speicherung in einer JSON-Datei.
* **Agent** (`cmd/agent`): läuft in der Benutzersitzung des Zielrechners, verbindet sich **ausgehend** per WebSocket zum Server (reconnect mit Backoff 2→30 s). Bildschirmaufnahme, Eingabe, Dateien, Zwischenablage. Lokale Einrichtungsseite auf `127.0.0.1:17321` (dient auch als Single-Instance-Sperre).
* **Viewer**: eine einzelne HTML-Datei mit Vanilla-JS, Canvas-Rendering.

### Dateien

| Datei | Inhalt |
|---|---|
| `internal/server/store.go` | Benutzer/Geräte (JSON), bcrypt, signierte Session-Tokens (HMAC), Kopplungscodes |
| `internal/server/server.go` | HTTP-Routen, Login/Registrierung, Geräteliste, Freigabe, CSRF-Header-Check |
| `internal/server/hub.go` | Agent-/Viewer-WebSockets, Weiterleitung, Upload/Download über HTTP↔Agent |
| `internal/server/limiter.go` | einfache Ratenbegrenzung |
| `internal/server/web/index.html` | komplette Weboberfläche (Login, Geräteliste, Sitzung, Dateipanel) |
| `cmd/agent/agent.go` | Verbindungsschleife, Nachrichtenverteilung, Zwischenablage-Schleife |
| `cmd/agent/capture.go` | Bildschirmaufnahme, Änderungserkennung (Kacheln), JPEG-Kodierung |
| `cmd/agent/files.go` | Verzeichnislisten, Upload (`.fwpart` → Rename), Download, ZIP-Streaming |
| `cmd/agent/input*.go` | Maus/Tastatur je Plattform (`_windows`, `_linux`, `_darwin`, `_other`) |
| `cmd/agent/ui.go` | lokale Einrichtungsseite (Host-/Token-Prüfung gegen DNS-Rebinding) |
| `cmd/agent/autostart*.go` | Autostart (Windows Run-Key, Linux `.desktop`, macOS LaunchAgent) |
| `cmd/agent/config.go` | Agent-Konfiguration (`agent.json`) |

## 3. Kopplung & Sicherheit

1. Agent ruft `POST /api/agent/pair {username,name,os}` → Server legt ein **unfreigegebenes** Gerät für den Benutzer an und liefert `id`, `secret` (Hash wird gespeichert) und einen 6-stelligen **Code** (nur am Agent sichtbar).
2. Agent verbindet sich per WebSocket (`X-Device-Id` + `Authorization: Bearer <secret>`), darf aber bis zur Freigabe keine Daten an Viewer liefern.
3. Benutzer gibt im Browser den Code ein (`POST /api/devices/{id}/approve`). 5 Fehlversuche löschen die Anfrage; offene Anfragen verfallen nach 30 min; max. 5 offene je Benutzer.
4. Gerät entfernen (`DELETE /api/devices/{id}`) sendet `revoked` an den Agent, der seine Kopplung löscht.

Weitere Maßnahmen: Passwörter bcrypt, Cookie `fw_session` (HttpOnly, SameSite=Strict, Secure hinter HTTPS), alle schreibenden API-Aufrufe brauchen den Header `X-FW`, WebSocket-Origin-Prüfung, Login-Limit (5/10 min je Name+IP), Registrierung nur für den ersten Benutzer (außer `-open-registration`).

## 4. Protokoll (Viewer ⇄ Relay ⇄ Agent)

Steuernachrichten sind JSON-Textframes mit Feld `t`, Daten sind Binärframes mit einem Typ-Byte. Der Relay leitet alles unverändert weiter, **außer** den Übertragungsnachrichten unten.

**Agent → Viewer**

| Nachricht | Inhalt |
|---|---|
| Binär `0x01` | `[0x01][x u16][y u16][w u16][h u16][JPEG]` – Bildausschnitt, wird auf das Canvas an (x,y) gemalt |
| `{t:"screen", w, h, i, displays:[{w,h}]}` | Größe/Index des gewählten Bildschirms |
| `{t:"clip", s}` | Zwischenablage des Zielrechners geändert |
| `{t:"ls-res", path, parent, entries:[{n,p,d,s,m}], err}` | Verzeichnisliste |

**Viewer → Agent**

| Nachricht | Inhalt |
|---|---|
| `{t:"mm", x, y}` | Mausbewegung (Pixel im gewählten Bildschirm) |
| `{t:"md"/"mu", b, x, y}` | Maustaste (0 links, 1 mitte, 2 rechts) |
| `{t:"wh", dx, dy}` | Rad in Rastschritten (positiv = nach unten) |
| `{t:"k", c, k, d, ctrl, alt, meta}` | Taste: `c`=`KeyboardEvent.code`, `k`=`.key`, `d`=gedrückt |
| `{t:"display", i}`, `{t:"opt", q}` | Bildschirm wählen, JPEG-Qualität (20–95) |
| `{t:"clip", s}` | Text in die Zwischenablage des Zielrechners |
| `{t:"ls", path}` | Verzeichnis listen (`""` = Startordner/Laufwerke) |

**Relay ⇄ Agent (Dateiübertragung, vom Relay erzeugt/ausgewertet)**

* Upload (`POST /api/devices/{id}/upload?dir=&name=`): Relay sendet `{t:"ul-begin",req,dir,name,size}`, dann Binär `[0x04][req u32][Daten]`, dann `{t:"ul-end",req}`. Agent antwortet `{t:"ul-done",req,path|err}`.
* Download (`GET /api/devices/{id}/download?path=`): Relay sendet `{t:"dl",req,path}`; Agent antwortet `{t:"dl-head",req,name,size}`, Binär `[0x03][req u32][Daten]`, `{t:"dl-end",req}` bzw. `{t:"dl-err",req,err}`. Ordner werden als ZIP gestreamt (Größe unbekannt → chunked). `{t:"dl-cancel"}` bricht ab.
* Relay → Agent: `{t:"viewer",on}` startet/stoppt Aufnahme, `{t:"approved"}`, `{t:"revoked"}`.

### Tastatur-Logik

Der Viewer schickt `code` (physische Taste) und `key` (Zeichen). Der Agent tippt **druckbare Einzelzeichen als Text** (Windows: `KEYEVENTF_UNICODE`, macOS: `CGEventKeyboardSetUnicodeString`, Linux: Keysym-Lookup) – unabhängig vom Layout des Zielrechners. Sobald Strg/Alt/Meta gehalten wird oder die Taste kein Zeichen ist (Pfeile, F-Tasten …), wird per `code` auf eine echte Taste abgebildet (Tabellen in den `input_*.go`). Beim Trennen werden alle gehaltenen Tasten losgelassen (`ReleaseAll`).

## 5. Teststand

| Bereich | Status |
|---|---|
| Server: Registrierung, Login, Kopplung, Freigabe-Code, Geräteliste | ✅ getestet (Windows) |
| Agent Windows: Aufnahme, Maus (DPI-korrekt), Dateien, ZIP, Reconnect | ✅ getestet |
| Agent Windows: **Tastatur**, Zwischenablage, Autostart, Mehrfach-Monitor | ⚠️ implementiert, nicht getestet |
| Agent Linux (X11): Aufnahme/Eingabe | ⚠️ kompiliert, nie auf einem Linux-Rechner gelaufen |
| Agent macOS | ❌ nie kompiliert, nie gelaufen |
| Viewer im Firefox/Safari | ⚠️ nur Chromium geprüft; Drag-Out auf den Desktop geht nur in Chrome/Edge |

## 6. Offene Punkte / Ideen (grob nach Nutzen)

1. **macOS-Agent zum Laufen bringen** (Kompilierung, Berechtigungen, Retina: `kbinani/screenshot` liefert auf macOS Bilder in Punkten – Koordinaten von Aufnahme und `CGEventPost` müssen übereinstimmen; prüfen!).
2. **Bildqualität/Geschwindigkeit:** Aktuell JPEG pro geändertem Rechteck (`capture.go`). Verbesserungen: mehrere kleine Rechtecke statt einer Bounding-Box, adaptive Qualität/FPS nach Bandbreite (Rückmeldung vom Viewer), Skalierung bei großen Displays, später H.264 (WebCodecs im Viewer) oder WebRTC.
3. **Zustimmung pro Sitzung** am Zielrechner (Dialog „X möchte zugreifen") als Option, sichtbarer Hinweis „Sitzung aktiv".
4. **Windows-Dienst** für Zugriff auf Sperr-/Anmeldebildschirm und UAC (Dienst + Helferprozess in der Benutzersitzung).
5. **Zeigerform des Zielrechners** übertragen (aktuell lokaler Zeiger im Viewer).
6. **Linux/Wayland** (PipeWire-Portal) – aktuell nur X11.
7. Persistente Sitzungen im Server, Mehrbenutzer-Rollen, Gerät umbenennen, Audit-Log, optionale Ende-zu-Ende-Verschlüsselung.
8. Tests: bisher nur manuelle End-zu-End-Tests; Unit-Tests für `store.go`, `changedRect`, Protokoll fehlen.

## 7. Stolpersteine, die schon aufgetreten sind

* **Netzlaufwerke blockieren `os.Stat`** (Windows, offline `Z:`) → Laufwerke werden parallel mit 700 ms Timeout geprüft, `ls` läuft in einer Goroutine, damit die Eingabeschleife nicht stockt.
* **DPI:** Windows-Agent ruft `SetProcessDPIAware` beim Start, sonst stimmen Bildgröße und Mauskoordinaten bei Skalierung nicht.
* **Browser-Tastaturen:** Reservierte Kombinationen (Strg+W, Strg+T, Alt+Tab) lassen sich nur im Vollbild mit der Keyboard-Lock-API abfangen (Chrome/Edge).
* Beim Start des Agents öffnet sich der Standardbrowser, solange er nicht gekoppelt ist; `-background` unterdrückt das nur bei bestehender Kopplung.
