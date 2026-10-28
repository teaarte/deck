package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// selfUpdate puts the latest release of deck in place of this one and starts it: deck.app on macOS, the installer,
// silent, on Windows; quit then ends this launcher.
func selfUpdate(r Runner, quit func()) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	now, err := r.Version(ctx)
	if err != nil {
		return fmt.Errorf("the version of deck: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		bundle, ok := bundleOf(exe)
		if !ok {
			return errors.New("this launcher is not in deck.app")
		}
		if err := ReplaceApp(ctx, Latest(), bundle, now); err != nil {
			return err
		}
		if err := exec.Command("open", "-n", bundle).Start(); err != nil {
			return err
		}
	case "windows":
		dir, err := os.MkdirTemp("", "deck-update-")
		if err != nil {
			return err
		}
		setup := filepath.Join(dir, "deck-setup.exe")
		if _, err := Latest().Fetch(ctx, "deck-setup.exe", setup, now); err != nil {
			os.RemoveAll(dir)
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

// bundleOf is the app a binary of macOS stands in.
func bundleOf(exe string) (string, bool) {
	bundle, _, ok := strings.Cut(exe, ".app/")
	return bundle + ".app", ok
}
