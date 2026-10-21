//go:build windows

package main

import (
	"os/exec"
	"syscall"
)

// hideWindow verhindert ein Konsolenfenster für Hilfsprogramme.
func hideWindow(c *exec.Cmd) { c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true} }
