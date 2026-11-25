package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
	// Zweiter Faktor (Authenticator-App): Geheimnis und zuletzt benutzter Zeitschritt.
	TOTP     string `json:"totp,omitempty"`
	TOTPLast uint64 `json:"totpLast,omitempty"`
}

type Device struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	Name       string    `json:"name"`
	OS         string    `json:"os"`
	SecretHash string    `json:"secretHash"`
	Approved   bool      `json:"approved"`
	Code       string    `json:"code,omitempty"`
	PassHash   string    `json:"passHash,omitempty"` // Zugriffspasswort des Zielrechners (bcrypt), nur bis zur Übernahme
	Tries      int       `json:"tries,omitempty"`
	Created    time.Time `json:"created"`
	LastSeen   time.Time `json:"lastSeen"`
	Seen       bool      `json:"seen,omitempty"` // hat sich mindestens einmal verbunden
	IP         string    `json:"ip,omitempty"`   // Absender der Anmeldung (Begrenzung)
}

type Store struct {
	mu      sync.Mutex
	path    string
	Users   map[string]*User   `json:"users"`
	Devices map[string]*Device `json:"devices"`
	Key     string             `json:"key"`
}

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("dummy-password"), bcrypt.DefaultCost)

var nameRe = regexp.MustCompile(`^[a-z0-9_.-]{3,32}$`)

const (
	pendingTTL = 30 * time.Minute
	maxPending = 5
	// Rechner ohne Besitzer ("verfügbar"): Obergrenzen (gesamt, je Absender) und Verfall,
	// wenn sie sich nie verbinden oder länger offline sind.
	maxUnclaimed      = 100
	maxUnclaimedPerIP = 10
	unclaimedTTL      = 24 * time.Hour
	neverSeenTTL      = 10 * time.Minute
	minAccessPW       = 6
	maxTries          = 5
	sessionTTL        = 12 * time.Hour
	minPassword       = 8
)

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, Users: map[string]*User{}, Devices: map[string]*Device{}}
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, s); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return nil, err
	}
	if s.Users == nil {
		s.Users = map[string]*User{}
	}
	if s.Devices == nil {
		s.Devices = map[string]*Device{}
	}
	if s.Key == "" {
		s.Key = randHex(32)
		if err := s.saveLocked(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func randHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func randCode() string {
	b := make([]byte, 4)
	rand.Read(b)
	n := (uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])) % 1000000
	return fmt.Sprintf("%06d", n)
}

func hashSecret(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func NormalizeName(n string) string { return strings.ToLower(strings.TrimSpace(n)) }

func (s *Store) UserCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Users)
}

func (s *Store) AddUser(name, password string) error {
	name = NormalizeName(name)
	if !nameRe.MatchString(name) {
		return errors.New("Benutzername: 3-32 Zeichen (a-z, 0-9, _ . -)")
	}
	if len(password) < minPassword {
		return fmt.Errorf("Passwort muss mindestens %d Zeichen haben", minPassword)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.Users[name]; ok {
		return errors.New("Benutzername ist bereits vergeben")
	}
	s.Users[name] = &User{Name: name, Hash: string(h)}
	return s.saveLocked()
}

func (s *Store) CheckUser(name, password string) bool {
	name = NormalizeName(name)
	s.mu.Lock()
	u := s.Users[name]
	s.mu.Unlock()
	if u == nil {
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(u.Hash), []byte(password)) == nil
}

func (s *Store) HasUser(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Users[NormalizeName(name)] != nil
}

// SetPassword legt den Benutzer an oder ändert sein Passwort. created meldet, ob er neu ist.
func (s *Store) SetPassword(name, password string) (created bool, err error) {
	name = NormalizeName(name)
	if !s.HasUser(name) {
		return true, s.AddUser(name, password)
	}
	if len(password) < minPassword {
		return false, fmt.Errorf("Passwort muss mindestens %d Zeichen haben", minPassword)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, errors.New("Passwort ist zu lang")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Users[name].Hash = string(h)
	return false, s.saveLocked()
}

// DeleteUser entfernt den Benutzer samt seinen Geräten. Der letzte Benutzer bleibt erhalten,
// weil sich sonst wieder jeder als erster Benutzer registrieren könnte.
func (s *Store) DeleteUser(name string) error {
	name = NormalizeName(name)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Users[name] == nil {
		return errors.New("Benutzer nicht gefunden")
	}
	if len(s.Users) == 1 {
		return errors.New("Der letzte Benutzer kann nicht gelöscht werden")
	}
	delete(s.Users, name)
	for id, d := range s.Devices {
		if d.Owner == name {
			delete(s.Devices, id)
		}
	}
	return s.saveLocked()
}

// userKey bindet Sitzungen an Passwort und zweiten Faktor.
func (s *Store) userKey(name string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if u := s.Users[NormalizeName(name)]; u != nil {
		return u.Hash + "|" + u.TOTP
	}
	return ""
}

// Sitzungs-Token: base64(user|ablauf).hmac – die Signatur hängt am Passwort-Hash,
// eine Änderung von Passwort oder Einmalcode beendet alle bestehenden Anmeldungen.
func (s *Store) MakeToken(user string) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(user + "|" + strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10)))
	return p + "." + s.sign(p+"|"+s.userKey(user))
}

func (s *Store) sign(p string) string {
	m := hmac.New(sha256.New, []byte(s.Key))
	m.Write([]byte(p))
	return hex.EncodeToString(m.Sum(nil))
}

func (s *Store) VerifyToken(tok string) (string, bool) {
	i := strings.LastIndexByte(tok, '.')
	if i < 0 {
		return "", false
	}
	p, sig := tok[:i], tok[i+1:]
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return "", false
	}
	hash := s.userKey(parts[0])
	if hash == "" || subtle.ConstantTimeCompare([]byte(sig), []byte(s.sign(p+"|"+hash))) != 1 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return "", false
	}
	return parts[0], true
}

// NewPending legt ein noch nicht freigegebenes Gerät an. Rückgabe: ID, Geheimnis, Code.
// Ohne owner ist das Gerät "verfügbar": jeder angemeldete Benutzer sieht es und kann es mit
// Code und Zugriffspasswort übernehmen. Mit owner (ältere Agents) gehört es von Anfang an diesem Benutzer.
func (s *Store) NewPending(owner, name, osName, password, ip string) (id, secret, code string, err error) {
	owner = NormalizeName(owner)
	var passHash string
	if password != "" || owner == "" {
		if len(password) < minAccessPW {
			return "", "", "", fmt.Errorf("Das Zugriffspasswort muss mindestens %d Zeichen haben", minAccessPW)
		}
		h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			return "", "", "", errors.New("Zugriffspasswort ist zu lang")
		}
		passHash = string(h)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if owner != "" && s.Users[owner] == nil {
		return "", "", "", errors.New("Benutzer nicht gefunden")
	}
	pend, fromIP, limit := 0, 0, maxPending
	if owner == "" {
		limit = maxUnclaimed
	}
	for _, d := range s.Devices {
		if d.Owner == owner && !d.Approved {
			pend++
			if d.IP == ip {
				fromIP++
			}
		}
	}
	if pend >= limit || (owner == "" && fromIP >= maxUnclaimedPerIP) {
		return "", "", "", errors.New("Zu viele offene Anfragen")
	}
	id, secret, code = randHex(8), randHex(32), randCode()
	s.Devices[id] = &Device{
		ID: id, Owner: owner, Name: clip(name, 64), OS: clip(osName, 16), PassHash: passHash, IP: ip,
		SecretHash: hashSecret(secret), Code: code, Created: time.Now(), LastSeen: time.Now(),
	}
	return id, secret, code, s.saveLocked()
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

// AuthDevice prüft das Geheimnis und liefert eine Kopie des Geräts.
func (s *Store) AuthDevice(id, secret string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.Devices[id]
	if d == nil || subtle.ConstantTimeCompare([]byte(d.SecretHash), []byte(hashSecret(secret))) != 1 {
		return Device{}, false
	}
	return *d, true
}

func (s *Store) Touch(id, name, osName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d := s.Devices[id]; d != nil {
		d.LastSeen, d.Seen = time.Now(), true
		if name != "" {
			d.Name = clip(name, 64)
		}
		if osName != "" {
			d.OS = clip(osName, 16)
		}
		s.saveLocked()
	}
}

// Approve gibt ein Gerät mit dem am Zielrechner angezeigten Code und dem dort vergebenen
// Zugriffspasswort frei; ein verfügbares Gerät gehört danach dem Benutzer.
func (s *Store) Approve(user, id, code, password string) error {
	notFound, wrong := errors.New("Gerät nicht gefunden"), errors.New("Code oder Zugriffspasswort stimmt nicht")
	s.mu.Lock()
	d := s.Devices[id]
	if d == nil || (d.Owner != user && !(d.Owner == "" && !d.Approved)) {
		s.mu.Unlock()
		return notFound
	}
	if d.Approved {
		s.mu.Unlock()
		return nil
	}
	passHash, want := d.PassHash, d.Code
	s.mu.Unlock()

	// bcrypt außerhalb der Sperre: dauert absichtlich lange.
	ok := subtle.ConstantTimeCompare([]byte(want), []byte(strings.TrimSpace(code))) == 1
	if passHash != "" && bcrypt.CompareHashAndPassword([]byte(passHash), []byte(password)) != nil {
		ok = false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	d = s.Devices[id]
	if d == nil || (d.Owner != user && !(d.Owner == "" && !d.Approved)) {
		return notFound // inzwischen entfernt oder von jemand anderem übernommen
	}
	if !ok {
		if d.Owner != "" { // eigene Anfrage (ältere Agents): nach zu vielen Fehlversuchen verwerfen
			if d.Tries++; d.Tries >= maxTries {
				delete(s.Devices, id)
				s.saveLocked()
				return errors.New("Zu viele Fehlversuche - Anfrage wurde verworfen")
			}
			s.saveLocked()
		}
		return wrong
	}
	d.Owner, d.Approved, d.Code, d.PassHash, d.Tries, d.IP = user, true, "", "", 0, ""
	return s.saveLocked()
}

func (s *Store) DeleteDevice(owner, id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.Devices[id]
	if d == nil || d.Owner != owner {
		return false
	}
	delete(s.Devices, id)
	s.saveLocked()
	return true
}

func (s *Store) Device(id string) (Device, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.Devices[id]
	if d == nil {
		return Device{}, false
	}
	return *d, true
}

// DevicesFor liefert die eigenen und alle noch verfügbaren (besitzerlosen) Geräte.
func (s *Store) DevicesFor(user string) []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Device
	for _, d := range s.Devices {
		if d.Owner == user || (d.Owner == "" && !d.Approved) {
			out = append(out, *d)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := strings.ToLower(out[i].Name), strings.ToLower(out[j].Name); a != b {
			return a < b
		}
		return out[i].ID < out[j].ID
	})
	return out
}

// ExpirePending verwirft offene Anfragen: eigene nach pendingTTL, verfügbare Rechner erst,
// wenn sie länger als unclaimedTTL nicht mehr verbunden waren.
func (s *Store) ExpirePending(online func(id string) bool) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var gone []string
	for id, d := range s.Devices {
		if d.Approved {
			continue
		}
		expired := time.Since(d.Created) > pendingTTL
		if d.Owner == "" {
			ttl := unclaimedTTL
			if !d.Seen {
				ttl = neverSeenTTL
			}
			expired = !online(id) && time.Since(d.LastSeen) > ttl
		}
		if expired {
			delete(s.Devices, id)
			gone = append(gone, id)
		}
	}
	if len(gone) > 0 {
		s.saveLocked()
	}
	return gone
}
