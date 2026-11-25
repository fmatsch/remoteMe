//go:build windows

package main

import (
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows"
)

var releaseInstance = func() {}

// restartEvent: der Helfer bittet damit den Dienst, sich mit der neuen Programmdatei neu zu starten.
const restartEvent = `Global\remoteMeRestart`

func applyUpdate(data []byte, version string) (func(), error) {
	exe := exePath()
	dir, err := updateDir(exe)
	if err != nil {
		return nil, errors.New("Programmordner ist nicht beschreibbar: " + err.Error())
	}
	defer os.RemoveAll(dir)
	next := filepath.Join(dir, "remoteMe.exe")
	if err := os.WriteFile(next, data, 0o755); err != nil {
		return nil, err
	}
	if err := checkBinary(next, version); err != nil {
		return nil, err
	}
	// Eine laufende Programmdatei lässt sich unter Windows umbenennen, aber nicht überschreiben.
	if err := replaceFile(exe, next); err != nil {
		return nil, err
	}
	return func() {
		if machineMode {
			name, _ := windows.UTF16PtrFromString(restartEvent)
			h, err := windows.OpenEvent(windows.EVENT_MODIFY_STATE, false, name)
			if err != nil {
				log.Println("Neustart des Dienstes:", err)
				return
			}
			windows.SetEvent(h)
			windows.CloseHandle(h)
			return
		}
		releaseInstance()
		c := exec.Command(exe, "-background")
		if err := c.Start(); err != nil {
			log.Println("Neustart:", err)
			return
		}
		os.Exit(0)
	}, nil
}
