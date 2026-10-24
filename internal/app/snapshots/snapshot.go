package snapshots

import (
	"archive/tar"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"deck2/internal/events"
	"deck2/internal/health"
	"deck2/internal/home"
	"deck2/internal/registry"
	"github.com/google/uuid"
)

const (
	// snapshotsKept is how many snapshots of each kind stay in backups/.
	snapshotsKept = 7
	// workKept is how long the work folder of a finished agent process stays for rework.
	workKept = 7 * 24 * time.Hour
)

// Snapshot writes backups/deck-<time>.tar.gz with a copy of the base, files/, the model sessions deck keeps
// and the data folders of projects, then keeps only the newest ones; secrets and the token stay out.
func (s *Service) Snapshot(ctx context.Context) (string, error) {
	return s.take(ctx, home.SnapshotDaily, 0)
}

// BeforeDelete is a snapshot before a project is deleted, kept in a turn of its own beside the daily ones: the base
// with the rows of the project, and only its stored files and data folder.
func (s *Service) BeforeDelete(ctx context.Context, project int64) (string, error) {
	return s.take(ctx, home.SnapshotDelete, project)
}

// take writes a snapshot and says how it went in the health row snapshot: an error, the files it left out, or ok.
func (s *Service) take(ctx context.Context, kind string, project int64) (string, error) {
	path, left, err := s.write(ctx, kind, project)
	switch {
	case err != nil:
		if err := health.Write(ctx, s.St, health.Of{}, health.Snapshot, health.Error, fmt.Sprintf("the snapshot failed: %s", err)); err != nil {
			slog.Error("write health", "err", err)
		}
	case len(left.unread) > 0:
		if err := health.Write(ctx, s.St, health.Of{}, health.Snapshot, health.Warning, fmt.Sprintf("the snapshot left out %d files it could not read whole: %s",
			len(left.unread), strings.Join(left.unread[:min(len(left.unread), 5)], "; "))); err != nil {
			slog.Error("write health", "err", err)
		}
	case left.over > 0:
		if err := health.Write(ctx, s.St, health.Of{}, health.Snapshot, health.OK, fmt.Sprintf("%s, without %d MB of big files of data folders past %d MB",
			filepath.Base(path), left.over>>20, s.Budget/2>>20)); err != nil {
			slog.Error("write health", "err", err)
		}
	default:
		if err := health.Write(ctx, s.St, health.Of{}, health.Snapshot, health.OK, filepath.Base(path)); err != nil {
			slog.Error("write health", "err", err)
		}
	}
	return path, err
}

// write takes a snapshot of every project, or of one when project is not 0.
func (s *Service) write(ctx context.Context, kind string, project int64) (string, leftOut, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir := s.Home.Backups()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", leftOut{}, err
	}
	stale, _ := filepath.Glob(filepath.Join(dir, ".*"))
	parts, _ := filepath.Glob(filepath.Join(dir, "*.part"))
	for _, p := range append(stale, parts...) {
		_ = os.RemoveAll(p)
	}
	id := uuid.NewString()
	path := filepath.Join(dir, kind+"-"+s.Now().UTC().Format(home.SnapshotLayout)+".tar.gz")
	copyDB := filepath.Join(dir, ".deck-"+id+".db")
	files := filepath.Join(dir, ".files-"+id)
	defer os.Remove(copyDB)
	defer os.RemoveAll(files)
	if err := s.hold(ctx, copyDB, files, project); err != nil {
		return "", leftOut{}, err
	}
	part := path + "." + id + ".part"
	left, err := writeSnapshot(part, s.Home, copyDB, files, project, s.Budget/2)
	if err != nil {
		_ = os.Remove(part)
		return "", leftOut{}, err
	}
	if err := os.Rename(part, path); err != nil {
		return "", leftOut{}, err
	}
	old, _ := filepath.Glob(filepath.Join(dir, kind+"-*.tar.gz"))
	slices.Sort(old)
	for len(old) > snapshotsKept {
		_ = os.Remove(old[0])
		old = old[1:]
	}
	s.fit(dir, path)
	s.Bus.Emit(events.Event{Topic: "snapshots"})
	return path, left, nil
}

// hold copies the base and links the stored files into files while no file comes or goes, so the copy names
// only files the snapshot holds; the stored files never change, so the links are their copy.
func (s *Service) hold(ctx context.Context, copyDB, files string, project int64) error {
	s.LockFiles()
	defer s.UnlockFiles()
	if err := s.St.Backup(ctx, copyDB); err != nil {
		return err
	}
	if err := os.Mkdir(files, 0o700); err != nil {
		return err
	}
	stored, err := os.ReadDir(s.Home.Files())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var of map[string]bool
	if project != 0 {
		ids, err := s.St.Q.ListProjectFileIDs(ctx, project)
		if err != nil {
			return err
		}
		of = map[string]bool{}
		for _, id := range ids {
			of[fmt.Sprint(id)] = true
		}
	}
	for _, e := range stored {
		if e.Type().IsRegular() && (of == nil || of[e.Name()]) {
			if err := os.Link(filepath.Join(s.Home.Files(), e.Name()), filepath.Join(files, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

// snapshot is a snapshot of the base and files in backups.
type snapshot struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	TakenAt string `json:"taken_at"`
	Path    string `json:"path"`
}

// list is the snapshots in dir, newest first by the time in their names.
func list(dir string) []snapshot {
	paths, _ := filepath.Glob(filepath.Join(dir, "*-*.tar.gz"))
	stamp := func(p string) string { _, t, _ := strings.Cut(filepath.Base(p), "-"); return t }
	slices.SortFunc(paths, func(a, b string) int { return strings.Compare(stamp(b), stamp(a)) })
	out := make([]snapshot, 0, len(paths))
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		at := ""
		if t, err := time.Parse(home.SnapshotLayout, strings.TrimSuffix(stamp(p), ".tar.gz")); err == nil {
			at = t.UTC().Format(time.RFC3339)
		}
		out = append(out, snapshot{Name: filepath.Base(p), Size: st.Size(), TakenAt: at, Path: p})
	}
	return out
}

// fit removes the oldest snapshots until all of them take at most the budget; kept, the one just taken, stays.
func (s *Service) fit(dir, kept string) {
	snaps := list(dir)
	total := int64(0)
	for _, sn := range snaps {
		total += sn.Size
	}
	for _, sn := range slices.Backward(snaps) {
		if total <= s.Budget {
			return
		}
		if sn.Path != kept && os.Remove(sn.Path) == nil {
			total -= sn.Size
		}
	}
}

// leftOut is what a snapshot left out: files it could not read whole, and the bytes of data files past its budget.
type leftOut struct {
	unread []string
	over   int64
}

// rebuilt are folders of data folders their programs fill again: caches, environments and downloaded models.
func rebuilt(name string) bool {
	return strings.HasPrefix(name, ".") || strings.Contains(strings.ToLower(name), "cache") ||
		slices.Contains([]string{"node_modules", "hf", "venv", "site-packages"}, name)
}

// writeSnapshot archives the base, the stored files and the data folders, or one project's, but for rebuilt folders
// and data past budget bytes; a file it cannot read whole is left out and named.
func writeSnapshot(path string, h home.Layout, copyDB, files string, project, budget int64) (leftOut, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return leftOut{}, err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	db, _ := filepath.Rel(string(h), h.DB())
	if _, err := addFile(tw, copyDB, filepath.ToSlash(db)); err != nil {
		return leftOut{}, err
	}
	stored, _ := filepath.Rel(string(h), h.Files())
	dirs := map[string]string{files: stored}
	data, _ := filepath.Glob(filepath.Join(h.Projects(), "*", "data"))
	if project != 0 {
		data = []string{h.Data(project)}
	}
	for _, d := range data {
		dirs[d], _ = filepath.Rel(string(h), d)
	}
	var left leftOut
	for _, dir := range slices.Sorted(maps.Keys(dirs)) {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				if !os.IsNotExist(err) {
					left.unread = append(left.unread, fmt.Sprintf("%s: %v", p, err))
				}
				return nil
			}
			if d.IsDir() && p != dir && dir != files && rebuilt(d.Name()) {
				return filepath.SkipDir
			}
			if !d.Type().IsRegular() {
				return nil
			}
			if dir != files {
				fi, err := d.Info()
				if err == nil && fi.Size() > budget {
					left.over += fi.Size()
					return nil
				}
				if err == nil {
					budget -= fi.Size()
				}
			}
			rel, _ := filepath.Rel(dir, p)
			name := filepath.ToSlash(filepath.Join(dirs[dir], rel))
			skip, err := addFile(tw, p, name)
			if skip != nil && !os.IsNotExist(skip) {
				left.unread = append(left.unread, fmt.Sprintf("%s: %v", name, skip))
			}
			return err
		})
		if err != nil {
			return leftOut{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return leftOut{}, err
	}
	if err := gz.Close(); err != nil {
		return leftOut{}, err
	}
	return left, f.Close()
}

// addFile puts a file in the archive. A file it cannot open is skipped; one that shrinks while it is copied is
// padded with zeros, so the archive stays whole, and named too. err is a failure of the archive itself.
func addFile(tw *tar.Writer, path, name string) (skip, err error) {
	src, err := os.Open(path)
	if err != nil {
		return err, nil
	}
	defer src.Close()
	fi, err := src.Stat()
	if err != nil {
		return err, nil
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return nil, err
	}
	n, rerr := io.Copy(tw, io.LimitReader(src, fi.Size()))
	if n < fi.Size() {
		if _, err := io.CopyN(tw, zeros{}, fi.Size()-n); err != nil {
			return nil, err
		}
		return cmp.Or(rerr, errors.New("it changed while it was copied")), nil
	}
	return nil, nil
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func (s *Service) snapshotCreate(ctx context.Context, _ registry.Actor, _ struct{}) (any, error) {
	path, err := s.Snapshot(ctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": path}, nil
}
