package main

import (
	"fmt"
	"os"
	"path/filepath"
)

// agent is the login item of the launcher: a launch agent of the user that opens it hidden at login.
func agent() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Library", "LaunchAgents", "com.deck2.launcher.plist")
}

func autostart() bool {
	_, err := os.Stat(agent())
	return err == nil
}

func setAutostart(on bool) error {
	if !on {
		if err := os.Remove(agent()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.deck2.launcher</string>
<key>ProgramArguments</key><array><string>%s</string><string>-hidden</string></array>
<key>RunAtLoad</key><true/>
</dict></plist>
`, exe)
	if err := os.MkdirAll(filepath.Dir(agent()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(agent(), []byte(plist), 0o644)
}
