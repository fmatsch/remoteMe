//go:build !windows

package main

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

// releaseInstance gibt vor dem Neustart die Sperre (lokaler Port) frei; in main gesetzt.
var releaseInstance = func() {}

// spawnLater startet das Programm kurz nach dem eigenen Ende neu, unabhängig von diesem Prozess.
func spawnLater(script string, args ...string) {
	c := exec.Command("/bin/sh", append([]string{"-c", "sleep 2; " + script}, args...)...)
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	releaseInstance()
	c.Start()
	os.Exit(0)
}

func applyUpdate(data []byte, version string) (func(), error) {
	if runtime.GOOS == "darwin" {
		app := filepath.Dir(filepath.Dir(filepath.Dir(exePath())))
		if !strings.HasSuffix(app, ".app") {
			return nil, errors.New("läuft nicht aus remoteMe.app")
		}
		return applyMacUpdate(app, data, version)
	}
	exe := exePath()
	dir, err := updateDir(exe)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	next := filepath.Join(dir, "agent")
	if err := os.WriteFile(next, data, 0o755); err != nil {
		return nil, err
	}
	if err := checkBinary(next, version); err != nil {
		return nil, err
	}
	if err := replaceFile(exe, next); err != nil {
		return nil, err
	}
	return func() { spawnLater(`exec "$0" -background`, exe) }, nil
}

// requirement liefert die Signaturanforderung eines Programmpakets ("designated => ...").
func requirement(app string) string {
	out, _ := exec.Command("/usr/bin/codesign", "-d", "-r-", app).CombinedOutput()
	for _, l := range strings.Split(string(out), "\n") {
		if _, r, ok := strings.Cut(l, "designated => "); ok {
			return r
		}
	}
	return ""
}

// applyMacUpdate ersetzt das ganze Programmpaket (.app).
func applyMacUpdate(app string, zip []byte, version string) (func(), error) {
	dir, err := updateDir(app)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	archive := filepath.Join(dir, "app.zip")
	if err := os.WriteFile(archive, zip, 0o600); err != nil {
		return nil, err
	}
	if out, err := exec.Command("/usr/bin/ditto", "-x", "-k", archive, dir).CombinedOutput(); err != nil {
		return nil, errors.New("Entpacken: " + string(bytes.TrimSpace(out)))
	}
	next := filepath.Join(dir, "remoteMe.app")
	if err := exec.Command("/usr/bin/codesign", "--verify", "--deep", "--strict", next).Run(); err != nil {
		return nil, errors.New("Programmpaket ist beschädigt (Signatur)")
	}
	// Mit Zertifikat signierte Installationen nehmen nur Pakete mit demselben Zertifikat an;
	// so bleiben auch die macOS-Freigaben erhalten.
	if cur := requirement(app); strings.Contains(cur, "certificate") && requirement(next) != cur {
		return nil, errors.New("Programmpaket ist mit einem anderen Zertifikat signiert")
	}
	if err := checkBinary(filepath.Join(next, "Contents", "MacOS", "remoteMe"), version); err != nil {
		return nil, err
	}
	if err := replaceFile(app, next); err != nil {
		return nil, err
	}
	return func() { spawnLater(`exec /usr/bin/open -n "$0" --args -background`, app) }, nil
}
