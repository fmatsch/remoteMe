// Package update beschreibt signierte Programmpakete: Manifest, Signaturprüfung, Versionsvergleich.
package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// File ist das Paket einer Plattform ("windows-amd64", "darwin-universal", "linux-amd64" ...).
type File struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Manifest liegt als manifest.json auf dem Server; manifest.sig enthält die Ed25519-Signatur
// (Base64) über genau diese Bytes.
type Manifest struct {
	Version string          `json:"version"`
	Files   map[string]File `json:"files"`
}

// MaxSize begrenzt ein Paket.
const MaxSize = 200 << 20

// Verify prüft die Signatur und liest das Manifest.
func Verify(publicKey string, manifest, signature []byte) (*Manifest, error) {
	key, err := base64.StdEncoding.DecodeString(publicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return nil, errors.New("dieses Programm kennt keinen Update-Schlüssel")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signature)))
	if err != nil || !ed25519.Verify(ed25519.PublicKey(key), manifest, sig) {
		return nil, errors.New("Signatur des Updates ist ungültig")
	}
	var m Manifest
	if err := json.Unmarshal(manifest, &m); err != nil {
		return nil, err
	}
	for _, f := range m.Files {
		if f.Name == "" || strings.ContainsAny(f.Name, `/\`) || strings.HasPrefix(f.Name, ".") || f.Size <= 0 || f.Size > MaxSize {
			return nil, errors.New("Manifest enthält ungültige Angaben")
		}
	}
	return &m, nil
}

// Newer meldet, ob Version a neuer als b ist (Zahlen durch Punkte getrennt).
func Newer(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) || i < len(pb); i++ {
		var x, y int
		if i < len(pa) {
			x, _ = strconv.Atoi(pa[i])
		}
		if i < len(pb) {
			y, _ = strconv.Atoi(pb[i])
		}
		if x != y {
			return x > y
		}
	}
	return false
}
