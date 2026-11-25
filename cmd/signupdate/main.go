// signupdate erzeugt den Update-Schlüssel und signiert Manifeste (läuft nur beim Herausgeber).
//
//	signupdate keygen <schlüsseldatei>
//	signupdate sign <schlüsseldatei> <version> <ordner> plattform=datei ...
package main

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"remoteme/internal/update"
)

func readKey(path string) ed25519.PrivateKey {
	b, err := os.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(seed) != ed25519.SeedSize {
		log.Fatal("ungültige Schlüsseldatei")
	}
	return ed25519.NewKeyFromSeed(seed)
}

func public(k ed25519.PrivateKey) string {
	return base64.StdEncoding.EncodeToString(k.Public().(ed25519.PublicKey))
}

func main() {
	a := os.Args[1:]
	switch {
	case len(a) == 2 && a[0] == "keygen":
		if _, err := os.Stat(a[1]); err == nil {
			log.Fatal("Schlüsseldatei existiert bereits: ", a[1])
		}
		_, priv, err := ed25519.GenerateKey(nil)
		if err != nil {
			log.Fatal(err)
		}
		os.MkdirAll(filepath.Dir(a[1]), 0o700)
		if err := os.WriteFile(a[1], []byte(base64.StdEncoding.EncodeToString(priv.Seed())+"\n"), 0o600); err != nil {
			log.Fatal(err)
		}
		fmt.Println(public(priv))
	case len(a) == 2 && a[0] == "public":
		fmt.Println(public(readKey(a[1])))
	case len(a) >= 5 && a[0] == "sign":
		key, version, dir := readKey(a[1]), a[2], a[3]
		m := update.Manifest{Version: version, Files: map[string]update.File{}}
		for _, spec := range a[4:] {
			platform, name, ok := strings.Cut(spec, "=")
			if !ok {
				log.Fatal("erwartet plattform=datei: ", spec)
			}
			b, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				log.Fatal(err)
			}
			sum := sha256.Sum256(b)
			m.Files[platform] = update.File{Name: name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(b))}
		}
		data, _ := json.MarshalIndent(m, "", "  ")
		sig := base64.StdEncoding.EncodeToString(ed25519.Sign(key, data))
		if _, err := update.Verify(public(key), data, []byte(sig)); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o644); err != nil {
			log.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "manifest.sig"), []byte(sig+"\n"), 0o644); err != nil {
			log.Fatal(err)
		}
		fmt.Println("signiert:", version)
	default:
		log.Fatal("Aufruf: signupdate keygen|public <schlüssel> | sign <schlüssel> <version> <ordner> plattform=datei ...")
	}
}
