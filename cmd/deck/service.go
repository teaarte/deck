package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	deckhome "deck2/internal/home"
	"deck2/internal/sandbox"
)

// service is the service of the system that starts the node of a home when its user signs in and keeps it running:
// a LaunchAgent on macOS, a unit of systemd of the user on Linux.
type service struct {
	label, file, text string
	start, stop       [][]string
}

// serviceOf is the service of the node of h at address; the label of a home besides the user's own deck2 names it.
func serviceOf(h deckhome.Layout, exe, address string) (service, error) {
	user, err := os.UserHomeDir()
	if err != nil {
		return service{}, err
	}
	label := "deck-node"
	if filepath.Clean(string(h)) != filepath.Join(user, ".deck2") {
		sum := sha256.Sum256([]byte(filepath.Clean(string(h))))
		label += "." + hex.EncodeToString(sum[:4])
	}
	switch runtime.GOOS {
	case "darwin":
		domain := "gui/" + strconv.Itoa(os.Getuid())
		args := ""
		for _, a := range []string{exe, "-join", address} {
			args += "\n    <string>" + escaped(a) + "</string>"
		}
		text := `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>` + label + `</string>
  <key>ProgramArguments</key>
  <array>` + args + `
  </array>
  <key>EnvironmentVariables</key>
  <dict>
    <key>DECK_HOME</key>
    <string>` + escaped(string(h)) + `</string>
  </dict>
  <key>RunAtLoad</key>
  <true/>
  <key>KeepAlive</key>
  <true/>
  <key>ProcessType</key>
  <string>Standard</string>
  <key>StandardErrorPath</key>
  <string>` + escaped(h.NodeLog()) + `</string>
</dict>
</plist>
`
		file := filepath.Join(user, "Library", "LaunchAgents", label+".plist")
		return service{label: label, file: file, text: text,
			start: [][]string{{"launchctl", "bootstrap", domain, file}},
			stop:  [][]string{{"launchctl", "bootout", domain + "/" + label}}}, nil
	case "linux":
		config := os.Getenv("XDG_CONFIG_HOME")
		if config == "" {
			config = filepath.Join(user, ".config")
		}
		unit := label + ".service"
		text := "[Unit]\nDescription=deck node: serves deck at " + address + " the models of this machine\n" +
			"After=network-online.target\n\n[Service]\nEnvironment=" + strconv.Quote("DECK_HOME="+string(h)) + "\n" +
			"ExecStart=" + strconv.Quote(exe) + " -join " + strconv.Quote(address) + "\nRestart=always\nRestartSec=5\n\n" +
			"[Install]\nWantedBy=default.target\n"
		return service{label: label, file: filepath.Join(config, "systemd", "user", unit), text: text,
			start: [][]string{{"systemctl", "--user", "daemon-reload"}, {"systemctl", "--user", "enable", unit}, {"systemctl", "--user", "restart", unit}},
			stop:  [][]string{{"systemctl", "--user", "disable", "--now", unit}}}, nil
	}
	return service{}, fmt.Errorf("deck installs the service of a node on macOS and Linux, not on %s; run deck -join <address> there yourself", runtime.GOOS)
}

func escaped(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// installService writes the service of the node and starts it; one there before goes first.
func installService(ctx context.Context, h deckhome.Layout, address string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return err
	}
	s, err := serviceOf(h, exe, address)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(h.Node(), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(s.file); err == nil {
		for _, argv := range s.stop {
			_, _ = sandbox.System(ctx, argv...)
		}
	}
	if err := deckhome.WriteFile(s.file, []byte(s.text)); err != nil {
		return err
	}
	for _, argv := range s.start {
		if out, err := sandbox.System(ctx, argv...); err != nil {
			return fmt.Errorf("%s: %v: %s", strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
		}
	}
	fmt.Printf("The node starts with this machine's sign-in and keeps itself connected to deck at %s: service %s, file %s, log %s.\nIt runs %s; keep that binary where it is, or install again.\n",
		address, s.label, s.file, h.NodeLog(), exe)
	return nil
}

// uninstallService stops the service of the node of h and removes it; its key stays, until the owner revokes the node.
func uninstallService(h deckhome.Layout) error {
	s, err := serviceOf(h, "", "")
	if err != nil {
		return err
	}
	if _, err := os.Stat(s.file); errors.Is(err, os.ErrNotExist) {
		fmt.Printf("No service of the node of %s is installed.\n", string(h))
		return nil
	}
	for _, argv := range s.stop {
		_, _ = sandbox.System(context.Background(), argv...)
	}
	if err := os.Remove(s.file); err != nil {
		return err
	}
	if runtime.GOOS == "linux" {
		_, _ = sandbox.System(context.Background(), "systemctl", "--user", "daemon-reload")
	}
	fmt.Printf("The service %s is stopped and removed. The key of the node stays in %s until it is revoked in Devices on deck.\n", s.label, h.NodeKey())
	return nil
}
