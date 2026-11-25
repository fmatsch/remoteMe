package update

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"
)

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	key := base64.StdEncoding.EncodeToString(pub)
	m := []byte(`{"version":"2.0.0","files":{"windows-amd64":{"name":"a.exe","sha256":"x","size":3}}}`)
	sig := []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m)))
	if got, err := Verify(key, m, sig); err != nil || got.Version != "2.0.0" {
		t.Fatal(got, err)
	}
	if _, err := Verify(key, []byte(`{"version":"9.0.0"}`), sig); err == nil {
		t.Fatal("verändertes Manifest angenommen")
	}
	other, _, _ := ed25519.GenerateKey(nil)
	if _, err := Verify(base64.StdEncoding.EncodeToString(other), m, sig); err == nil {
		t.Fatal("fremder Schlüssel angenommen")
	}
	if _, err := Verify("", m, sig); err == nil {
		t.Fatal("ohne Schlüssel angenommen")
	}
	bad := []byte(`{"version":"2.0.0","files":{"x":{"name":"../a","sha256":"x","size":3}}}`)
	if _, err := Verify(key, bad, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, bad)))); err == nil {
		t.Fatal("Pfad im Dateinamen angenommen")
	}
}

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"1.5.0", "1.4.0", true}, {"1.10.0", "1.9.9", true}, {"1.4.0", "1.4.0", false}, {"1.3.9", "1.4.0", false}, {"2.0", "1.9.9", true}} {
		if Newer(c.a, c.b) != c.want {
			t.Errorf("Newer(%s,%s) != %v", c.a, c.b, c.want)
		}
	}
}
