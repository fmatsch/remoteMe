//go:build windows

package main

import "golang.org/x/sys/windows/registry"

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

func winAutostartGet() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("remoteMeAgent")
	return err == nil
}

func winAutostartSet(on bool, exe string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue("remoteMeAgent"); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	return k.SetStringValue("remoteMeAgent", `"`+exe+`" -background`)
}
