package snapshots

import (
	"cmp"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"deck2/internal/home"
)

// cachesKept is how long a cache of a module stays untouched before the daily cleanup removes it.
const cachesKept = 30 * 24 * time.Hour

// workSize is what the folders of the modules, their caches among them, and the runs take; by names each module.
type workSize struct {
	modules, caches, runs int64
	by                    string
}

// moduleSize is what the folder of a module takes, and its caches of that.
type moduleSize struct {
	name         string
	size, caches int64
}

// tidyWork walks the folders of the modules once a day, outside any transaction: it removes their caches untouched
// for cachesKept and weighs each folder for the disk row.
func (s *Service) tidyWork(ctx context.Context, now time.Time) {
	names := map[int64]string{}
	if ps, err := s.St.Q.ListProjects(ctx); err == nil {
		for _, p := range ps {
			names[p.ID] = p.Name
		}
	}
	ents, _ := os.ReadDir(s.Home.Projects())
	for _, e := range ents {
		if id, err := strconv.ParseInt(e.Name(), 10, 64); err == nil && e.IsDir() && names[id] == "" {
			names[id] = e.Name()
		}
	}
	var sizes []moduleSize
	var total, caches int64
	for id, name := range names {
		dir := s.Home.Project(id)
		for _, rel := range staleCaches(s.Home, id, now) {
			if err := home.RemoveIn(dir, []string{rel}); err != nil {
				slog.Error("remove a cache of a module", "project", id, "cache", rel, "err", err)
			}
		}
		m := moduleSize{name: name, size: folderSize(dir)}
		for _, c := range cacheFolders(s.Home, id) {
			m.caches += folderSize(filepath.Join(dir, c))
		}
		sizes = append(sizes, m)
		total, caches = total+m.size, caches+m.caches
	}
	slices.SortFunc(sizes, func(a, b moduleSize) int { return cmp.Or(cmp.Compare(b.size, a.size), strings.Compare(a.name, b.name)) })
	parts := make([]string, len(sizes))
	for i, m := range sizes {
		parts[i] = fmt.Sprintf("%s %d", m.name, m.size>>20)
	}
	s.weights.Lock()
	s.work = &workSize{modules: total, caches: caches, runs: folderSize(s.Home.Runs()), by: cmp.Or(strings.Join(parts, ", "), "none")}
	s.weights.Unlock()
}

// cacheFolders are the folders of a module's caches by their paths in its folder: that of its scripts and agents,
// and those its MCP servers keep in their homes.
func cacheFolders(h home.Layout, project int64) []string {
	out := []string{filepath.Join("data", ".cache")}
	servers, _ := os.ReadDir(filepath.Join(h.Project(project), "mcp"))
	for _, sv := range servers {
		if sv.IsDir() {
			out = append(out, filepath.Join("mcp", sv.Name(), ".cache"), filepath.Join("mcp", sv.Name(), ".npm"))
		}
	}
	return out
}

// staleCaches are the caches of a module untouched for cachesKept by their paths in its folder, each child of a cache
// folder whole; uv's is touched by the last run of a script, the caches of an MCP server by its last start.
func staleCaches(h home.Layout, project int64, now time.Time) []string {
	dir := h.Project(project)
	ran := newest(h.Scripts(project), newest(h.Drafts(project), time.Time{}))
	var out []string
	for _, c := range cacheFolders(h, project) {
		ents, _ := os.ReadDir(filepath.Join(dir, c))
		used := time.Time{}
		if strings.HasPrefix(c, "mcp") {
			used = stamp(filepath.Join(dir, filepath.Dir(c)))
		}
		for _, e := range ents {
			rel := filepath.Join(c, e.Name())
			touched := newest(filepath.Join(dir, rel), used)
			if c == filepath.Join("data", ".cache") && e.Name() == "uv" {
				touched = maxTime(touched, ran)
				if now.Sub(touched) <= cachesKept {
					out = append(out, spareEnvironments(h, project, filepath.Join(rel, "environments-v2"))...)
				}
			}
			if now.Sub(touched) > cachesKept {
				out = append(out, rel)
			}
		}
	}
	return out
}

// spareEnvironments are uv's environments under envs past the newest one a script file of that name: uv names one by
// its file and keeps it by its path. One of a name no file has waits for the rule of 30 days.
func spareEnvironments(h home.Layout, project int64, envs string) []string {
	files := map[string]int{}
	for _, d := range []string{h.Scripts(project), h.Drafts(project)} {
		_ = filepath.WalkDir(d, func(_ string, e fs.DirEntry, err error) error {
			if err == nil && e.Type().IsRegular() {
				files[strings.TrimSuffix(e.Name(), filepath.Ext(e.Name()))]++
			}
			return nil
		})
	}
	type env struct {
		name string
		at   time.Time
	}
	by := map[string][]env{}
	ents, _ := os.ReadDir(filepath.Join(h.Project(project), envs))
	for _, e := range ents {
		i := strings.LastIndexByte(e.Name(), '-')
		if i <= 0 || !e.IsDir() || files[e.Name()[:i]] == 0 {
			continue
		}
		by[e.Name()[:i]] = append(by[e.Name()[:i]], env{e.Name(), newest(filepath.Join(h.Project(project), envs, e.Name()), time.Time{})})
	}
	var out []string
	for name, es := range by {
		slices.SortFunc(es, func(a, b env) int { return b.at.Compare(a.at) })
		for _, e := range es[min(files[name], len(es)):] {
			out = append(out, filepath.Join(envs, e.name))
		}
	}
	return out
}

// newest is the latest change of anything under path, or since when later; links are not followed.
func newest(path string, since time.Time) time.Time {
	_ = filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if fi, err := d.Info(); err == nil {
			since = maxTime(since, fi.ModTime())
		}
		return nil
	})
	return since
}

func stamp(path string) time.Time {
	fi, err := os.Lstat(path)
	if err != nil {
		return time.Time{}
	}
	return fi.ModTime()
}

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}
