package main

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

const run = `Software\Microsoft\Windows\CurrentVersion\Run`

func autostart() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, run, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue("deck")
	return err == nil
}

func setAutostart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, run, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		if err := k.DeleteValue("deck"); err != nil && err != registry.ErrNotExist {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue("deck", `"`+exe+`" -hidden`)
}
