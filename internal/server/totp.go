package server

// Zeitbasierte Einmalcodes (TOTP, RFC 6238: SHA-1, 30 s, 6 Stellen) für Authenticator-Apps.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const totpPeriod = 30

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret erzeugt ein neues Geheimnis (160 Bit, Base32).
func NewTOTPSecret() string {
	b := make([]byte, 20)
	rand.Read(b)
	return b32.EncodeToString(b)
}

// TOTPURI ist die Adresse, die Authenticator-Apps per QR-Code einlesen.
func TOTPURI(user, secret string) string {
	return "otpauth://totp/" + url.PathEscape("remoteMe:"+user) + "?secret=" + secret + "&issuer=remoteMe"
}

func totpCode(secret string, counter uint64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil || len(key) < 10 {
		return "", errors.New("ungültiges Geheimnis")
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	h := m.Sum(nil)
	o := h[len(h)-1] & 0x0f
	n := binary.BigEndian.Uint32(h[o:o+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000), nil
}

// matchTOTP liefert den Zeitschritt, zu dem der Code passt (±1 Schritt Uhrabweichung).
func matchTOTP(secret, code string, now time.Time) (uint64, bool) {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != 6 {
		return 0, false
	}
	c := uint64(now.Unix()) / totpPeriod
	for _, step := range []uint64{c, c - 1, c + 1} {
		want, err := totpCode(secret, step)
		if err == nil && subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			return step, true
		}
	}
	return 0, false
}

// HasTOTP meldet, ob für den Benutzer ein Einmalcode eingerichtet ist.
func (s *Store) HasTOTP(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.Users[NormalizeName(name)]
	return u != nil && u.TOTP != ""
}

// CheckTOTP prüft den Einmalcode; jeder Code gilt nur einmal.
func (s *Store) CheckTOTP(name, code string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.Users[NormalizeName(name)]
	if u == nil || u.TOTP == "" {
		return false
	}
	step, ok := matchTOTP(u.TOTP, code, time.Now())
	if !ok || step <= u.TOTPLast {
		return false
	}
	u.TOTPLast = step
	return s.saveLocked() == nil
}

// SetTOTP richtet den Einmalcode ein, nachdem code beweist, dass die App das Geheimnis hat.
// Ein leeres Geheimnis schaltet ihn ab.
func (s *Store) SetTOTP(name, secret, code string) error {
	if secret != "" {
		if _, ok := matchTOTP(secret, code, time.Now()); !ok {
			return errors.New("Der Einmalcode stimmt nicht - nichts geändert (Uhrzeit auf dem Telefon prüfen)")
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	u := s.Users[NormalizeName(name)]
	if u == nil {
		return errors.New("Benutzer nicht gefunden")
	}
	u.TOTP, u.TOTPLast = strings.ToUpper(secret), 0
	return s.saveLocked()
}
