package modules

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"deck2/internal/docs"
	"deck2/internal/home"
)

// ReadFolder reads the text files of the folder rel of top, never through a link out of top, and leaves hidden
// entries out; what it cannot take, symbolic links included, is in skipped.
func ReadFolder(top, rel string) (files []docs.File, skipped []string, err error) {
	root, err := os.OpenRoot(top)
	if err != nil {
		return nil, nil, err
	}
	defer root.Close()
	if rel != "." {
		sub, err := root.OpenRoot(rel)
		if err != nil {
			return nil, nil, err
		}
		defer sub.Close()
		root = sub
	}
	err = fs.WalkDir(root.FS(), ".", func(rel string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			skipped = append(skipped, rel+": a symbolic link")
			return nil
		}
		b, err := home.ReadRoot(root, rel, docs.MaxFile)
		switch {
		case errors.Is(err, home.ErrTooBig):
			skipped = append(skipped, rel+": larger than 1 MB")
			return nil
		case err != nil:
			skipped = append(skipped, rel+": not a regular file")
			return nil
		}
		if !utf8.Valid(b) {
			skipped = append(skipped, rel+": not text")
			return nil
		}
		files = append(files, docs.File{Name: filepath.ToSlash(rel), Text: string(b)})
		return nil
	})
	return files, skipped, err
}
