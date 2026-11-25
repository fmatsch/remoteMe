package main

import (
	"os"
	"path/filepath"
	"strings"
)

// Rolle dieser Installation (Mac-App): nur Zielrechner oder zusätzlich Fernsteuerung anderer.
const (
	roleUnset  = ""
	roleTarget = "target"
	roleFull   = "full"
)

func rolePath() string { return filepath.Join(configDir(), "role") }

// loadRole liefert die gewählte Rolle. Bestehende Installationen ohne Wahl gelten als Vollversion.
func loadRole() string {
	if b, err := os.ReadFile(rolePath()); err == nil {
		switch r := strings.TrimSpace(string(b)); r {
		case roleTarget, roleFull:
			return r
		}
	}
	if loadConfig() != nil || loadAccount().Server != "" {
		return roleFull
	}
	return roleUnset
}

func saveRole(r string) error {
	if r != roleTarget && r != roleFull {
		return nil
	}
	return os.WriteFile(rolePath(), []byte(r+"\n"), 0o600)
}
