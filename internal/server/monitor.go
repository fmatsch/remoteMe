package server

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"remoteme/internal/update"
)

const historyLen = 60 // bei 10 s Abstand: die letzten 10 Minuten

type diskStat struct {
	Name  string `json:"n"`
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

type stats struct {
	CPU      *float64   `json:"cpu,omitempty"` // Prozent
	MemUsed  uint64     `json:"memUsed"`
	MemTotal uint64     `json:"memTotal"`
	Disks    []diskStat `json:"disks"`
	Rx       *float64   `json:"rx,omitempty"` // Byte pro Sekunde
	Tx       *float64   `json:"tx,omitempty"`
}

// agentInfo: was ein verbundener Agent über sich meldet (nur im Speicher).
type agentInfo struct {
	mu                        sync.Mutex
	version, platform, status string
	service                   bool
	last                      *stats
	cpuHist, memHist          []int
}

func finite(p *float64, max float64) *float64 {
	if p == nil || math.IsNaN(*p) || math.IsInf(*p, 0) {
		return nil
	}
	v := math.Round(math.Min(math.Max(*p, 0), max)*10) / 10
	return &v
}

// short begrenzt vom Agent gelieferte Texte.
func short(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

func (i *agentInfo) hello(data []byte) {
	var h struct {
		Ver     string `json:"ver"`
		Arch    string `json:"arch"`
		Service bool   `json:"service"`
	}
	json.Unmarshal(data, &h)
	i.mu.Lock()
	defer i.mu.Unlock()
	i.version, i.platform, i.service = short(h.Ver, 20), short(h.Arch, 30), h.Service
}

// Alles Gemeldete ist nicht vertrauenswürdig und wird begrenzt.
func (i *agentInfo) addStats(data []byte) {
	var s stats
	if json.Unmarshal(data, &s) != nil {
		return
	}
	s.CPU, s.Rx, s.Tx = finite(s.CPU, 100), finite(s.Rx, 1e12), finite(s.Tx, 1e12)
	if len(s.Disks) > 8 {
		s.Disks = s.Disks[:8]
	}
	for k := range s.Disks {
		s.Disks[k].Name = short(s.Disks[k].Name, 40)
		s.Disks[k].Used = min(s.Disks[k].Used, s.Disks[k].Total)
	}
	s.MemUsed = min(s.MemUsed, s.MemTotal)
	cpu, mem := -1, 0
	if s.CPU != nil {
		cpu = int(math.Round(*s.CPU))
	}
	if s.MemTotal > 0 {
		mem = int(float64(s.MemUsed) / float64(s.MemTotal) * 100)
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.last = &s
	if cpu >= 0 {
		i.cpuHist = append(i.cpuHist, cpu)
		i.memHist = append(i.memHist, mem)
		if len(i.cpuHist) > historyLen {
			i.cpuHist, i.memHist = i.cpuHist[1:], i.memHist[1:]
		}
	}
}

func (i *agentInfo) setStatus(data []byte) {
	var m struct {
		S string `json:"s"`
	}
	json.Unmarshal(data, &m)
	i.mu.Lock()
	defer i.mu.Unlock()
	i.status = short(m.S, 200)
}

// manifest liest das aktuell bereitgestellte Update (die Signatur prüfen die Agents selbst).
func (s *Server) manifest() *update.Manifest {
	if s.cfg.UpdateDir == "" {
		return nil
	}
	b, err := os.ReadFile(filepath.Join(s.cfg.UpdateDir, "manifest.json"))
	if err != nil {
		return nil
	}
	var m update.Manifest
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	return &m
}

// hUpdateFile liefert signierte Pakete an Agents; öffentlich, weil der Inhalt signiert ist.
func (s *Server) hUpdateFile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	if s.cfg.UpdateDir == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		http.NotFound(w, r)
		return
	}
	p := filepath.Join(s.cfg.UpdateDir, name)
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, p)
}

// hUpdate bittet den eigenen, verbundenen Computer, sich zu aktualisieren.
func (s *Server) hUpdate(w http.ResponseWriter, r *http.Request, user string) {
	ac := s.ownedAgent(w, r, user)
	if ac == nil {
		return
	}
	log.Printf("Update angefordert: Gerät %q durch %q von %s", r.PathValue("id"), user, clientIP(r))
	ac.info.setStatus([]byte(`{"s":"Update angefordert …"}`))
	if ac.sendJSON(map[string]any{"t": "update"}) != nil {
		jerr(w, 502, "Agent nicht erreichbar")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
