package server

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

//go:embed web
var webFS embed.FS

type Config struct {
	Addr             string
	DataFile         string
	CertFile         string
	KeyFile          string
	OpenRegistration bool
}

type Server struct {
	cfg    Config
	st     *Store
	mu     sync.Mutex
	agents map[string]*agentConn
	lim    *limiter
}

func New(cfg Config) (*Server, error) {
	st, err := OpenStore(cfg.DataFile)
	if err != nil {
		return nil, err
	}
	return &Server{cfg: cfg, st: st, agents: map[string]*agentConn{}, lim: newLimiter()}, nil
}

func (s *Server) Store() *Store { return s.st }

func (s *Server) Run(ctx context.Context) error {
	web, _ := fs.Sub(webFS, "web")
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(web))

	mux.HandleFunc("POST /api/register", s.hRegister)
	mux.HandleFunc("POST /api/login", s.hLogin)
	mux.HandleFunc("POST /api/logout", s.hLogout)
	mux.HandleFunc("GET /api/me", s.auth(s.hMe))
	mux.HandleFunc("GET /api/devices", s.auth(s.hDevices))
	mux.HandleFunc("POST /api/devices/{id}/approve", s.auth(s.hApprove))
	mux.HandleFunc("DELETE /api/devices/{id}", s.auth(s.hDelete))
	mux.HandleFunc("POST /api/devices/{id}/upload", s.auth(s.hUpload))
	mux.HandleFunc("GET /api/devices/{id}/download", s.auth(s.hDownload))
	mux.HandleFunc("POST /api/agent/pair", s.hPair)
	mux.HandleFunc("GET /ws/agent", s.hAgentWS)
	mux.HandleFunc("GET /ws/view/{id}", s.auth(s.hViewWS))

	srv := &http.Server{Addr: s.cfg.Addr, Handler: secure(mux), ReadHeaderTimeout: 15 * time.Second}
	go s.housekeeping(ctx)
	go func() {
		<-ctx.Done()
		sc, c := context.WithTimeout(context.Background(), 3*time.Second)
		defer c()
		srv.Shutdown(sc)
	}()
	var err error
	if s.cfg.CertFile != "" {
		log.Printf("Server (HTTPS) auf %s", s.cfg.Addr)
		err = srv.ListenAndServeTLS(s.cfg.CertFile, s.cfg.KeyFile)
	} else {
		log.Printf("Server (HTTP) auf %s - für den Internetbetrieb TLS verwenden (-cert/-key oder Reverse-Proxy)", s.cfg.Addr)
		err = srv.ListenAndServe()
	}
	if err == http.ErrServerClosed {
		return nil
	}
	return err
}

func secure(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") && r.Method != http.MethodGet {
			// einfacher CSRF-Schutz zusätzlich zu SameSite=Strict
			if r.Header.Get("X-FW") == "" && r.URL.Path != "/api/agent/pair" {
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		h.ServeHTTP(w, r)
	})
}

func (s *Server) housekeeping(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, id := range s.st.ExpirePending() {
				if ac := s.agent(id); ac != nil {
					ac.close()
				}
			}
			s.lim.gc()
		}
	}
}

// ---- Helfer ----

type ctxKey struct{}

func (s *Server) auth(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("fw_session")
		if err != nil {
			http.Error(w, "nicht angemeldet", http.StatusUnauthorized)
			return
		}
		user, ok := s.st.VerifyToken(c.Value)
		if !ok {
			http.Error(w, "nicht angemeldet", http.StatusUnauthorized)
			return
		}
		h(w, r, user)
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func jerr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func clientIP(r *http.Request) string {
	if f := r.Header.Get("X-Forwarded-For"); f != "" {
		return strings.TrimSpace(strings.Split(f, ",")[0])
	}
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return h
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

// ---- Konto ----

type cred struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) setSession(w http.ResponseWriter, r *http.Request, user string) {
	http.SetCookie(w, &http.Cookie{
		Name: "fw_session", Value: s.st.MakeToken(user), Path: "/", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r), MaxAge: int(sessionTTL.Seconds()),
	})
}

func (s *Server) hRegister(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.OpenRegistration && s.st.UserCount() > 0 {
		jerr(w, 403, "Registrierung ist deaktiviert")
		return
	}
	if !s.lim.allow("reg:"+clientIP(r), 10, time.Hour) {
		jerr(w, 429, "Zu viele Versuche")
		return
	}
	var c cred
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&c) != nil {
		jerr(w, 400, "Ungültige Anfrage")
		return
	}
	if err := s.st.AddUser(c.Username, c.Password); err != nil {
		jerr(w, 400, err.Error())
		return
	}
	s.setSession(w, r, NormalizeName(c.Username))
	writeJSON(w, 200, map[string]string{"user": NormalizeName(c.Username)})
}

func (s *Server) hLogin(w http.ResponseWriter, r *http.Request) {
	var c cred
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&c) != nil {
		jerr(w, 400, "Ungültige Anfrage")
		return
	}
	key := "login:" + NormalizeName(c.Username) + "@" + clientIP(r)
	if !s.lim.allow(key, 5, 10*time.Minute) {
		jerr(w, 429, "Zu viele Fehlversuche, bitte später erneut versuchen")
		return
	}
	if !s.st.CheckUser(c.Username, c.Password) {
		jerr(w, 401, "Benutzername oder Passwort falsch")
		return
	}
	s.lim.reset(key)
	s.setSession(w, r, NormalizeName(c.Username))
	writeJSON(w, 200, map[string]string{"user": NormalizeName(c.Username)})
}

func (s *Server) hLogout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "fw_session", Path: "/", MaxAge: -1})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) hMe(w http.ResponseWriter, r *http.Request, user string) {
	writeJSON(w, 200, map[string]string{"user": user})
}

// ---- Geräte ----

func (s *Server) hDevices(w http.ResponseWriter, r *http.Request, user string) {
	type dev struct {
		ID       string    `json:"id"`
		Name     string    `json:"name"`
		OS       string    `json:"os"`
		Approved bool      `json:"approved"`
		Online   bool      `json:"online"`
		Busy     bool      `json:"busy"`
		LastSeen time.Time `json:"lastSeen"`
	}
	out := []dev{}
	for _, d := range s.st.DevicesOf(user) {
		x := dev{ID: d.ID, Name: d.Name, OS: d.OS, Approved: d.Approved, LastSeen: d.LastSeen}
		if ac := s.agent(d.ID); ac != nil {
			x.Online, x.Busy = true, ac.hasViewer()
		}
		out = append(out, x)
	}
	writeJSON(w, 200, out)
}

func (s *Server) hApprove(w http.ResponseWriter, r *http.Request, user string) {
	var b struct {
		Code string `json:"code"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&b)
	id := r.PathValue("id")
	if err := s.st.Approve(user, id, b.Code); err != nil {
		jerr(w, 400, err.Error())
		return
	}
	if ac := s.agent(id); ac != nil {
		ac.sendJSON(map[string]any{"t": "approved"})
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) hDelete(w http.ResponseWriter, r *http.Request, user string) {
	id := r.PathValue("id")
	if !s.st.DeleteDevice(user, id) {
		jerr(w, 404, "Gerät nicht gefunden")
		return
	}
	if ac := s.agent(id); ac != nil {
		ac.sendJSON(map[string]any{"t": "revoked"})
		ac.close()
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Agent beantragt die Kopplung an einen Benutzer.
func (s *Server) hPair(w http.ResponseWriter, r *http.Request) {
	if !s.lim.allow("pair:"+clientIP(r), 10, time.Hour) {
		jerr(w, 429, "Zu viele Anfragen")
		return
	}
	var b struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		OS       string `json:"os"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b) != nil {
		jerr(w, 400, "Ungültige Anfrage")
		return
	}
	id, secret, code, err := s.st.NewPending(b.Username, b.Name, b.OS)
	if err != nil {
		jerr(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"id": id, "secret": secret, "code": code})
}

func (s *Server) agent(id string) *agentConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.agents[id]
}
