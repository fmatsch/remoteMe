package server

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	pingEvery = 20 * time.Second
	pongWait  = 60 * time.Second
	chunkSize = 128 * 1024

	frameUpload   = 0x04 // Relay -> Agent: [0x04][req u32][daten]
	frameDownload = 0x03 // Agent -> Relay: [0x03][req u32][daten]
)

var upgrader = websocket.Upgrader{
	ReadBufferSize: 64 * 1024, WriteBufferSize: 64 * 1024,
	CheckOrigin: sameOrigin,
}

// wsConn serialisiert Schreibzugriffe.
type wsConn struct {
	ws  *websocket.Conn
	wmu sync.Mutex
}

func (c *wsConn) write(mt int, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	c.ws.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return c.ws.WriteMessage(mt, data)
}

func (c *wsConn) keepalive(done <-chan struct{}) {
	c.ws.SetReadDeadline(time.Now().Add(pongWait))
	c.ws.SetPongHandler(func(string) error { c.ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	go func() {
		t := time.NewTicker(pingEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				c.wmu.Lock()
				err := c.ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				c.wmu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()
}

type xmsg struct {
	data []byte // Binärdaten (Download) ...
	text []byte // ... oder Steuernachricht
}

type waiter struct {
	ch   chan xmsg
	done chan struct{}
}

type agentConn struct {
	srv *Server
	id  string
	wsConn

	vmu    sync.Mutex
	viewer *viewerConn

	xmu     sync.Mutex
	nextReq uint32
	waiters map[uint32]*waiter
	closed  chan struct{}
	once    sync.Once
}

type viewerConn struct {
	wsConn
	user string
}

func (a *agentConn) close() {
	a.once.Do(func() { close(a.closed); a.ws.Close() })
}

func (a *agentConn) sendJSON(v any) error {
	b, _ := json.Marshal(v)
	return a.write(websocket.TextMessage, b)
}

func (a *agentConn) hasViewer() bool {
	a.vmu.Lock()
	defer a.vmu.Unlock()
	return a.viewer != nil
}

func (a *agentConn) newWaiter(buf int) (uint32, *waiter) {
	a.xmu.Lock()
	defer a.xmu.Unlock()
	a.nextReq++
	w := &waiter{ch: make(chan xmsg, buf), done: make(chan struct{})}
	a.waiters[a.nextReq] = w
	return a.nextReq, w
}

func (a *agentConn) dropWaiter(req uint32) {
	a.xmu.Lock()
	if w := a.waiters[req]; w != nil {
		close(w.done)
		delete(a.waiters, req)
	}
	a.xmu.Unlock()
}

func (a *agentConn) deliver(req uint32, m xmsg) {
	a.xmu.Lock()
	w := a.waiters[req]
	a.xmu.Unlock()
	if w == nil {
		return
	}
	select {
	case w.ch <- m:
	case <-w.done:
	case <-time.After(30 * time.Second):
	}
}

// ---- Agent-WebSocket ----

func (s *Server) hAgentWS(w http.ResponseWriter, r *http.Request) {
	id := r.Header.Get("X-Device-Id")
	secret := r.Header.Get("Authorization")
	if len(secret) > 7 && secret[:7] == "Bearer " {
		secret = secret[7:]
	}
	dev, ok := s.st.AuthDevice(id, secret)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	ac := &agentConn{srv: s, id: id, wsConn: wsConn{ws: ws}, waiters: map[uint32]*waiter{}, closed: make(chan struct{})}

	s.mu.Lock()
	old := s.agents[id]
	s.agents[id] = ac
	s.mu.Unlock()
	if old != nil {
		old.close()
	}
	defer func() {
		s.mu.Lock()
		if s.agents[id] == ac {
			delete(s.agents, id)
		}
		s.mu.Unlock()
		ac.close()
		ac.vmu.Lock()
		if ac.viewer != nil {
			ac.viewer.write(websocket.TextMessage, []byte(`{"t":"agent-gone"}`))
			ac.viewer.ws.Close()
		}
		ac.vmu.Unlock()
	}()

	log.Printf("Agent online: %s (%s)", dev.Name, id)
	ac.keepalive(ac.closed)
	ac.sendJSON(map[string]any{"t": "welcome", "approved": dev.Approved})

	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		ws.SetReadDeadline(time.Now().Add(pongWait))
		if !dev.Approved {
			// Während der Freigabe prüfen, ob sie inzwischen erfolgt ist.
			if d, ok := s.st.Device(id); ok {
				dev.Approved = d.Approved
			}
		}
		switch mt {
		case websocket.BinaryMessage:
			if len(data) >= 5 && data[0] == frameDownload {
				ac.deliver(binary.BigEndian.Uint32(data[1:5]), xmsg{data: data[5:]})
				continue
			}
			if dev.Approved {
				ac.toViewer(mt, data)
			}
		case websocket.TextMessage:
			var h struct {
				T   string `json:"t"`
				Req uint32 `json:"req"`
			}
			json.Unmarshal(data, &h)
			switch h.T {
			case "hello":
				var hi struct{ Name, OS string }
				json.Unmarshal(data, &hi)
				s.st.Touch(id, "", hi.OS)
			case "ul-done", "dl-head", "dl-end", "dl-err":
				ac.deliver(h.Req, xmsg{text: data})
			default:
				if dev.Approved {
					ac.toViewer(mt, data)
				}
			}
		}
	}
	s.st.Touch(id, "", "")
	log.Printf("Agent offline: %s (%s)", dev.Name, id)
}

func (a *agentConn) toViewer(mt int, data []byte) {
	a.vmu.Lock()
	v := a.viewer
	a.vmu.Unlock()
	if v != nil {
		if err := v.write(mt, data); err != nil {
			v.ws.Close()
		}
	}
}

// ---- Viewer-WebSocket ----

func (s *Server) hViewWS(w http.ResponseWriter, r *http.Request, user string) {
	id := r.PathValue("id")
	dev, ok := s.st.Device(id)
	if !ok || dev.Owner != user || !dev.Approved {
		http.Error(w, "nicht gefunden", http.StatusNotFound)
		return
	}
	ac := s.agent(id)
	if ac == nil {
		http.Error(w, "Gerät ist offline", http.StatusConflict)
		return
	}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	v := &viewerConn{wsConn: wsConn{ws: ws}, user: user}

	ac.vmu.Lock()
	old := ac.viewer
	ac.viewer = v
	ac.vmu.Unlock()
	if old != nil {
		old.write(websocket.TextMessage, []byte(`{"t":"taken-over"}`))
		old.ws.Close()
	}
	ac.sendJSON(map[string]any{"t": "viewer", "on": true})
	log.Printf("Viewer %s verbunden mit %s", user, dev.Name)

	done := make(chan struct{})
	v.keepalive(done)
	ws.SetReadLimit(4 << 20)
	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		ws.SetReadDeadline(time.Now().Add(pongWait))
		if ac.write(mt, data) != nil {
			break
		}
	}
	close(done)
	ws.Close()
	ac.vmu.Lock()
	if ac.viewer == v {
		ac.viewer = nil
		ac.vmu.Unlock()
		ac.sendJSON(map[string]any{"t": "viewer", "on": false})
	} else {
		ac.vmu.Unlock()
	}
	log.Printf("Viewer %s getrennt von %s", user, dev.Name)
}

// ---- Dateiübertragung über HTTP ----

func (s *Server) ownedAgent(w http.ResponseWriter, r *http.Request, user string) *agentConn {
	id := r.PathValue("id")
	dev, ok := s.st.Device(id)
	if !ok || dev.Owner != user || !dev.Approved {
		jerr(w, 404, "Gerät nicht gefunden")
		return nil
	}
	ac := s.agent(id)
	if ac == nil {
		jerr(w, 409, "Gerät ist offline")
		return nil
	}
	return ac
}

func waitText(w *waiter, d time.Duration) (map[string]any, error) {
	select {
	case m := <-w.ch:
		var out map[string]any
		if m.text == nil {
			return nil, errors.New("unerwartete Daten")
		}
		json.Unmarshal(m.text, &out)
		return out, nil
	case <-time.After(d):
		return nil, errors.New("Zeitüberschreitung")
	}
}

func (s *Server) hUpload(w http.ResponseWriter, r *http.Request, user string) {
	ac := s.ownedAgent(w, r, user)
	if ac == nil {
		return
	}
	q := r.URL.Query()
	req, wt := ac.newWaiter(4)
	defer ac.dropWaiter(req)
	err := ac.sendJSON(map[string]any{"t": "ul-begin", "req": req, "dir": q.Get("dir"), "name": q.Get("name"), "size": r.ContentLength})
	if err != nil {
		jerr(w, 502, "Agent nicht erreichbar")
		return
	}
	buf := make([]byte, 5+chunkSize)
	buf[0] = frameUpload
	binary.BigEndian.PutUint32(buf[1:5], req)
	for {
		n, rerr := io.ReadFull(r.Body, buf[5:])
		if n > 0 {
			if ac.write(websocket.BinaryMessage, buf[:5+n]) != nil {
				jerr(w, 502, "Agent nicht erreichbar")
				return
			}
		}
		select { // Agent meldet vorzeitig einen Fehler (z.B. Datenträger voll)?
		case m := <-wt.ch:
			var res map[string]any
			json.Unmarshal(m.text, &res)
			writeJSON(w, 500, res)
			return
		default:
		}
		if rerr != nil {
			if rerr != io.EOF && rerr != io.ErrUnexpectedEOF {
				ac.sendJSON(map[string]any{"t": "ul-abort", "req": req})
				jerr(w, 400, "Upload abgebrochen")
				return
			}
			break
		}
	}
	ac.sendJSON(map[string]any{"t": "ul-end", "req": req})
	res, err := waitText(wt, 2*time.Minute)
	if err != nil {
		jerr(w, 504, err.Error())
		return
	}
	if e, _ := res["err"].(string); e != "" {
		jerr(w, 500, e)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) hDownload(w http.ResponseWriter, r *http.Request, user string) {
	ac := s.ownedAgent(w, r, user)
	if ac == nil {
		return
	}
	req, wt := ac.newWaiter(64)
	finished := false
	defer func() {
		ac.dropWaiter(req)
		if !finished {
			ac.sendJSON(map[string]any{"t": "dl-cancel", "req": req})
		}
	}()
	if ac.sendJSON(map[string]any{"t": "dl", "req": req, "path": r.URL.Query().Get("path")}) != nil {
		jerr(w, 502, "Agent nicht erreichbar")
		return
	}
	head, err := waitText(wt, 20*time.Second)
	if err != nil {
		jerr(w, 504, err.Error())
		return
	}
	if head["t"] == "dl-err" {
		finished = true
		e, _ := head["err"].(string)
		jerr(w, 404, e)
		return
	}
	name, _ := head["name"].(string)
	if size, _ := head["size"].(float64); size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(int64(size), 10))
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	fl, _ := w.(http.Flusher)
	for {
		select {
		case m := <-wt.ch:
			if m.text != nil {
				var t struct {
					T   string `json:"t"`
					Err string `json:"err"`
				}
				json.Unmarshal(m.text, &t)
				finished = true
				if t.T == "dl-err" {
					panic(http.ErrAbortHandler) // Verbindung hart abbrechen, damit der Browser den Fehler sieht
				}
				return
			}
			if _, err := w.Write(m.data); err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		case <-r.Context().Done():
			return
		case <-ac.closed:
			return
		case <-time.After(60 * time.Second):
			return
		}
	}
}
