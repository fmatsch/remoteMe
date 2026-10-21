# remoteMe

Selbst gehostetes Fernwartungs-Tool (Go): Relay-Server + Agent (Win/Linux/macOS) + Browser-Viewer.

* Architektur, Protokoll, Teststand, offene Aufgaben: **DEVELOPMENT.md** (zuerst lesen).
* Bedienung: README.md. Webseite: `docs/index.html` (GitHub Pages).
* Bauen: `go build ./...`; Release-Binaries `build.ps1` (Windows) bzw. GitHub Actions.
* Plattformcode steht in `cmd/agent/*_windows.go|_linux.go|_darwin.go`; macOS braucht cgo (ScreenCaptureKit/VideoToolbox, `video_darwin.m`, `app_darwin.m`); App bauen mit `./build-macos.sh`. Server-Deployment: `deploy/deploy.sh` (siehe DEVELOPMENT.md §1b).
* Sprache der UI und Kommentare: Deutsch. Keine zusätzlichen Abhängigkeiten ohne Grund.
* Video: H.264 (Frame `0x02`) mit Flusssteuerung (`ack`) in `video*.go`; JPEG-Kacheln (`0x01`) bleiben Fallback. Nach Änderungen am Protokoll: `index.html` (Viewer), `agent.go` (`Msg`, `handle`) und DEVELOPMENT.md §4 gemeinsam anpassen.
