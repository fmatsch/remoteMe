package main

import (
	"net/http/httptest"
	"testing"
)

// Ohne den Schlüssel in der Adresse darf die lokale Seite das Zugriffstoken nicht herausgeben.
func TestSetupPageNeedsKey(t *testing.T) {
	u := &ui{token: "0123456789abcdef", port: uiPort}
	for query, want := range map[string]int{"": 403, "?k=falsch": 403, "?k=0123456789abcdef": 200} {
		r := httptest.NewRequest("GET", "http://127.0.0.1:17321/"+query, nil)
		w := httptest.NewRecorder()
		u.page(w, r)
		if w.Code != want {
			t.Errorf("%q: Status %d, erwartet %d", query, w.Code, want)
		}
	}
}

func TestCheckServerURL(t *testing.T) {
	for url, ok := range map[string]bool{
		"https://example.org": true, "http://127.0.0.1:8080": true, "http://localhost:8080": true,
		"http://example.org": false, "http://192.168.0.5": false, "ftp://example.org": false, "": false,
	} {
		if (checkServerURL(url) == nil) != ok {
			t.Errorf("%q: erwartet zulässig=%v", url, ok)
		}
	}
}
