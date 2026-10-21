# remoteMe

**Webseite:** https://fmatsch.ist/remoteMe/ · **Entwicklung/Mac-Setup:** [DEVELOPMENT.md](DEVELOPMENT.md)

Selbst gehostetes Fernwartungs-Tool für Windows, Linux und macOS. Der Zugriff läuft im Browser: Bildschirm sehen, Maus und Tastatur bedienen, Dateien per Drag & Drop in beide Richtungen schieben, Zwischenablage teilen.

```
 Browser (Viewer)  <──HTTPS/WSS──>  Relay-Server  <──WSS──  Agent (Zielrechner)
```

Beide Seiten verbinden sich **ausgehend** zum Server – am Zielrechner sind keine Portfreigaben nötig.

## So funktioniert die Einrichtung

1. **Am Rechner, von dem du zugreifst:** im Browser den Server öffnen und einen **Benutzer anlegen**.
2. **Am Zielrechner:** den **Agent** starten. Es öffnet sich eine Einrichtungsseite. Dort Server-Adresse und **den Benutzernamen eintragen, der Zugriff bekommen soll**. Der Agent zeigt einen 6-stelligen **Freigabecode**.
3. **Im Browser** erscheint der neue Computer als „wartet auf Freigabe“. Code eingeben → freigegeben. Danach ist der Computer jederzeit erreichbar (Autostart beim Anmelden ist optional).

Dass erst der Code vom Zielrechner den Zugriff freigibt, verhindert, dass jemand einfach deinen Benutzernamen am Agent eintippt und sich so auf deine Geräteliste schleust.

## Server

```powershell
.\remoteme-server-windows-amd64.exe -addr :8080 -data remoteme-data.json
```

| Option | Bedeutung |
|---|---|
| `-addr` | Adresse/Port (Standard `:8080`) |
| `-data` | Datei für Benutzer und Geräte (Passwörter als bcrypt-Hash) |
| `-cert`, `-key` | TLS-Zertifikat direkt im Server; alternativ Reverse-Proxy (Caddy, nginx) davor |
| `-open-registration` | Registrierung für alle erlauben. Ohne die Option kann **nur der erste Benutzer** sich registrieren, weitere mit `server adduser <name> <passwort>` |

**Im Internet nur mit TLS betreiben** (Passwort, Sitzungs-Cookie und Bildschirminhalt laufen sonst im Klartext). Beispiel Caddy: `remoteme.example.com { reverse_proxy localhost:8080 }` – WebSockets funktionieren automatisch; `X-Forwarded-For` wird für das Login-Limit ausgewertet.

## Agent

* **Windows:** `remoteme-agent-windows-amd64.exe` doppelklicken. Läuft ohne Fenster im Hintergrund; Einstellungen unter `http://127.0.0.1:17321` (erneutes Starten öffnet die Seite wieder).
* **Linux:** `remoteme-agent-linux-amd64`. Benötigt eine **X11**-Sitzung (Wayland wird nicht unterstützt) und für die Zwischenablage `xclip` oder `xsel`.
* **macOS (13+):** `./build-macos.sh --dmg` baut `remoteMe.app` (Menüleistensymbol, Apple Silicon + Intel). Beim ersten Start fragt macOS nach **Bildschirmaufnahme** und **Bedienungshilfen** (*Systemeinstellungen → Datenschutz & Sicherheit*); danach die App einmal neu starten. Die Einrichtung erfolgt in einem **nativen Fenster** (kein Browser nötig); es öffnet sich beim ersten Start und danach über das Menüleistensymbol → „Einstellungen …“.

Server-Adresse vorbelegen: `.\build.ps1 -Server https://remoteme.example.com` – dann muss am Zielrechner nur noch der Benutzername eingetragen werden.

Logs: `agent.log` im Konfigurationsordner (`%APPDATA%\remoteMe`, `~/.config/remoteMe`, `~/Library/Application Support/remoteMe`).

## Bedienung im Browser

* **Maus/Tastatur** direkt im Bild; **Vollbild** fängt auch Tastenkombinationen ab (Chrome/Edge).
* **Dateien hochladen:** Dateien aus dem Explorer auf das Fenster ziehen → landen im im Dateipanel geöffneten Ordner (Standard: Desktop).
* **Dateien herunterladen:** im Dateipanel Datei doppelklicken oder per Drag & Drop auf den lokalen Desktop/Explorer ziehen (Chrome/Edge; Ordner kommen als ZIP).
* **Zwischenablage:** Text wird bei Strg+V zum Zielrechner gesendet und Änderungen am Zielrechner werden übernommen.
* Mehrere Bildschirme: Auswahl in der Leiste. Qualität lässt sich umschalten.

## Sicherheit

* Passwörter bcrypt-gehasht, Sitzungen per signiertem HttpOnly/SameSite-Strict-Cookie, Login- und Kopplungs-Ratenlimit, Geräte-Geheimnisse nur als Hash gespeichert.
* Nur der Besitzer eines freigegebenen Geräts kann sich verbinden; Gerät entfernen trennt den Agent sofort und löscht dessen Kopplung.
* Der Relay-Server sieht den Datenstrom (kein Ende-zu-Ende-Schutz) – also nur auf einem Server betreiben, dem du vertraust.
* Der Agent ist auf Dauerzugriff nach Freigabe ausgelegt. Eine Zustimmungsabfrage pro Sitzung gibt es noch nicht.

## Bekannte Grenzen

* **Bildübertragung:** H.264 mit Flusssteuerung und adaptiver Bitrate (30 Bilder/s). Auf dem Mac nativ (ScreenCaptureKit + VideoToolbox, keine Zusatzsoftware); unter Windows/Linux über **ffmpeg** (im PATH oder neben dem Agent; Hardware-Encoder nvenc/QSV/AMF werden automatisch genutzt, sonst libx264). Der Browser dekodiert per WebCodecs (Chrome/Edge/Safari/Firefox aktuell; nur über **https** oder localhost). Ohne ffmpeg oder ohne WebCodecs greift automatisch der JPEG-Modus (nur geänderte Bildbereiche).
* Der Agent läuft in der Benutzersitzung: Sperrbildschirm, Anmeldebildschirm und UAC-Dialoge sind nicht sichtbar bzw. bedienbar. Dafür wäre ein Windows-Dienst nötig.
* Der Mauszeiger ist der lokale Zeiger (keine Anzeige der Zeigerform des Zielrechners).
* Windows und Dateiübertragung sind getestet; **Linux- und macOS-Eingabe (Maus/Tastatur) sind implementiert, aber noch nicht auf echten Geräten geprüft.**

## Entwickeln

```powershell
go build ./...           # alles
.\build.ps1              # Release-Binaries in dist\
```
