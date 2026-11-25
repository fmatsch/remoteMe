package main

import (
	"context"
	"time"
)

const statsInterval = 10 * time.Second

type diskStat struct {
	Name  string `json:"n"`
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

// sample: Rohwerte einer Messung. CPU- und Netzwerkwerte sind laufende Zähler.
type sample struct {
	cpuBusy, cpuTotal uint64
	memUsed, memTotal uint64
	disks             []diskStat
	rx, tx            uint64
}

// statsLoop meldet dem Server regelmäßig die Auslastung, auch ohne laufende Sitzung.
func (a *Agent) statsLoop(ctx context.Context) {
	prev, at := takeSample(), time.Now()
	t := time.NewTicker(statsInterval)
	defer t.Stop()
	first := time.After(2 * time.Second)
	for {
		select {
		case <-ctx.Done():
			return
		case <-first:
		case <-t.C:
		}
		cur, now := takeSample(), time.Now()
		secs := now.Sub(at).Seconds()
		msg := map[string]any{"t": "stats", "memUsed": cur.memUsed, "memTotal": cur.memTotal, "disks": cur.disks}
		if dt := cur.cpuTotal - prev.cpuTotal; cur.cpuTotal > prev.cpuTotal {
			msg["cpu"] = 100 * float64(cur.cpuBusy-prev.cpuBusy) / float64(dt)
		}
		if secs > 0 && cur.rx >= prev.rx && cur.tx >= prev.tx {
			msg["rx"], msg["tx"] = float64(cur.rx-prev.rx)/secs, float64(cur.tx-prev.tx)/secs // Byte pro Sekunde
		}
		prev, at = cur, now
		a.sendJSON(msg)
	}
}
