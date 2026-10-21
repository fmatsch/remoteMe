package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config ist die gekoppelte Verbindung zum Relay-Server.
type Config struct {
	Server   string `json:"server"`
	Username string `json:"username"`
	ID       string `json:"id"`
	Secret   string `json:"secret"`
	Name     string `json:"name"`
	Code     string `json:"code,omitempty"` // Freigabecode, solange noch nicht freigegeben
	Approved bool   `json:"approved"`
}

func configDir() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = "."
	}
	d = filepath.Join(d, "remoteMe")
	os.MkdirAll(d, 0o700)
	return d
}

func configPath() string { return filepath.Join(configDir(), "agent.json") }

func loadConfig() *Config {
	b, err := os.ReadFile(configPath())
	if err != nil {
		return nil
	}
	var c Config
	if json.Unmarshal(b, &c) != nil || c.ID == "" {
		return nil
	}
	return &c
}

func saveConfig(c *Config) error {
	if c == nil {
		err := os.Remove(configPath())
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(configPath(), b, 0o600)
}
