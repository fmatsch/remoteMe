package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func exePath() string {
	p, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return p
}

// openBrowser öffnet die lokale Einrichtungsseite im Standardbrowser.
func openBrowser(u string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		c = exec.Command("open", u)
	default:
		c = exec.Command("xdg-open", u)
	}
	c.Start()
}

const desktopEntry = `[Desktop Entry]
Type=Application
Name=remoteMe
Exec="%s" -background
X-GNOME-Autostart-enabled=true
`

const launchAgent = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>at.remoteme.agent</string>
<key>ProgramArguments</key><array><string>%s</string><string>-background</string></array>
<key>RunAtLoad</key><true/>
<key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
</dict></plist>
`

func linuxEntry() string {
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "autostart", "remoteme-agent.desktop")
}

func macPlist() string {
	return filepath.Join(homeDir(), "Library", "LaunchAgents", "at.remoteme.agent.plist")
}

func autostartEnabled() bool {
	switch runtime.GOOS {
	case "windows":
		return winAutostartGet()
	case "darwin":
		_, err := os.Stat(macPlist())
		return err == nil
	default:
		_, err := os.Stat(linuxEntry())
		return err == nil
	}
}

func setAutostart(on bool) error {
	switch runtime.GOOS {
	case "windows":
		return winAutostartSet(on, exePath())
	case "darwin":
		p := macPlist()
		if !on {
			exec.Command("launchctl", "unload", p).Run()
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(strings.Replace(launchAgent, "%s", exePath(), 1)), 0o644); err != nil {
			return err
		}
		exec.Command("launchctl", "load", p).Run()
		return nil
	case "linux":
		p := linuxEntry()
		if !on {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return err
			}
			return nil
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		return os.WriteFile(p, []byte(strings.Replace(desktopEntry, "%s", exePath(), 1)), 0o644)
	}
	return errors.New("nicht unterstützt")
}
