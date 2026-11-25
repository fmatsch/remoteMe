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

	"remoteme/internal/update"
)

//go:embed web
var webFS embed.FS

type Config struct {
	Addr             string
	DataFile         string
	CertFile         string
	KeyFile          string
	OpenRegistration bool
	UpdateDir        string // signierte Pakete für Agents (manifest.json, manifest.sig, Dateien)
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
	mux.HandleFunc("POST /api/devices/{id}/update", s.auth(s.hUpdate))
	mux.HandleFunc("GET /update/{file}", s.hUpdateFile)
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
		w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
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
			for _, id := range s.st.ExpirePending(func(id string) bool { return s.agent(id) != nil }) {
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

// clientIP wertet X-Forwarded-For nur aus, wenn die Anfrage von einem Reverse-Proxy auf demselben
// Rechner kommt, und nimmt dann den von ihm angehängten (letzten) Eintrag.
func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		h = r.RemoteAddr
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		if f := r.Header.Get("X-Forwarded-For"); f != "" {
			parts := strings.Split(f, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
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
	Code     string `json:"code"` // Einmalcode aus der Authenticator-App
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
	ip, user := clientIP(r), NormalizeName(c.Username)
	if len(user) > 32 {
		user = user[:32]
	}
	// Begrenzung je Absender, je Benutzer und Absender sowie je Benutzer über alle Absender.
	key := "login:" + user + "@" + ip
	if !s.lim.allow("login-ip:"+ip, 30, 10*time.Minute) || !s.lim.allow(key, 6, 10*time.Minute) ||
		(s.st.HasUser(user) && !s.lim.allow("login-user:"+user, 100, time.Hour)) {
		log.Printf("Anmeldung gesperrt (zu viele Versuche): %q von %s", user, ip)
		jerr(w, 429, "Zu viele Fehlversuche, bitte später erneut versuchen")
		return
	}
	if !s.st.CheckUser(c.Username, c.Password) {
		log.Printf("Anmeldung fehlgeschlagen (Passwort): %q von %s", user, ip)
		jerr(w, 401, "Benutzername oder Passwort falsch")
		return
	}
	if s.st.HasTOTP(user) {
		if strings.TrimSpace(c.Code) == "" {
			writeJSON(w, 401, map[string]any{"error": "Bitte den Einmalcode aus der Authenticator-App eingeben", "totp": true})
			return
		}
		if !s.st.CheckTOTP(user, c.Code) {
			log.Printf("Anmeldung fehlgeschlagen (Einmalcode): %q von %s", user, ip)
			writeJSON(w, 401, map[string]any{"error": "Einmalcode stimmt nicht oder wurde schon benutzt", "totp": true})
			return
		}
	}
	s.lim.reset(key)
	log.Printf("Anmeldung: %q von %s", user, ip)
	s.setSession(w, r, user)
	writeJSON(w, 200, map[string]string{"user": user})
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
		// Claimable: verfügbarer Rechner ohne Besitzer; NeedsPassword: Übernahme verlangt das Zugriffspasswort.
		Claimable     bool `json:"claimable"`
		NeedsPassword bool `json:"needsPassword"`
		// Nur für eigene, verbundene Computer.
		Version         string `json:"version,omitempty"`
		Service         bool   `json:"service,omitempty"`
		UpdateAvailable string `json:"updateAvailable,omitempty"`
		UpdateStatus    string `json:"updateStatus,omitempty"`
		Stats           *stats `json:"stats,omitempty"`
		CPUHist         []int  `json:"cpuHist,omitempty"`
		MemHist         []int  `json:"memHist,omitempty"`
	}
	manifest := s.manifest()
	out := []dev{}
	for _, d := range s.st.DevicesFor(user) {
		x := dev{ID: d.ID, Name: d.Name, OS: d.OS, Approved: d.Approved, LastSeen: d.LastSeen,
			Claimable: d.Owner == "", NeedsPassword: d.PassHash != ""}
		if ac := s.agent(d.ID); ac != nil {
			x.Online, x.Busy = true, ac.hasViewer()
			if d.Owner == user && d.Approved {
				i := &ac.info
				i.mu.Lock()
				x.Version, x.Service, x.UpdateStatus, x.Stats = i.version, i.service, i.status, i.last
				x.CPUHist, x.MemHist = append([]int(nil), i.cpuHist...), append([]int(nil), i.memHist...)
				if manifest != nil && i.platform != "" && update.Newer(manifest.Version, i.version) {
					if _, ok := manifest.Files[i.platform]; ok {
						x.UpdateAvailable = manifest.Version
					}
				}
				i.mu.Unlock()
			}
		}
		if x.Claimable && !x.Online {
			continue // nur tatsächlich verfügbare Rechner anzeigen
		}
		out = append(out, x)
	}
	writeJSON(w, 200, out)
}

func (s *Server) hApprove(w http.ResponseWriter, r *http.Request, user string) {
	var b struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&b)
	id := r.PathValue("id")
	// Raten begrenzen: je Gerät und je Benutzer.
	key, userKey := "approve:"+id, "approve-user:"+user
	if !s.lim.allow(key, 5, 15*time.Minute) || !s.lim.allow(userKey, 20, 15*time.Minute) {
		jerr(w, 429, "Zu viele Fehlversuche, bitte später erneut versuchen")
		return
	}
	if err := s.st.Approve(user, id, b.Code, b.Password); err != nil {
		log.Printf("Übernahme fehlgeschlagen: Gerät %q, Benutzer %q von %s: %v", id, user, clientIP(r), err)
		jerr(w, 400, err.Error())
		return
	}
	log.Printf("Übernahme: Gerät %q durch %q von %s", id, user, clientIP(r))
	s.lim.reset(key)
	s.lim.reset(userKey)
	if ac := s.agent(id); ac != nil {
		ac.sendJSON(map[string]any{"t": "approved", "owner": user})
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) hDelete(w http.ResponseWriter, r *http.Request, user string) {
	id := r.PathValue("id")
	if !s.st.DeleteDevice(user, id) {
		jerr(w, 404, "Gerät nicht gefunden")
		return
	}
	log.Printf("Gerät %q entfernt durch %q von %s", id, user, clientIP(r))
	if ac := s.agent(id); ac != nil {
		ac.sendJSON(map[string]any{"t": "revoked"})
		ac.close()
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Agent meldet sich an: ohne Benutzernamen als verfügbarer Rechner (mit Zugriffspasswort).
func (s *Server) hPair(w http.ResponseWriter, r *http.Request) {
	if !s.lim.allow("pair:"+clientIP(r), 10, time.Hour) {
		jerr(w, 429, "Zu viele Anfragen")
		return
	}
	var b struct {
		Username string `json:"username"`
		Name     string `json:"name"`
		OS       string `json:"os"`
		Password string `json:"password"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&b) != nil {
		jerr(w, 400, "Ungültige Anfrage")
		return
	}
	id, secret, code, err := s.st.NewPending(b.Username, b.Name, b.OS, b.Password, clientIP(r))
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
