package home

import (
	"archive/tar"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
)

// unpacked numbers the files Unpack writes apart before they move into place.
var unpacked atomic.Int64

// Unpack writes the folders and files of a tar under dir whole, never past dir nor through a link in it; take refuses
// a name the caller does not expect and stops it, a file keep names stays as it is.
func Unpack(dir string, r io.Reader, take, keep func(name string) bool) error {
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
		if h.Typeflag == tar.TypeReg && keep != nil && keep(name) {
			continue
		}
		at := filepath.FromSlash(name)
		if err := Plain(root, at); err != nil {
			return err
		}
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
	tmp := filepath.Join(filepath.Dir(at), ".tmp-"+strconv.Itoa(os.Getpid())+"-"+strconv.FormatInt(unpacked.Add(1), 10))
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

// PutIn writes a new file under dir whole from what r gives, never past dir nor through a link in it; a name that is
// there already is os.ErrExist.
func PutIn(dir, name string, r io.Reader) error {
	at := filepath.Clean(filepath.FromSlash(name))
	if !filepath.IsLocal(at) {
		return fmt.Errorf("%q is outside %s", name, dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := Plain(root, at); err != nil {
		return err
	}
	if _, err := root.Lstat(at); err == nil {
		return os.ErrExist
	}
	return unpackFile(root, at, r, 0o600)
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
		if err := Plain(root, at); err != nil {
			return err
		}
		if err := root.RemoveAll(at); err != nil {
			return err
		}
	}
	return nil
}

// Plain refuses a name under root whose path holds a link: a link that stays inside root still leads elsewhere than
// its name says, such as from the data of a project into deck's state.
func Plain(root *os.Root, name string) error {
	parts := strings.Split(filepath.ToSlash(name), "/")
	for i := range parts {
		fi, err := root.Lstat(filepath.FromSlash(strings.Join(parts[:i+1], "/")))
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%q goes through a link", name)
		}
	}
	return nil
}
