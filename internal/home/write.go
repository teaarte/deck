package home

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// Temp is a file written apart in its folder and moved whole into place, so no read finds half of it.
type Temp struct {
	*os.File
	kept bool
}

// Create opens a Temp in dir with mode 600, making dir.
func Create(dir string) (*Temp, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return nil, err
	}
	return &Temp{File: f}, nil
}

// Keep closes t and moves it to path, in its folder.
func (t *Temp) Keep(path string) error {
	if err := t.Close(); err != nil {
		return err
	}
	if err := os.Rename(t.Name(), path); err != nil {
		return err
	}
	t.kept = true
	return nil
}

// Drop closes and removes t unless it was kept.
func (t *Temp) Drop() {
	if !t.kept {
		_ = t.Close()
		_ = os.Remove(t.Name())
	}
}

// WriteFile writes a file whole or not at all, with mode 600, making its folder.
func WriteFile(path string, data []byte) error {
	t, err := Create(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer t.Drop()
	if _, err := t.Write(data); err != nil {
		return err
	}
	return t.Keep(path)
}

// Fresh writes a new file with mode 600 in place of whatever path held, never following a link a program put there.
func Fresh(path, text string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, werr := io.WriteString(f, text)
	return errors.Join(werr, f.Close())
}

// WriteTree writes files by their paths under dir with Fresh, refusing a path that leaves dir.
func WriteTree(dir string, files map[string]string) error {
	for path, text := range files {
		f := filepath.Join(dir, path)
		if !Inside(dir, f) {
			return fmt.Errorf("file %q is outside %s", path, dir)
		}
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			return err
		}
		if err := Fresh(f, text); err != nil {
			return err
		}
	}
	return nil
}
