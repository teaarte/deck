package files

import (
	"cmp"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"unicode/utf8"

	"deck2/internal/app/access"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/untrusted"
)

type dataReadIn struct {
	Project string `type:"project" desc:"Project name"`
	Path    string `opt:"" desc:"A file or folder inside the data folder; the folder itself when empty"`
}

// dataEntry is a file or folder of a data folder; .. is the folder above. Path is from the root of the data folder,
// File the file as a media block of a deck screen names it, <project>/<path>.
type dataEntry struct {
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Size     int64  `json:"size,omitempty"`
	Path     string `json:"path"`
	Modified string `json:"modified,omitempty"`
	File     string `json:"file,omitempty"`
}

type dataRead struct {
	Path    string      `json:"path"`
	Entries []dataEntry `json:"entries"`
	Text    string      `json:"text,omitempty"`
	Size    int64       `json:"size,omitempty"`
	// Cut says the list or the text goes on past what data_read gives.
	Cut bool `json:"cut,omitempty"`
}

// dataLimit is the most of a file data_read gives.
const dataLimit = 64 << 10

func (s *Service) dataRead(ctx context.Context, act registry.Actor, in dataReadIn) (dataRead, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return dataRead{}, err
	}
	root, err := os.OpenRoot(s.Home.Data(p.ID))
	if err != nil && strings.Trim(in.Path, "/.") == "" {
		return dataRead{Entries: []dataEntry{}}, nil
	}
	if err != nil {
		return dataRead{}, registry.NotFound("deck: %s has no data folder yet", p.Name)
	}
	defer root.Close()
	path := strings.TrimPrefix(filepath.Clean("/"+in.Path), "/")
	missing := registry.NotFound("deck: no %q in the data folder of %s", in.Path, p.Name)
	f, err := root.OpenFile(cmp.Or(path, "."), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return dataRead{}, missing
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return dataRead{}, missing
	}
	out := dataRead{Path: path, Entries: []dataEntry{}}
	switch {
	case fi.IsDir():
		es, err := f.ReadDir(1001)
		if err != nil && !errors.Is(err, io.EOF) {
			return dataRead{}, missing
		}
		if out.Cut = len(es) > 1000; out.Cut {
			es = es[:1000]
		}
		slices.SortFunc(es, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
		if path != "" {
			out.Entries = append(out.Entries, dataEntry{Name: "..", Dir: true, Path: strings.TrimPrefix(filepath.Dir("/"+path), "/")})
		}
		for _, e := range es {
			d := dataEntry{Name: e.Name(), Dir: e.IsDir(), Path: filepath.ToSlash(filepath.Join(path, e.Name()))}
			if i, err := e.Info(); err == nil {
				d.Modified = store.Time(i.ModTime())
				if !e.IsDir() {
					d.Size, d.File = i.Size(), p.Name+"/"+d.Path
				}
			}
			out.Entries = append(out.Entries, d)
		}
	case fi.Mode().IsRegular():
		b, _ := io.ReadAll(io.LimitReader(f, dataLimit))
		out.Size, out.Cut = fi.Size(), fi.Size() > dataLimit
		for i := 0; out.Cut && i < utf8.UTFMax && !utf8.Valid(b); i++ {
			b = b[:len(b)-1]
		}
		if utf8.Valid(b) {
			out.Text = untrusted.Block("file "+path+" of the data folder of "+p.Name, string(b))
		}
	default:
		return dataRead{}, missing
	}
	return out, nil
}
