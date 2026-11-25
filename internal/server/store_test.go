package server

import (
	"path/filepath"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := OpenStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"anna", "berta"} {
		if err := s.AddUser(u, "langes-passwort"); err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func TestClaimNeedsCodeAndPassword(t *testing.T) {
	s := testStore(t)
	if _, _, _, err := s.NewPending("", "PC", "linux", "kurz"); err == nil {
		t.Fatal("zu kurzes Zugriffspasswort angenommen")
	}
	if _, _, _, err := s.NewPending("", "PC", "linux", ""); err == nil {
		t.Fatal("verfügbarer Rechner ohne Passwort angenommen")
	}
	id, _, code, err := s.NewPending("", "PC", "linux", "geheim-123")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.DevicesFor("anna")) != 1 || len(s.DevicesFor("berta")) != 1 {
		t.Fatal("verfügbarer Rechner muss für alle Benutzer sichtbar sein")
	}
	if s.Approve("anna", id, code, "falsch-123") == nil {
		t.Fatal("falsches Passwort angenommen")
	}
	if s.Approve("anna", id, "000000x", "geheim-123") == nil {
		t.Fatal("falscher Code angenommen")
	}
	for i := 0; i < 10; i++ { // verfügbare Rechner dürfen durch Raten nicht verschwinden
		s.Approve("berta", id, "1", "x")
	}
	if err := s.Approve("anna", id, code, "geheim-123"); err != nil {
		t.Fatal(err)
	}
	d, _ := s.Device(id)
	if d.Owner != "anna" || !d.Approved || d.PassHash != "" || d.Code != "" {
		t.Fatalf("nach Übernahme falsch: %+v", d)
	}
	if len(s.DevicesFor("berta")) != 0 {
		t.Fatal("übernommener Rechner darf für andere nicht sichtbar sein")
	}
	if s.Approve("berta", id, code, "geheim-123") == nil {
		t.Fatal("fremder Benutzer konnte übernommenen Rechner übernehmen")
	}
}

func TestExpireUnclaimed(t *testing.T) {
	s := testStore(t)
	id, _, _, err := s.NewPending("", "PC", "linux", "geheim-123")
	if err != nil {
		t.Fatal(err)
	}
	s.Devices[id].Created = time.Now().Add(-48 * time.Hour)
	s.Devices[id].LastSeen = time.Now().Add(-unclaimedTTL - time.Hour)
	if got := s.ExpirePending(func(string) bool { return true }); len(got) != 0 {
		t.Fatal("verbundener Rechner darf nicht verfallen")
	}
	if got := s.ExpirePending(func(string) bool { return false }); len(got) != 1 {
		t.Fatal("lange offline: muss verfallen")
	}
}

func TestPasswordChangeEndsSessions(t *testing.T) {
	s := testStore(t)
	old := s.MakeToken("anna")
	if u, ok := s.VerifyToken(old); !ok || u != "anna" {
		t.Fatal("frisches Token ungültig")
	}
	if created, err := s.SetPassword("anna", "neues-passwort"); err != nil || created {
		t.Fatal(created, err)
	}
	if _, ok := s.VerifyToken(old); ok {
		t.Fatal("altes Token gilt nach Passwortänderung weiter")
	}
	if !s.CheckUser("anna", "neues-passwort") || s.CheckUser("anna", "langes-passwort") {
		t.Fatal("Passwort nicht geändert")
	}
	if err := s.DeleteUser("anna"); err != nil {
		t.Fatal(err)
	}
	if s.DeleteUser("berta") == nil {
		t.Fatal("letzter Benutzer darf nicht löschbar sein")
	}
}
