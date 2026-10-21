# remoteMe

Selbst gehostetes Fernwartungs-Tool (Go): Relay-Server + Agent (Win/Linux/macOS) + Browser-Viewer.

* Architektur, Protokoll, Teststand, offene Aufgaben: **DEVELOPMENT.md** (zuerst lesen).
* Bedienung: README.md. Webseite: `docs/index.html` (GitHub Pages).
* Bauen: `go build ./...`; Release-Binaries `build.ps1` (Windows) bzw. GitHub Actions.
* Plattformcode steht in `cmd/agent/*_windows.go|_linux.go|_darwin.go`; macOS braucht cgo und wurde noch nie kompiliert.
* Sprache der UI und Kommentare: Deutsch. Keine zusätzlichen Abhängigkeiten ohne Grund.
* Nach Änderungen am Protokoll: `index.html` (Viewer), `agent.go` (`Msg`, `handle`) und DEVELOPMENT.md §4 gemeinsam anpassen.
