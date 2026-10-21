//go:build !windows

package main

func winAutostartGet() bool              { return false }
func winAutostartSet(bool, string) error { return nil }
