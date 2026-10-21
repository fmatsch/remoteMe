package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// account ist die Anmeldung des Benutzers am Relay-Server (Controller-Seite der App):
// Geräteliste, Freigabe per Code, Entfernen. Die Verbindung zu einem Gerät selbst läuft im
// eingebetteten Viewer, der das Sitzungs-Cookie von hier übernimmt.
type account struct {
	mu     sync.Mutex
	server string
	user   string
	pw     string // nur im Speicher; dauerhaft liegt es im Schlüsselbund (macOS)
	client *http.Client
	ok     bool
}

var acct = &account{}

type accountFile struct {
	Server string `json:"server"`
	User   string `json:"user"`
}

func accountPath() string { return filepath.Join(configDir(), "account.json") }

// loadAccount liefert gespeicherte Server-/Benutzerdaten (ohne Passwort).
func loadAccount() accountFile {
	var f accountFile
	if b, err := os.ReadFile(accountPath()); err == nil {
		json.Unmarshal(b, &f)
	}
	return f
}

func normServer(s string) string {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if s != "" && !strings.Contains(s, "://") {
		s = "https://" + s
	}
	return s
}

// login meldet am Server an und merkt sich Sitzung und Zugangsdaten für automatische Neuanmeldung.
func (c *account) login(server, user, pw string) error {
	server = normServer(server)
	if server == "" || strings.TrimSpace(user) == "" || pw == "" {
		return errors.New("Bitte Server, Benutzer und Passwort eingeben")
	}
	jar, _ := cookiejar.New(nil)
	cl := &http.Client{Timeout: 20 * time.Second, Jar: jar}
	body, _ := json.Marshal(map[string]string{"username": strings.TrimSpace(user), "password": pw})
	req, _ := http.NewRequest("POST", server+"/api/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-FW", "1")
	resp, err := cl.Do(req)
	if err != nil {
		return errors.New("Server nicht erreichbar: " + err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		var e struct{ Error string }
		json.NewDecoder(resp.Body).Decode(&e)
		if e.Error == "" {
			e.Error = resp.Status
		}
		return errors.New(e.Error)
	}
	c.mu.Lock()
	c.server, c.user, c.pw, c.client, c.ok = server, strings.ToLower(strings.TrimSpace(user)), pw, cl, true
	c.mu.Unlock()
	b, _ := json.Marshal(accountFile{Server: server, User: c.user})
	os.WriteFile(accountPath(), b, 0o600)
	return nil
}

func (c *account) logout() {
	c.mu.Lock()
	c.pw, c.ok, c.client = "", false, nil
	c.mu.Unlock()
}

func (c *account) state() (server, user string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.server, c.user, c.ok
}

// api ruft den Server auf; bei abgelaufener Sitzung (401) wird einmal neu angemeldet.
func (c *account) api(method, path string, in, out any) error {
	for attempt := 0; ; attempt++ {
		c.mu.Lock()
		cl, server, user, pw := c.client, c.server, c.user, c.pw
		c.mu.Unlock()
		if cl == nil {
			return errors.New("nicht angemeldet")
		}
		var rd io.Reader
		if in != nil {
			b, _ := json.Marshal(in)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, server+path, rd)
		req.Header.Set("X-FW", "1")
		req.Header.Set("Content-Type", "application/json")
		resp, err := cl.Do(req)
		if err != nil {
			return err
		}
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if resp.StatusCode == 401 && attempt == 0 {
			if err := c.login(server, user, pw); err != nil {
				return err
			}
			continue
		}
		if resp.StatusCode >= 300 {
			var e struct{ Error string }
			json.Unmarshal(data, &e)
			if e.Error == "" {
				e.Error = resp.Status
			}
			return errors.New(e.Error)
		}
		if out != nil {
			return json.Unmarshal(data, out)
		}
		return nil
	}
}

type remoteDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	OS       string `json:"os"`
	Approved bool   `json:"approved"`
	Online   bool   `json:"online"`
	Busy     bool   `json:"busy"`
}

func (c *account) devices() ([]remoteDevice, error) {
	var l []remoteDevice
	err := c.api("GET", "/api/devices", nil, &l)
	return l, err
}

func (c *account) approve(id, code string) error {
	return c.api("POST", "/api/devices/"+url.PathEscape(id)+"/approve", map[string]string{"code": strings.TrimSpace(code)}, nil)
}

func (c *account) remove(id string) error {
	return c.api("DELETE", "/api/devices/"+url.PathEscape(id), nil, nil)
}

// sessionCookie liefert das Sitzungs-Cookie für den eingebetteten Viewer.
func (c *account) sessionCookie() (name, value, host string, secure bool, err error) {
	c.mu.Lock()
	cl, server := c.client, c.server
	c.mu.Unlock()
	if cl == nil {
		return "", "", "", false, errors.New("nicht angemeldet")
	}
	u, _ := url.Parse(server)
	for _, ck := range cl.Jar.Cookies(u) {
		if ck.Name == "fw_session" {
			return ck.Name, ck.Value, u.Hostname(), u.Scheme == "https", nil
		}
	}
	return "", "", "", false, errors.New("keine Sitzung")
}
