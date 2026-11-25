package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"remoteme/internal/update"
)

func platformKey() string {
	if runtime.GOOS == "darwin" {
		return "darwin-universal"
	}
	return runtime.GOOS + "-" + runtime.GOARCH
}

func (a *Agent) updateStatus(s string) {
	log.Println("Update:", s)
	a.sendJSON(map[string]any{"t": "update-status", "s": s})
}

// startUpdate lädt das signierte Paket vom eigenen Server und ersetzt das Programm. Die Kopplung
// bleibt erhalten. Ausgelöst nur durch den Besitzer (Server-Nachricht "update").
func (a *Agent) startUpdate() {
	if !a.updating.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer a.updating.Store(false)
		if err := a.runUpdate(); err != nil {
			a.updateStatus("Fehlgeschlagen: " + err.Error())
		}
	}()
}

func httpGet(ctx context.Context, url string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Server antwortet mit %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		err = errors.New("Datei zu groß")
	}
	return b, err
}

func (a *Agent) runUpdate() error {
	cfg := a.config()
	if cfg == nil {
		return errors.New("nicht gekoppelt")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	base := strings.TrimRight(cfg.Server, "/") + "/update/"
	manifest, err := httpGet(ctx, base+"manifest.json", 64<<10)
	if err != nil {
		return err
	}
	sig, err := httpGet(ctx, base+"manifest.sig", 4<<10)
	if err != nil {
		return err
	}
	m, err := update.Verify(updatePublicKey, manifest, sig)
	if err != nil {
		return err
	}
	if !update.Newer(m.Version, version) { // schützt auch vor dem Wiedereinspielen alter Pakete
		return fmt.Errorf("bereits aktuell (%s)", version)
	}
	f, ok := m.Files[platformKey()]
	if !ok {
		return errors.New("kein Paket für dieses System")
	}
	a.updateStatus("Lade Version " + m.Version + " …")
	data, err := httpGet(ctx, base+f.Name, f.Size)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if int64(len(data)) != f.Size || !strings.EqualFold(hex.EncodeToString(sum[:]), f.SHA256) {
		return errors.New("Prüfsumme stimmt nicht")
	}
	a.updateStatus("Installiere Version " + m.Version + " …")
	restart, err := applyUpdate(data, m.Version)
	if err != nil {
		return err
	}
	a.updateStatus("Installiert, starte neu …")
	time.Sleep(500 * time.Millisecond) // Meldung noch zustellen
	restart()
	return nil
}

// checkBinary startet die neue Programmdatei probehalber: erst wenn sie läuft und die erwartete
// Version meldet, wird die alte ersetzt.
func checkBinary(path, want string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, "-version")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("neue Version startet nicht: %w", err)
	}
	if got := strings.TrimSpace(string(out)); got != want {
		return fmt.Errorf("neue Version meldet %q statt %q", got, want)
	}
	return nil
}

// replaceFile tauscht path gegen next; die alte Datei bleibt als .old zum Zurückrollen.
func replaceFile(path, next string) error {
	old := path + ".old"
	if err := os.RemoveAll(old); err != nil {
		old = fmt.Sprintf("%s.%d.old", path, time.Now().Unix())
	}
	if err := os.Rename(path, old); err != nil {
		return err
	}
	if err := os.Rename(next, path); err != nil {
		os.Rename(old, path)
		return err
	}
	return nil
}

// updateDir ist ein Arbeitsordner neben dem Ziel (gleiches Laufwerk, damit Umbenennen gelingt).
func updateDir(target string) (string, error) {
	return os.MkdirTemp(filepath.Dir(target), ".remoteme-update-*")
}
