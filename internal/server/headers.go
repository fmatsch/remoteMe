package server

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
)

// contentSecurityPolicy erlaubt nur das eingebettete Skript der Oberfläche (per Hash). Eingeschleuste
// Skripte oder Ereignis-Attribute führt der Browser damit nicht aus.
var contentSecurityPolicy = func() string {
	page, err := webFS.ReadFile("web/index.html")
	if err != nil {
		panic(err)
	}
	// Browser rechnen den Hash über den Inhalt mit LF-Zeilenenden.
	html := strings.ReplaceAll(string(page), "\r\n", "\n")
	_, rest, _ := strings.Cut(html, "<script>")
	script, _, ok := strings.Cut(rest, "</script>")
	if !ok {
		panic("index.html: <script> nicht gefunden")
	}
	sum := sha256.Sum256([]byte(script))
	return "default-src 'self'; script-src 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'; " +
		"style-src 'unsafe-inline'; img-src 'self' blob: data:; connect-src 'self'; object-src 'none'; " +
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}()
