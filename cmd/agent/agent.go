package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/atotto/clipboard"
	"github.com/gorilla/websocket"
)

const version = "1.1.0"

// Msg ist die gemeinsame JSON-Nachricht zwischen Viewer, Relay und Agent.
type Msg struct {
	T    string  `json:"t"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	B    int     `json:"b"`
	Dx   float64 `json:"dx"`
	Dy   float64 `json:"dy"`
	Code string  `json:"c"`
	Key  string  `json:"k"`
	Down bool    `json:"d"`
	Ctrl bool    `json:"ctrl"`
	Alt  bool    `json:"alt"`
	Meta bool    `json:"meta"`
	S    string  `json:"s"`
	I    int     `json:"i"`
	Q    int     `json:"q"`
	On   bool    `json:"on"`
	Req  uint32  `json:"req"`
	Path string  `json:"path"`
	Name string  `json:"name"`
	Dir  string  `json:"dir"`
	Size int64   `json:"size"`
	E    uint32  `json:"e"`     // Strom-Epoche (Flusssteuerung)
	N    uint32  `json:"n"`     // kumulativ bestätigte Bilder
	Cod  string  `json:"codec"` // gewünschter Codec: "h264" oder "jpeg"

	Approved bool `json:"approved"`
}

type Agent struct {
	mu     sync.Mutex
	cfg    *Config
	status string // "unpaired", "connecting", "pending", "online", "error"
	errMsg string
	wake   chan struct{}

	conn *websocket.Conn
	wmu  sync.Mutex

	viewerOn  bool
	capCancel context.CancelFunc
	disp      atomic.Int32
	quality   atomic.Int32
	codec     atomic.Int32
	keyReq    atomic.Bool
	fl        flow
	in        Input

	xfer  *transfers
	clip  string
	clipM sync.Mutex
}

func newAgent() *Agent {
	a := &Agent{cfg: loadConfig(), wake: make(chan struct{}, 1), in: newInput(), xfer: newTransfers()}
	a.quality.Store(70)
	if a.cfg == nil {
		a.status = "unpaired"
	} else {
		a.status = "connecting"
	}
	return a
}

func (a *Agent) config() *Config {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg == nil {
		return nil
	}
	c := *a.cfg
	return &c
}

func (a *Agent) setStatus(s, msg string) {
	a.mu.Lock()
	a.status, a.errMsg = s, msg
	a.mu.Unlock()
}

func (a *Agent) setConfig(c *Config) {
	a.mu.Lock()
	a.cfg = c
	if c == nil {
		a.status = "unpaired"
	} else {
		a.status, a.errMsg = "connecting", ""
	}
	a.mu.Unlock()
	saveConfig(c)
	select {
	case a.wake <- struct{}{}:
	default:
	}
	if conn := a.currentConn(); conn != nil {
		conn.Close()
	}
}

func (a *Agent) currentConn() *websocket.Conn {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	return a.conn
}

func (a *Agent) sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-a.wake:
	case <-time.After(d):
	}
}

func (a *Agent) run(ctx context.Context) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		cfg := a.config()
		if cfg == nil {
			a.sleep(ctx, time.Hour)
			continue
		}
		began := time.Now()
		err := a.session(ctx, cfg)
		a.stopSession()
		if errors.Is(err, errUnauthorized) {
			log.Println("Server kennt das Gerät nicht mehr - Kopplung wird zurückgesetzt")
			a.setConfig(nil)
			a.setStatus("unpaired", "Die Kopplung wurde vom Server aufgehoben. Bitte neu verbinden.")
			continue
		}
		if err != nil {
			log.Println("Verbindung:", err)
			a.setStatus("error", err.Error())
		}
		if time.Since(began) > time.Minute {
			backoff = 2 * time.Second
		}
		a.sleep(ctx, backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

var errUnauthorized = errors.New("unauthorized")

func wsURL(server string) (string, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" {
		return "", errors.New("ungültige Server-Adresse")
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	default:
		return "", errors.New("Server-Adresse muss mit http:// oder https:// beginnen")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/ws/agent"
	return u.String(), nil
}

func (a *Agent) session(ctx context.Context, cfg *Config) error {
	u, err := wsURL(cfg.Server)
	if err != nil {
		return err
	}
	h := http.Header{"X-Device-Id": {cfg.ID}, "Authorization": {"Bearer " + cfg.Secret}}
	d := websocket.Dialer{HandshakeTimeout: 15 * time.Second, Proxy: http.ProxyFromEnvironment, ReadBufferSize: 64 << 10, WriteBufferSize: 64 << 10}
	ws, resp, err := d.DialContext(ctx, u, h)
	if err != nil {
		if resp != nil && resp.StatusCode == http.StatusUnauthorized {
			return errUnauthorized
		}
		return err
	}
	defer ws.Close()
	a.wmu.Lock()
	a.conn = ws
	a.wmu.Unlock()

	ws.SetReadLimit(8 << 20)
	ws.SetReadDeadline(time.Now().Add(60 * time.Second))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(60 * time.Second)); return nil })
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				a.wmu.Lock()
				err := ws.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second))
				a.wmu.Unlock()
				if err != nil {
					return
				}
			}
		}
	}()

	host, _ := os.Hostname()
	a.sendJSON(map[string]any{"t": "hello", "name": host, "os": runtime.GOOS, "ver": version})
	log.Println("Mit Server verbunden")

	for {
		mt, data, err := ws.ReadMessage()
		if err != nil {
			return err
		}
		ws.SetReadDeadline(time.Now().Add(60 * time.Second))
		if mt == websocket.BinaryMessage {
			if len(data) >= 5 && data[0] == 0x04 {
				a.xfer.chunk(binary.BigEndian.Uint32(data[1:5]), data[5:], a)
			}
			continue
		}
		var m Msg
		if json.Unmarshal(data, &m) != nil {
			continue
		}
		a.handle(ctx, &m)
	}
}

func (a *Agent) stopSession() {
	a.wmu.Lock()
	a.conn = nil
	a.wmu.Unlock()
	a.viewer(false)
	a.xfer.reset()
}

func (a *Agent) sendJSON(v any) {
	b, _ := json.Marshal(v)
	a.send(websocket.TextMessage, b)
}

func (a *Agent) send(mt int, b []byte) error {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	if a.conn == nil {
		return errors.New("nicht verbunden")
	}
	a.conn.SetWriteDeadline(time.Now().Add(30 * time.Second))
	return a.conn.WriteMessage(mt, b)
}

func (a *Agent) handle(ctx context.Context, m *Msg) {
	switch m.T {
	case "welcome":
		if m.Approved {
			a.setStatus("online", "")
		} else {
			a.setStatus("pending", "")
		}
	case "approved":
		a.mu.Lock()
		if a.cfg != nil {
			a.cfg.Approved, a.cfg.Code = true, ""
			saveConfig(a.cfg)
		}
		a.mu.Unlock()
		a.setStatus("online", "")
	case "revoked":
		a.setConfig(nil)
		a.setStatus("unpaired", "Dieses Gerät wurde in der Weboberfläche entfernt.")
	case "viewer":
		a.viewer(m.On)
	case "display":
		a.disp.Store(int32(m.I))
	case "opt":
		if m.Q >= 20 && m.Q <= 95 {
			a.quality.Store(int32(m.Q))
		}
		switch m.Cod {
		case "h264":
			a.codec.Store(codecH264)
		case "jpeg":
			a.codec.Store(codecJPEG)
		}
	case "ack":
		a.fl.ack(m.E, m.N)
	case "key":
		a.keyReq.Store(true)
	case "mm":
		a.in.Move(a.abs(m.X, m.Y))
	case "md", "mu":
		x, y := a.abs(m.X, m.Y)
		a.in.Button(m.B, m.T == "md", x, y)
	case "wh":
		a.in.Wheel(m.Dx, m.Dy)
	case "k":
		a.in.Key(m.Code, m.Key, m.Down, m.Ctrl || m.Alt || m.Meta)
	case "clip":
		a.clipM.Lock()
		a.clip = m.S
		a.clipM.Unlock()
		clipboard.WriteAll(m.S)
	case "ls":
		p := m.Path
		go func() { a.sendJSON(listDir(p)) }() // darf die Eingabeverarbeitung nicht blockieren
	case "ul-begin":
		a.xfer.begin(m, a)
	case "ul-end":
		a.xfer.end(m.Req, a)
	case "ul-abort":
		a.xfer.abort(m.Req)
	case "dl":
		a.xfer.download(ctx, m, a)
	case "dl-cancel":
		a.xfer.cancel(m.Req)
	}
}

func (a *Agent) viewerActive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.viewerOn
}

func (a *Agent) viewer(on bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.capCancel != nil {
		a.capCancel()
		a.capCancel = nil
	}
	a.viewerOn = on
	if on {
		ctx, cancel := context.WithCancel(context.Background())
		a.capCancel = cancel
		go a.captureLoop(ctx)
		go a.clipboardLoop(ctx)
	} else {
		a.in.ReleaseAll()
	}
}

func (a *Agent) clipboardLoop(ctx context.Context) {
	t := time.NewTicker(800 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s, err := clipboard.ReadAll()
			if err != nil || len(s) > 1<<20 {
				continue
			}
			a.clipM.Lock()
			changed := s != a.clip
			a.clip = s
			a.clipM.Unlock()
			if changed && s != "" {
				a.sendJSON(map[string]any{"t": "clip", "s": s})
			}
		}
	}
}
