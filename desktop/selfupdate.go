package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// selfUpdate puts the latest release of deck in place of this one and starts it: deck.app on macOS, the installer,
// silent, on Windows; quit then ends this launcher.
func selfUpdate(quit func()) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	switch runtime.GOOS {
	case "darwin":
		bundle, _, ok := strings.Cut(exe, ".app/")
		if !ok {
			return errors.New("this launcher is not in deck.app")
		}
		bundle += ".app"
		if err := ReplaceApp(ctx, Latest(), bundle); err != nil {
			return err
		}
		if err := exec.Command("open", "-n", bundle).Start(); err != nil {
			return err
		}
	case "windows":
		setup := filepath.Join(os.TempDir(), "deck-setup.exe")
		if _, err := Latest().Fetch(ctx, "deck-setup.exe", setup); err != nil {
			return err
		}
		if err := exec.Command(setup, "/S").Start(); err != nil {
			return err
		}
	default:
		return errors.New("the launcher updates deck only on macOS and Windows")
	}
	quit()
	return nil
}
