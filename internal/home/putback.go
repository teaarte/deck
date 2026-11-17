package home

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
)

// PutBack puts the base of the snapshot at path in place of deck's base, its journal files gone, with deck stopped;
// the next start that migrates it takes a snapshot again.
func (l Layout) PutBack(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return fmt.Errorf("%s holds no state/deck.db", path)
		}
		if err != nil {
			return err
		}
		if h.Name == "state/deck.db" && h.Typeflag == tar.TypeReg {
			break
		}
	}
	t, err := Create(l.State())
	if err != nil {
		return err
	}
	defer t.Drop()
	if _, err := io.Copy(t, tr); err != nil {
		return err
	}
	for _, f := range []string{l.DB() + "-wal", l.DB() + "-shm", l.UpdateKept()} {
		if err := os.Remove(f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
	}
	return t.Keep(l.DB())
}
