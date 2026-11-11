package remote

import (
	"archive/tar"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"deck2/internal/home"
)

// stamp tells whether a file changed: its size and the time of its last write.
type stamp struct {
	Size int64
	Mod  time.Time
}

// walk lists the regular files of the folders roots under dir by their names, never through a link nor into the caches
// of a project.
func walk(dir string, roots []string) (map[string]stamp, error) {
	out := map[string]stamp{}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, r := range roots {
		err := fs.WalkDir(root.FS(), r, func(p string, d fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			if err == nil && d.IsDir() && cache(p) {
				return fs.SkipDir
			}
			if err != nil || !d.Type().IsRegular() {
				return err
			}
			fi, err := d.Info()
			if err != nil {
				return nil
			}
			out[p] = stamp{Size: fi.Size(), Mod: fi.ModTime()}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// stampOf is the stamp of the regular file name under dir, if there is one.
func stampOf(dir, name string) (stamp, bool) {
	fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(name)))
	if err != nil || !fi.Mode().IsRegular() {
		return stamp{}, false
	}
	return stamp{Size: fi.Size(), Mod: fi.ModTime()}, true
}

// cache says p is the folder of caches in the data of a project: the caches of programs stay on each machine.
func cache(p string) bool {
	return path.Base(p) == ".cache" && path.Base(path.Dir(p)) == "data" && strings.HasPrefix(p, "work/projects/")
}

// size is what the files of a list hold together.
func size(files map[string]stamp) int64 {
	var n int64
	for _, s := range files {
		n += s.Size
	}
	return n
}

// pack writes the files names under dir to w as a tar; a file gone meanwhile, or no longer a regular one, stays out.
func pack(w io.Writer, dir string, names []string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	tw := tar.NewWriter(w)
	for _, n := range slices.Sorted(slices.Values(names)) {
		if home.Plain(root, n) != nil {
			continue
		}
		f, err := home.OpenRegular(root, n)
		if err != nil {
			continue
		}
		fi, err := f.Stat()
		if err != nil {
			f.Close()
			continue
		}
		h := &tar.Header{Typeflag: tar.TypeReg, Name: n, Size: fi.Size(), Mode: int64(fi.Mode().Perm()), ModTime: fi.ModTime()}
		if err := tw.WriteHeader(h); err != nil {
			f.Close()
			return err
		}
		_, err = io.CopyN(tw, f, fi.Size())
		f.Close()
		if err != nil {
			return err
		}
	}
	return tw.Close()
}

// under says name is one of roots or inside one.
func under(name string, roots []string) bool {
	name = path.Clean(name)
	return slices.ContainsFunc(roots, func(r string) bool { return name == r || strings.HasPrefix(name, r+"/") })
}

// changed are the files of now that are new or changed since before, and the names of before gone.
func changed(before, now map[string]stamp) (names, gone []string) {
	for n, s := range now {
		if b, ok := before[n]; !ok || b.Size != s.Size || !b.Mod.Equal(s.Mod) {
			names = append(names, n)
		}
	}
	for n := range before {
		if _, ok := now[n]; !ok {
			gone = append(gone, n)
		}
	}
	slices.Sort(gone)
	return names, gone
}
