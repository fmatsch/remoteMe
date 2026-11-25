//go:build darwin

package main

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=13.0
#cgo LDFLAGS: -mmacosx-version-min=13.0 -framework Cocoa -framework ApplicationServices
#include <stdlib.h>
#include "app_darwin.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
)

func init() { runtime.LockOSThread() } // Cocoa muss auf dem Hauptthread laufen

var appAgent *Agent

//export goAppStatus
func goAppStatus() *C.char {
	a := appAgent
	if a == nil {
		return nil
	}
	a.mu.Lock()
	st, msg := a.status, a.errMsg
	a.mu.Unlock()
	text := map[string]string{
		"unpaired":   "Nicht eingerichtet",
		"connecting": "Verbindet …",
		"pending":    "Verfügbar – wartet auf Übernahme",
		"online":     "Verbunden",
		"error":      "Keine Verbindung",
	}[st]
	if st == "online" && a.fl.epoch.Load() > 0 && a.viewerActive() {
		text = "Sitzung aktiv"
	}
	if st == "error" && msg != "" {
		text += ": " + msg
	}
	return C.CString(fmt.Sprintf("remoteMe – %s", text))
}

// goWinState liefert den Zustand für das native Fenster als JSON.
//
//export goWinState
func goWinState() *C.char {
	a := appAgent
	if a == nil {
		return nil
	}
	a.mu.Lock()
	st, em, cfg := a.status, a.errMsg, a.cfg
	a.mu.Unlock()
	host, _ := os.Hostname()
	out := map[string]any{"status": st, "error": em, "autostart": autostartEnabled(), "defaultServer": defaultServer, "hostname": host, "role": loadRole()}
	if cfg != nil {
		out["server"], out["username"], out["name"] = cfg.Server, cfg.Username, cfg.Name
		if !cfg.Approved {
			out["code"] = cfg.Code
		}
	}
	b, _ := json.Marshal(out)
	return C.CString(string(b))
}

// goWinPair koppelt den Computer; Rückgabe NULL = Erfolg, sonst Fehlertext.
//
//export goWinPair
func goWinPair(server, name, password *C.char, autostart C.int) *C.char {
	if err := appAgent.pairDevice(C.GoString(server), C.GoString(name), C.GoString(password), autostart != 0); err != nil {
		return C.CString(err.Error())
	}
	return nil
}

//export goWinRole
func goWinRole() *C.char { return C.CString(loadRole()) }

//export goWinSetRole
func goWinSetRole(role *C.char) { saveRole(C.GoString(role)) }

//export goWinPaired
func goWinPaired() C.int {
	if appAgent != nil && appAgent.config() != nil {
		return 1
	}
	return 0
}

//export goWinUnpair
func goWinUnpair() {
	appAgent.setConfig(nil)
	appAgent.setStatus("unpaired", "")
}

//export goWinAutostart
func goWinAutostart(on C.int) { setAutostart(on != 0) }

//export goAppQuit
func goAppQuit() { appStop() }

var appStop = func() {}

// runApp zeigt das Symbol in der Menüleiste und blockiert bis zum Beenden.
func runApp(ctx context.Context, cancel context.CancelFunc, a *Agent, showSetup bool) {
	appAgent = a
	appStop = cancel
	go func() {
		<-ctx.Done()
		C.fwQuitApp()
	}()
	if showSetup {
		C.fwRunApp(1)
	} else {
		C.fwRunApp(0)
	}
}

// nativeUI: Einrichtung über ein Cocoa-Fenster statt über den Browser.
const nativeUI = true

func showWindow() { C.fwShowWindow() }
