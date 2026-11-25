# remoteMe – Entwicklungsdokumentation

Stand: v1.1 (v1.0 unter Windows 11 entwickelt; v1.1 auf dem Mac: H.264-Videostrom, Flusssteuerung, macOS-App, Server-Deployment). Dieses Dokument ist so geschrieben, dass du ohne Vorwissen auf dem Mac weiterarbeiten kannst.

## 1. Auf dem Mac loslegen

```bash
# Voraussetzungen (einmalig)
xcode-select --install          # C-Compiler, nötig für den macOS-Agent (cgo)
brew install go                 # Go ≥ 1.22 (entwickelt mit 1.27)

git clone https://github.com/fmatsch/remoteMe.git
cd remoteMe
go build ./...                  # alles bauen – das ist der erste Test, ob cgo/macOS-Code kompiliert
```

Der macOS-Agent kompiliert (cgo: ApplicationServices, ScreenCaptureKit, VideoToolbox, Cocoa). Es erscheinen harmlose Linker-Warnungen „built for newer macOS version“; `build-macos.sh` setzt `-mmacosx-version-min=13.0` einheitlich.

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

**macOS-App:** `./build-macos.sh [-s https://server/remote] [-v 1.1.0] [-i "Developer ID Application: …"] [--dmg]` baut `dist/macos/remoteMe.app` (Universal arm64+x86_64, Menüleistensymbol, Icon aus `packaging/macos/make-icon.swift`) und optional ein DMG. Ohne `-i` wird ad hoc signiert: läuft auf dem eigenen Mac; auf fremden Macs Rechtsklick → Öffnen (für reibungslose Verteilung Developer-ID-Signatur + Notarisierung nötig). **Eigenes Zertifikat:** Liegt im Anmelde-Schlüsselbund ein Codesigning-Zertifikat namens „remoteMe Signatur“ (selbst erzeugt, nicht als vertrauenswürdig markiert), signiert `build-macos.sh` damit; die Freigaben (Bildschirmaufnahme, Bedienungshilfen) bleiben dann über Neubauten erhalten. Ohne das Zertifikat wird ad hoc signiert und jede neue Version gilt als neue App. Den privaten Schlüssel nicht weitergeben.

## 1b. Server-Deployment

`deploy/deploy.sh` baut den Linux-Server, lädt ihn per SSH (`~/.ssh/praxiskalender_ed25519`, `root@138.201.56.28`) hoch und installiert ihn: Dienst `remoteme.service` (Benutzer `remoteme`, Port 4740 auf 127.0.0.1, Daten `/srv/remoteme/data.json`, Programm `/opt/remoteme/`), Caddy-Datei `/etc/caddy/remoteme.caddy` für **https://remote.depes.ch** (eigener Ursprung – nicht unter einem Pfad neben anderen Anwendungen betreiben, sonst teilen sie Cookies und Rechte). Vor jeder Änderung wird `/etc/caddy` nach `/root/remoteme-caddy-<zeit>` gesichert; ungültige Konfiguration wird zurückgesetzt. Unter `www.depes.ch/remote/ws/agent` verbinden sich weiterhin früher gekoppelte Zielrechner (Rest des alten Pfads leitet um).

## 1c. Windows-Dienst (`cmd/agent/service_windows.go`)

`remoteMe.exe -install -from <agent.json> [-quiet]` kopiert Programm und ffmpeg nach `%ProgramFiles%\remoteMe`, die Kopplung nach `%ProgramData%\remoteMe` (DACL nur SYSTEM und Administratoren), setzt `SoftwareSASGeneration=1` und legt den Dienst an. Der Dienst (`-service`) startet in der aktiven Sitzung (aktive RDP-/Konsolensitzung, sonst Konsole) einen Helfer (`-helper`) mit SYSTEM-Token; ein Job-Objekt beendet ihn samt ffmpeg. Der Helfer ruft vor jeder Aufnahme/Eingabe `useInputDesktop()` (OpenInputDesktop/SetThreadDesktop), hat keine Einrichtungsseite und startet nichts aus dem Suchpfad. Strg+Alt+Entf: Viewer `{t:"sas"}` → Ereignis `Global\remoteMeSAS` → Dienst ruft `SendSAS`. Der Viewer zeigt den Knopf, wenn `screen.sas` gesetzt ist. `-uninstall` entfernt Dienst und Kopplung.

**Test:** `.github/workflows/windows-service.yml` (Push auf Branch `windows-test` oder manuell) installiert den Dienst auf einem GitHub-Windows-Runner, verbindet `test/e2e`, löst Strg+Alt+Entf aus, sperrt den Rechner und tippt ins Passwortfeld; die Bilder liegen im Artefakt `ergebnis`.

Meldet der Agent kein Bild, schickt er `{t:"nocap", msg}` (`nocap.go`).

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
| `cmd/agent/capture.go` | JPEG-Modus: Bildschirmaufnahme, Änderungserkennung (Kacheln), JPEG-Kodierung |
| `cmd/agent/video.go` | Codec-Wahl (H.264 ↔ JPEG), Flusssteuerung (`flow`), adaptive Bitrate, `videoLoop` |
| `cmd/agent/video_darwin.{go,m,h}` | nativer Mac-Encoder: ScreenCaptureKit → VideoToolbox (Hardware-H.264), Annex-B-Ausgabe |
| `cmd/agent/video_ffmpeg.go` | H.264 für Windows/Linux über einen ffmpeg-Unterprozess (nvenc/qsv/amf/libx264/videotoolbox) |
| `cmd/agent/app_darwin.{go,m,h}` | native Mac-Oberfläche (Cocoa): Menüleistensymbol, Einrichtungs-/Statusfenster mit Freigabecode, Freigaben anfordern; kein Browser nötig. Kopplung gemeinsam mit der Browser-Seite über `Agent.pairDevice` (`ui.go`) |
| `cmd/agent/files.go` | Verzeichnislisten, Upload (`.fwpart` → Rename), Download, ZIP-Streaming |
| `cmd/agent/input*.go` | Maus/Tastatur je Plattform (`_windows`, `_linux`, `_darwin`, `_other`) |
| `cmd/agent/ui.go` | lokale Einrichtungsseite (Host-/Token-Prüfung gegen DNS-Rebinding) |
| `cmd/agent/autostart*.go` | Autostart (Windows Run-Key, Linux `.desktop`, macOS LaunchAgent) |
| `cmd/agent/config.go` | Agent-Konfiguration (`agent.json`) |

## 3. Kopplung & Sicherheit

1. Agent ruft `POST /api/agent/pair {name,os,password}` → Server legt ein **verfügbares** Gerät ohne Besitzer an und liefert `id`, `secret` (Hash wird gespeichert) und einen 6-stelligen **Code** (nur am Agent sichtbar). Das **Zugriffspasswort** (≥ 6 Zeichen) wird am Zielrechner vergeben, dort nicht gespeichert; der Server hält nur einen bcrypt-Hash bis zur Übernahme.
2. Agent verbindet sich per WebSocket (`X-Device-Id` + `Authorization: Bearer <secret>`), liefert aber bis zur Freigabe keine Daten an Viewer.
3. Jeder angemeldete Benutzer sieht verfügbare Rechner, solange sie online sind. Übernahme: `POST /api/devices/{id}/approve {code,password}` → das Gerät gehört danach diesem Benutzer, Code und Passwort-Hash werden gelöscht. Raten ist begrenzt (5 je Gerät, 20 je Benutzer in 15 min). Verfügbare Geräte verfallen erst, wenn sie 7 Tage offline waren; höchstens 100 gleichzeitig.
4. Gerät entfernen (`DELETE /api/devices/{id}`, nur Besitzer) sendet `revoked` an den Agent, der seine Kopplung löscht.
5. Ältere Agents (1.1) schicken `username` statt `password`: das Gerät gehört sofort diesem Benutzer, Freigabe nur mit Code (30 min, 5 Versuche, max. 5 offene).

Wer sich anmelden kann, sieht Namen und System aller verfügbaren Rechner – Registrierung deshalb geschlossen lassen.

**Rollen (Mac-App):** „Nur als Ziel“ oder „Vollversion“ (`role` im Konfigurationsordner, `cmd/agent/role.go`); die Auswahl erscheint beim ersten Start und ist im Einstellungsfenster änderbar. In der Ziel-Rolle sind die Fernsteuer-Menüpunkte ausgeblendet (reine Oberflächen-Einstellung, keine Sicherheitsgrenze).

**Zweiter Faktor:** je Benutzer TOTP (`internal/server/totp.go`), Einrichtung nur auf dem Server (`deploy/user.sh --totp NAME`), jeder Code gilt einmal. Anmeldung `POST /api/login {username,password,code}`; fehlt der Code, kommt 401 mit `totp:true`. Sitzungen gelten 12 h und enden bei Änderung von Passwort oder zweitem Faktor; die Mac-App meldet nicht still neu an.

**Alles vom Zielrechner ist nicht vertrauenswürdig:** Der Viewer wandelt Zahlen mit `num()` und maskiert Texte mit `esc()`; die Content-Security-Policy (`headers.go`) erlaubt nur das eingebettete Skript – deshalb keine `on…=`-Attribute und kein zweites `<script>` in `index.html`.

Weitere Maßnahmen: Passwörter bcrypt, Cookie `fw_session` (HttpOnly, SameSite=Strict, Secure hinter HTTPS), alle schreibenden API-Aufrufe brauchen den Header `X-FW`, WebSocket-Origin-Prüfung, Anmeldebegrenzung (je Absender, je Benutzer+Absender, je Benutzer), `X-Forwarded-For` nur vom lokalen Proxy, Nachrichtengröße der Agents begrenzt (64 KB vor, 16 MB nach Freigabe), höchstens 10 verfügbare Rechner je Absender, Apps verlangen https, lokale Einrichtungsseite nur ohne natives Fenster und nur mit Schlüssel (`ui.token`), Anmeldungen/Fehlversuche/Übernahmen im Protokoll (`journalctl -u remoteme`), Registrierung nur für den ersten Benutzer (außer `-open-registration`).

**Bekannte Grenzen:** keine Ende-zu-Ende-Verschlüsselung (der Server sieht alles), keine Zustimmung/Anzeige am Zielrechner, keine signierten Apps und kein Auto-Update, Zwischenablage des Ziels wird automatisch übertragen. Prüfung der Abhängigkeiten: `go run golang.org/x/vuln/cmd/govulncheck@latest ./cmd/server ./cmd/agent`.

## 4. Protokoll (Viewer ⇄ Relay ⇄ Agent)

Steuernachrichten sind JSON-Textframes mit Feld `t`, Daten sind Binärframes mit einem Typ-Byte. Der Relay leitet alles unverändert weiter, **außer** den Übertragungsnachrichten unten.

**Agent → Viewer**

| Nachricht | Inhalt |
|---|---|
| Binär `0x01` | `[0x01][x u16][y u16][w u16][h u16][JPEG]` – Bildausschnitt, wird auf das Canvas an (x,y) gemalt |
| Binär `0x02` | `[0x02][flags u8: Bit0=Schlüsselbild][seq u32][H.264 Access Unit, Annex B]` – ein ganzes Videobild; Schlüsselbilder enthalten SPS/PPS, keine B-Frames |
| `{t:"screen", w, h, i, displays, codec, e, vw?, vh?}` | Größe (Punkte = Eingabekoordinaten) und Index des Bildschirms, aktiver Codec, Strom-Epoche `e`; bei H.264 Kodiergröße `vw×vh` (Canvas wird so groß, Eingaben skalieren über `w×h`) |
| `{t:"codec", c:"jpeg", why}` | Agent konnte H.264 nicht starten und liefert JPEG |
| `{t:"clip", s}` | Zwischenablage des Zielrechners geändert |
| `{t:"ls-res", path, parent, entries:[{n,p,d,s,m}], err}` | Verzeichnisliste |

**Viewer → Agent**

| Nachricht | Inhalt |
|---|---|
| `{t:"mm", x, y}` | Mausbewegung (Pixel im gewählten Bildschirm) |
| `{t:"md"/"mu", b, x, y}` | Maustaste (0 links, 1 mitte, 2 rechts) |
| `{t:"wh", dx, dy}` | Rad in Rastschritten (positiv = nach unten) |
| `{t:"k", c, k, d, ctrl, alt, meta}` | Taste: `c`=`KeyboardEvent.code`, `k`=`.key`, `d`=gedrückt |
| `{t:"display", i}`, `{t:"opt", q, codec}` | Bildschirm wählen; Qualität 20–95 (JPEG-Qualität bzw. Höchstbitrate 1–14,5 MBit/s); Codec `"h264"` (nur wenn WebCodecs verfügbar ist: https oder localhost) oder `"jpeg"` |
| `{t:"ack", e, n}` | **Flusssteuerung:** kumulativ n verarbeitete Bilder in Epoche e. Der Agent sendet höchstens 4 (JPEG) bzw. 8 (H.264) unbestätigte Bilder; bei Stau werden H.264-Bilder verworfen, die Bitrate sinkt und es wird auf ein neues Schlüsselbild gewartet |
| `{t:"key"}` | Viewer braucht ein Schlüsselbild (Decoderfehler) |
| `{t:"clip", s}` | Text in die Zwischenablage des Zielrechners |
| `{t:"ls", path}` | Verzeichnis listen (`""` = Startordner/Laufwerke) |

**Relay ⇄ Agent (Dateiübertragung, vom Relay erzeugt/ausgewertet)**

* Upload (`POST /api/devices/{id}/upload?dir=&name=`): Relay sendet `{t:"ul-begin",req,dir,name,size}`, dann Binär `[0x04][req u32][Daten]`, dann `{t:"ul-end",req}`. Agent antwortet `{t:"ul-done",req,path|err}`.
* Download (`GET /api/devices/{id}/download?path=`): Relay sendet `{t:"dl",req,path}`; Agent antwortet `{t:"dl-head",req,name,size}`, Binär `[0x03][req u32][Daten]`, `{t:"dl-end",req}` bzw. `{t:"dl-err",req,err}`. Ordner werden als ZIP gestreamt (Größe unbekannt → chunked). `{t:"dl-cancel"}` bricht ab.
* Relay → Agent: `{t:"viewer",on}` startet/stoppt Aufnahme, `{t:"welcome",approved,owner}`, `{t:"approved",owner}`, `{t:"revoked"}`.

### Tastatur-Logik

Der Viewer schickt `code` (physische Taste) und `key` (Zeichen). Der Agent tippt **druckbare Einzelzeichen als Text** (Windows: `KEYEVENTF_UNICODE`, macOS: `CGEventKeyboardSetUnicodeString`, Linux: Keysym-Lookup) – unabhängig vom Layout des Zielrechners. Sobald Strg/Alt/Meta gehalten wird oder die Taste kein Zeichen ist (Pfeile, F-Tasten …), wird per `code` auf eine echte Taste abgebildet (Tabellen in den `input_*.go`). Beim Trennen werden alle gehaltenen Tasten losgelassen (`ReleaseAll`).

## 5. Teststand

| Bereich | Status |
|---|---|
| Server: Registrierung, Login, Kopplung, Freigabe-Code, Geräteliste | ✅ getestet (Windows) |
| Agent Windows: Aufnahme, Maus (DPI-korrekt), Dateien, ZIP, Reconnect | ✅ getestet |
| Agent Windows: **Tastatur**, Zwischenablage, Autostart, Mehrfach-Monitor | ⚠️ implementiert, nicht getestet |
| Agent Linux (X11): Aufnahme/Eingabe | ⚠️ kompiliert, nie auf einem Linux-Rechner gelaufen |
| Agent macOS: Build, Aufnahme (ScreenCaptureKit), H.264 (VideoToolbox) → Browser, 30 Bilder/s | ✅ getestet (Mac mit macOS 27, Chromium) |
| Agent macOS: Maus/Tastatur (`CGEventPost`), Retina-Koordinaten, `.app` mit Freigaben | ⚠️ implementiert, Eingabe noch nicht geprüft |
| H.264 per ffmpeg-Pfad (Windows/Linux) | ⚠️ am Mac mit h264_videotoolbox geprüft, nicht unter Windows/Linux |
| Server auf `https://www.depes.ch/remote/` (Ubuntu 24.04, Caddy) | ✅ läuft im Testbetrieb |
| Viewer im Firefox/Safari | ⚠️ nur Chromium geprüft; Drag-Out auf den Desktop geht nur in Chrome/Edge |

## 6. Offene Punkte / Ideen (grob nach Nutzen)

1. **macOS-Eingabe prüfen** (Maus/Tastatur über `CGEventPost`; Koordinaten sind Punkte wie in `screen.w/h`). Mauszeiger des Zielrechners einblenden/übertragen.
2. **Video weiter ausbauen:** ✅ H.264 (macOS nativ, sonst ffmpeg), Flusssteuerung und adaptive Bitrate sind umgesetzt. Offen: Windows-Hardware-Encoder ohne ffmpeg (Media Foundation), ffmpeg mit der Windows-/Linux-App ausliefern (Lizenz beachten: libx264 ist GPL) oder go-openh264 (purego) einbinden, Bildschirmaufnahme unter Windows schneller (DXGI Desktop Duplication statt GDI), AV1/VP9, Audio. Der JPEG-Modus bleibt Fallback (http ohne WebCodecs, kein Encoder).
3. **Zustimmung pro Sitzung** am Zielrechner (Dialog „X möchte zugreifen") als Option, sichtbarer Hinweis „Sitzung aktiv".
4. ✅ Windows-Dienst für Sperr-/Anmeldebildschirm (§1c). Offen: Gegenstück für macOS (LaunchDaemon, LoginWindow-Sitzung, signierte App).
5. **Zeigerform des Zielrechners** übertragen (aktuell lokaler Zeiger im Viewer).
6. **Linux/Wayland** (PipeWire-Portal) – aktuell nur X11.
7. Persistente Sitzungen im Server, Mehrbenutzer-Rollen, Gerät umbenennen, Audit-Log, optionale Ende-zu-Ende-Verschlüsselung.
8. Tests: bisher nur manuelle End-zu-End-Tests; Unit-Tests für `store.go`, `changedRect`, Protokoll fehlen.

## 7. Stolpersteine, die schon aufgetreten sind

* **Netzlaufwerke blockieren `os.Stat`** (Windows, offline `Z:`) → Laufwerke werden parallel mit 700 ms Timeout geprüft, `ls` läuft in einer Goroutine, damit die Eingabeschleife nicht stockt.
* **DPI:** Windows-Agent ruft `SetProcessDPIAware` beim Start, sonst stimmen Bildgröße und Mauskoordinaten bei Skalierung nicht.
* **Browser-Tastaturen:** Reservierte Kombinationen (Strg+W, Strg+T, Alt+Tab) lassen sich nur im Vollbild mit der Keyboard-Lock-API abfangen (Chrome/Edge).
* **`kbinani/screenshot` ist auf dem Mac langsam** (~225 ms pro Bild, also ~4 Bilder/s): nur noch Fallback/JPEG. Der Videopfad nutzt ScreenCaptureKit.
* **Server hält Benutzer im Speicher:** `server adduser|setuser|deluser` nur bei gestopptem Dienst ausführen, sonst überschreibt der laufende Server die Änderung. `deploy/user.sh` erledigt Stoppen und Starten. Sitzungs-Token sind an den Passwort-Hash gebunden.
* **Heredocs im Deploy-Skript:** verschachtelte `EOF` brechen Shell-Aufrufe; deshalb die Server-Seite in `deploy/remote-install.sh`.
* Beim Start des Agents öffnet sich der Standardbrowser, solange er nicht gekoppelt ist; `-background` unterdrückt das nur bei bestehender Kopplung.
