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

	"golang.org/x/sys/unix"
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

// Trash moves the file name of dir, the one fi says, to the stored file id. The file is taken through a root of dir
// and moved by the descriptor of its folder, so no link leads the move out of dir.
func (l Layout) Trash(dir, name string, fi os.FileInfo, id int64) error {
	parent, base, err := parentIn(dir, name, false)
	if err != nil {
		return err
	}
	defer parent.Close()
	store, stored, err := l.storeOf(id)
	if err != nil {
		return err
	}
	defer store.Close()
	if now, err := parent.root.Lstat(base); err != nil {
		return err
	} else if !os.SameFile(fi, now) {
		return ErrMoved
	}
	if err := unix.Renameat(parent.fd(), base, int(store.Fd()), stored); errors.Is(err, syscall.EXDEV) {
		return ErrApart
	} else if err != nil {
		return &os.LinkError{Op: "rename", Old: name, New: l.File(id), Err: err}
	}
	if moved, err := os.Lstat(l.File(id)); err != nil || !os.SameFile(fi, moved) {
		if unix.Renameat(int(store.Fd()), stored, parent.fd(), base) != nil {
			_ = os.RemoveAll(l.File(id))
		}
		return ErrMoved
	}
	return nil
}

// Untrash puts the stored file id back as the file name of dir, not over a file there (os.ErrExist) nor through a
// link: the link is made by the descriptor of a folder taken through a root of dir.
func (l Layout) Untrash(id int64, dir, name string) error {
	if !filepath.IsLocal(filepath.FromSlash(name)) {
		return fmt.Errorf("%q is outside %s", name, dir)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	parent, base, err := parentIn(dir, name, true)
	if err != nil {
		return err
	}
	defer parent.Close()
	store, stored, err := l.storeOf(id)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := unix.Linkat(int(store.Fd()), stored, parent.fd(), base, 0); err != nil {
		return &os.LinkError{Op: "link", Old: l.File(id), New: name, Err: err}
	}
	return nil
}

// folder is a folder taken through a root, with the descriptor a move or a link goes by.
type folder struct {
	root *os.Root
	dir  *os.File
}

func (f folder) fd() int { return int(f.dir.Fd()) }

func (f folder) Close() { _ = f.dir.Close(); _ = f.root.Close() }

// parentIn is the folder of name inside dir and the last part of name, refused (ErrMoved) when the path goes through
// a link; mkdir makes the missing folders.
func parentIn(dir, name string, mkdir bool) (folder, string, error) {
	at := filepath.Clean(filepath.FromSlash(name))
	if !filepath.IsLocal(at) || at == "." {
		return folder{}, "", fmt.Errorf("%q is outside %s", name, dir)
	}
	top, err := os.OpenRoot(dir)
	if err != nil {
		return folder{}, "", err
	}
	defer top.Close()
	if err := Plain(top, at); err != nil {
		return folder{}, "", ErrMoved
	}
	if mkdir {
		if err := top.MkdirAll(filepath.Dir(at), 0o700); err != nil {
			return folder{}, "", err
		}
	}
	root, err := top.OpenRoot(filepath.Dir(at))
	if errors.Is(err, os.ErrNotExist) {
		return folder{}, "", err
	} else if err != nil {
		return folder{}, "", ErrMoved
	}
	d, err := root.Open(".")
	if err != nil {
		_ = root.Close()
		return folder{}, "", err
	}
	return folder{root, d}, filepath.Base(at), nil
}

// storeOf is the folder of the stored file id, opened, and its name there.
func (l Layout) storeOf(id int64) (*os.File, string, error) {
	path := l.File(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, "", err
	}
	d, err := os.Open(filepath.Dir(path))
	if err != nil {
		return nil, "", err
	}
	return d, filepath.Base(path), nil
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
