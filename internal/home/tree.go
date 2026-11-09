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
	"syscall"
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

// ErrApart is a move between two file systems, which deck does not make as a copy.
var ErrApart = errors.New("the data folder and deck's stored files are on different file systems")

// ErrMoved is a file that is not the one checked before, or a path that now goes through a link.
var ErrMoved = errors.New("the file changed meanwhile")

// Trash moves the file name of dir, the one fi says, to the stored file id, through one root of deck's folder.
func (l Layout) Trash(dir, name string, fi os.FileInfo, id int64) error {
	root, at, err := l.under(dir, name)
	if err != nil {
		return err
	}
	defer root.Close()
	now, err := root.Lstat(at)
	if err != nil {
		return err
	}
	if !os.SameFile(fi, now) {
		return ErrMoved
	}
	stored, _ := filepath.Rel(string(l), l.File(id))
	if err := root.MkdirAll(filepath.Dir(stored), 0o700); err != nil {
		return err
	}
	if err := root.Rename(at, stored); errors.Is(err, syscall.EXDEV) {
		return ErrApart
	} else if err != nil {
		return err
	}
	return nil
}

// Untrash puts the stored file id back as the file name of dir, never over a file there: that is os.ErrExist.
func (l Layout) Untrash(id int64, dir, name string) error {
	if !filepath.IsLocal(filepath.FromSlash(name)) {
		return fmt.Errorf("%q is outside %s", name, dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	root, at, err := l.under(dir, name)
	if err != nil {
		return err
	}
	defer root.Close()
	if err := root.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		return err
	}
	stored, _ := filepath.Rel(string(l), l.File(id))
	if err := root.Link(stored, at); errors.Is(err, os.ErrExist) {
		return os.ErrExist
	} else if err != nil {
		return err
	}
	return nil
}

// under is deck's folder as a root and name of dir in it, refused when its path goes through a link.
func (l Layout) under(dir, name string) (*os.Root, string, error) {
	rel, err := filepath.Rel(string(l), dir)
	if err != nil || !filepath.IsLocal(rel) {
		return nil, "", fmt.Errorf("%s is outside deck's folder", dir)
	}
	at := filepath.Join(rel, filepath.Clean(filepath.FromSlash(name)))
	root, err := os.OpenRoot(string(l))
	if err != nil {
		return nil, "", err
	}
	if err := Plain(root, at); err != nil {
		root.Close()
		return nil, "", ErrMoved
	}
	return root, at, nil
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
