//go:build !windows

package main

// Dienstbetrieb (Zugriff auf gesperrte Rechner) gibt es nur unter Windows (service_windows.go).
const (
	machineMode      = false
	serviceSupported = false
)

func platformFlags() func() bool { return func() bool { return false } }
func machineConfigDir() string   { return "" }
func publicDesktop() string      { return "" }
func machineRoots() []entry      { return nil }
func useInputDesktop()           {}
func sendSAS()                   {}
func serviceInstalled() bool     { return false }
func startServiceInstall() error { return nil }
