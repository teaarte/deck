package main

import (
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	// An app opened from Downloads runs from a copy macOS removes; it starts at login once moved to Applications.
	if strings.Contains(exe, "/AppTranslocation/") {
		return errors.New(tr("Move deck to Applications first"))
	}
	var path strings.Builder
	_ = xml.EscapeText(&path, []byte(exe))
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>com.deck2.launcher</string>
<key>ProgramArguments</key><array><string>%s</string><string>-hidden</string></array>
<key>RunAtLoad</key><true/>
</dict></plist>
`, path.String())
	if err := os.MkdirAll(filepath.Dir(agent()), 0o755); err != nil {
		return err
	}
	return os.WriteFile(agent(), []byte(plist), 0o644)
}
