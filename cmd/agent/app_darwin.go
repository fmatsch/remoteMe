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
	"fmt"
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
		"pending":    "Wartet auf Freigabe",
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

//export goAppOpenUI
func goAppOpenUI() { openBrowser(fmt.Sprintf("http://127.0.0.1:%d/", uiPort)) }

//export goAppQuit
func goAppQuit() { appStop() }

var appStop = func() {}

// runApp zeigt das Symbol in der Menüleiste und blockiert bis zum Beenden.
func runApp(ctx context.Context, cancel context.CancelFunc, a *Agent) {
	appAgent = a
	appStop = cancel
	go func() {
		<-ctx.Done()
		C.fwQuitApp()
	}()
	C.fwRunApp()
}
