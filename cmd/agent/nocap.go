package main

import (
	"context"
	"runtime"
	"time"
)

// setCapError merkt sich, warum gerade kein Bild aufgenommen werden kann ("" = kein Problem).
func (a *Agent) setCapError(s string) { a.capErr.Store(&s) }

// watchCapture meldet dem Viewer, wenn kein Bild ankommen kann, statt ihn bei "Verbinde ..." zu lassen.
func (a *Agent) watchCapture(ctx context.Context) {
	t := time.NewTicker(4 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		var why string
		if p := a.capErr.Load(); p != nil {
			why = *p
		}
		// Ohne Fehler und mit gesendeten Bildern ist alles in Ordnung (ein ruhendes Bild sendet nichts).
		if why == "" && a.fl.sent.Load() > 0 {
			continue
		}
		msg := "Der Zielrechner kann seinen Bildschirm gerade nicht aufnehmen."
		if why != "" {
			msg += " (" + why + ")"
		}
		if runtime.GOOS == "windows" && !machineMode {
			msg += " Häufige Ursachen unter Windows: Rechner gesperrt, nicht angemeldet oder Remotedesktop getrennt bzw. minimiert. Abhilfe: remoteMe dort als Dienst installieren."
		} else if runtime.GOOS == "darwin" {
			msg += " Auf dem Mac: Rechner entsperrt? Freigabe für die Bildschirmaufnahme erteilt?"
		}
		a.sendJSON(map[string]any{"t": "nocap", "msg": msg})
	}
}
