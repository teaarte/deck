package home

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"

	"deck2/internal/nonce"
)

// Unpack writes the folders and regular files of a tar under dir by their names, each file whole, never past dir nor
// through a link in it; take refuses a name the caller does not expect, and nothing else of the tar is written.
func Unpack(dir string, r io.Reader, take func(name string) bool) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := path.Clean(h.Name)
		if !filepath.IsLocal(filepath.FromSlash(name)) || !take(name) {
			return fmt.Errorf("%q is outside the folders it may write", h.Name)
		}
		at := filepath.FromSlash(name)
		switch h.Typeflag {
		case tar.TypeDir:
			err = root.MkdirAll(at, 0o700)
		case tar.TypeReg:
			err = unpackFile(root, at, tr, os.FileMode(h.Mode)&0o700|0o600)
			if err == nil && !h.ModTime.IsZero() {
				err = root.Chtimes(at, h.ModTime, h.ModTime)
			}
		}
		if err != nil {
			return err
		}
	}
}

// unpackFile writes a file apart beside its place and moves it there whole.
func unpackFile(root *os.Root, at string, r io.Reader, mode os.FileMode) error {
	if err := root.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(at), ".tmp-"+nonce.Hex(8))
	f, err := root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, r)
	if err = errors.Join(err, f.Close()); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	if err := root.Rename(tmp, at); err != nil {
		_ = root.Remove(tmp)
		return err
	}
	return nil
}

// RemoveIn removes the files and folders of names under dir, never past it.
func RemoveIn(dir string, names []string) error {
	root, err := os.OpenRoot(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	for _, n := range names {
		at := filepath.FromSlash(path.Clean(n))
		if !filepath.IsLocal(at) {
			return fmt.Errorf("%q is outside %s", n, dir)
		}
		if err := root.RemoveAll(at); err != nil {
			return err
		}
	}
	return nil
}
