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
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	Name string `json:"name"`
	Hash string `json:"hash"`
}

type Device struct {
	ID         string    `json:"id"`
	Owner      string    `json:"owner"`
	Name       string    `json:"name"`
	OS         string    `json:"os"`
	SecretHash string    `json:"secretHash"`
	Approved   bool      `json:"approved"`
	Code       string    `json:"code,omitempty"`
	Tries      int       `json:"tries,omitempty"`
	Created    time.Time `json:"created"`
	LastSeen   time.Time `json:"lastSeen"`
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
	pendingTTL  = 30 * time.Minute
	maxPending  = 5
	maxTries    = 5
	sessionTTL  = 14 * 24 * time.Hour
	minPassword = 8
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

// Sitzungs-Token: base64(user|ablauf).hmac
func (s *Store) MakeToken(user string) string {
	p := base64.RawURLEncoding.EncodeToString([]byte(user + "|" + strconv.FormatInt(time.Now().Add(sessionTTL).Unix(), 10)))
	return p + "." + s.sign(p)
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
	if subtle.ConstantTimeCompare([]byte(sig), []byte(s.sign(p))) != 1 {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(p)
	if err != nil {
		return "", false
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return "", false
	}
	exp, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > exp || !s.HasUser(parts[0]) {
		return "", false
	}
	return parts[0], true
}

// NewPending legt ein noch nicht freigegebenes Gerät an. Rückgabe: ID, Geheimnis, Code.
func (s *Store) NewPending(owner, name, osName string) (id, secret, code string, err error) {
	owner = NormalizeName(owner)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Users[owner] == nil {
		return "", "", "", errors.New("Benutzer nicht gefunden")
	}
	pend := 0
	for _, d := range s.Devices {
		if d.Owner == owner && !d.Approved {
			pend++
		}
	}
	if pend >= maxPending {
		return "", "", "", errors.New("Zu viele offene Anfragen für diesen Benutzer")
	}
	id, secret, code = randHex(8), randHex(32), randCode()
	s.Devices[id] = &Device{
		ID: id, Owner: owner, Name: clip(name, 64), OS: clip(osName, 16),
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
		d.LastSeen = time.Now()
		if name != "" {
			d.Name = clip(name, 64)
		}
		if osName != "" {
			d.OS = clip(osName, 16)
		}
		s.saveLocked()
	}
}

// Approve gibt ein Gerät mit dem am Zielrechner angezeigten Code frei.
func (s *Store) Approve(owner, id, code string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.Devices[id]
	if d == nil || d.Owner != owner {
		return errors.New("Gerät nicht gefunden")
	}
	if d.Approved {
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(d.Code), []byte(strings.TrimSpace(code))) != 1 {
		d.Tries++
		if d.Tries >= maxTries {
			delete(s.Devices, id)
			s.saveLocked()
			return errors.New("Zu viele Fehlversuche - Anfrage wurde verworfen")
		}
		s.saveLocked()
		return errors.New("Code stimmt nicht")
	}
	d.Approved, d.Code = true, ""
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

func (s *Store) DevicesOf(owner string) []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Device
	for _, d := range s.Devices {
		if d.Owner == owner {
			out = append(out, *d)
		}
	}
	return out
}

// ExpirePending verwirft nicht freigegebene Anfragen nach pendingTTL.
func (s *Store) ExpirePending() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var gone []string
	for id, d := range s.Devices {
		if !d.Approved && time.Since(d.Created) > pendingTTL {
			delete(s.Devices, id)
			gone = append(gone, id)
		}
	}
	if len(gone) > 0 {
		s.saveLocked()
	}
	return gone
}
