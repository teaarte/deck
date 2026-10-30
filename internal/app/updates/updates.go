// Package updates finds a newer release of deck once a day, tells the owner, and puts it in place at the owner's
// press: the binary of this variant, checked against the signed list, or its launcher's update.
package updates

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"deck2/internal/app/access"
	"deck2/internal/gateway"
	"deck2/internal/health"
	"deck2/internal/registry"
	"deck2/internal/release"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Source is where the releases of deck are: the folder of the latest one's files, the hosts it and its redirects
// reach, and the key its list is signed with.
type Source struct {
	URL     string
	Domains []string
	Key     ed25519.PublicKey
}

// GitHub is where deck is released.
var GitHub = Source{URL: "https://github.com/teaarte/deck/releases/latest/download/", Domains: []string{"github.com", "githubusercontent.com"}}

// Subject is the health row of the update of deck.
const Subject = "update"

type Service struct {
	St      *store.Store
	Now     func() time.Time
	Source  Source
	Version string
	// Variant is the binary of this build in a release; Launcher marks a deck its launcher updates.
	Variant  string
	Launcher bool
	// Image is the image of a deck that runs in a container: a newer one comes as a newer image, never in place.
	Image string
	// Exe is the binary of deck; Restart ends deck to take an update; Pending is where the update waits to be served.
	Exe      func() (string, error)
	Restart  func(release.Restart)
	Pending  string
	Snapshot func(context.Context) error
	Go       func(func(context.Context)) bool

	mu      sync.Mutex
	checked time.Time
	latest  release.Files
	failed  string
	busy    bool
}

// Mark keeps the row of the update with the version and build of this deck; a build from source says how it updates.
func (s *Service) Mark(ctx context.Context) error {
	return s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := health.Get(ctx, q, health.Of{}, Subject); errors.Is(err, sql.ErrNoRows) || s.Version == "dev" {
			text := ""
			if s.Version == "dev" {
				text = "built from source; it updates with git pull and go build"
			}
			if _, err := health.Set(ctx, q, health.Of{}, Subject, health.OK, text); err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		version := s.Version + " " + s.Variant
		if s.Version == "dev" {
			version = s.Variant
		}
		return q.SetHealthVersion(ctx, db.SetHealthVersionParams{Version: version, Subject: Subject})
	})
}

// Daily looks for a newer release once a day.
func (s *Service) Daily(ctx context.Context, now time.Time) {
	s.mu.Lock()
	due := now.Sub(s.checked) >= 24*time.Hour
	if due {
		s.checked = now
	}
	s.mu.Unlock()
	if due && s.Version != "dev" {
		if _, err := s.check(ctx); err != nil {
			slog.Warn("look for an update of deck", "err", err)
		}
	}
}

// latestList is the signed list of the latest release.
func (s *Service) latestList(ctx context.Context) (release.Files, error) {
	list, err := s.get(ctx, release.List, 1<<20)
	if err != nil {
		return release.Files{}, err
	}
	sig, err := s.get(ctx, release.Signature, 1<<10)
	if err != nil {
		return release.Files{}, err
	}
	return release.Read(s.Source.Key, list, sig)
}

func (s *Service) get(ctx context.Context, name string, limit int64) ([]byte, error) {
	resp, err := gateway.Fetch(ctx, s.Source.Domains, s.Source.URL+name)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Scheme != "https" {
		return nil, fmt.Errorf("%s of the latest release: %s from %s", name, resp.Status, resp.Request.URL.Host)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// check reads the latest release and tells the owner of a newer one, once a version.
func (s *Service) check(ctx context.Context) (release.Files, error) {
	fs, err := s.latestList(ctx)
	s.mu.Lock()
	if s.failed = ""; err != nil {
		s.failed = err.Error()
	} else {
		s.latest = fs
	}
	s.mu.Unlock()
	if err != nil {
		return fs, err
	}
	status, text := health.OK, fmt.Sprintf("deck %s is the latest", s.Version)
	newer := release.Newer(fs.Version, s.Version)
	if newer {
		status, text = health.Warning, fmt.Sprintf("deck %s is out; this one is %s", fs.Version, s.Version)
		if s.Image != "" {
			text = fmt.Sprintf("deck %s is out; pull the image %s: docker compose pull && docker compose up -d", fs.Version, s.Image)
		}
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		changed, err := health.Set(ctx, q, health.Of{}, Subject, status, text)
		if err != nil || !changed || !newer {
			return err
		}
		spaces, err := q.ListWorkspaces(ctx)
		if err != nil {
			return err
		}
		buttons, _ := json.Marshal([]map[string]any{{"label": "Update", "command": registry.Builtin + ".update", "icon": "download"}})
		if s.Image != "" {
			buttons = []byte("[]")
		}
		for _, ws := range spaces {
			m, err := access.Master(ctx, q, ws)
			if err != nil {
				continue
			}
			if _, err := q.AddFeed(ctx, db.AddFeedParams{CreatedAt: store.Time(s.Now()), WorkspaceID: ws, ProjectID: m.ID, Kind: store.FeedNote,
				Author: "deck", Text: text, Actions: string(buttons), Params: "{}", Cause: Subject}); err != nil {
				return err
			}
		}
		return nil
	})
	return fs, err
}

// versionOut is the version of this deck and what deck knows of the latest release.
type versionOut struct {
	Version string `json:"version"`
	Commit  string `json:"commit,omitempty"`
	Variant string `json:"variant"`
	Latest  string `json:"latest,omitempty"`
	Newer   bool   `json:"newer"`
	Error   string `json:"error,omitempty"`
}

func (s *Service) out() versionOut {
	s.mu.Lock()
	defer s.mu.Unlock()
	return versionOut{Version: s.Version, Commit: release.Commit, Variant: s.Variant, Latest: s.latest.Version,
		Newer: release.Newer(s.latest.Version, s.Version), Error: s.failed}
}

func (s *Service) version(context.Context, registry.Actor, struct{}) (versionOut, error) {
	return s.out(), nil
}

func (s *Service) checkNow(ctx context.Context, _ registry.Actor, _ struct{}) (any, error) {
	if s.Version == "dev" {
		return nil, registry.Invalid("deck: this deck is built from source; it updates with git pull and go build")
	}
	_, _ = s.check(ctx)
	return s.out(), nil
}

// update puts the latest release in place and restarts deck in it; a launcher updates what it runs itself.
func (s *Service) update(ctx context.Context, _ registry.Actor, _ struct{}) (any, error) {
	if s.Version == "dev" {
		return nil, registry.Invalid("deck: this deck is built from source; it updates with git pull and go build")
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return nil, registry.Conflict("deck: an update is under way")
	}
	s.busy = true
	s.mu.Unlock()
	done := false
	defer func() {
		if !done {
			s.mu.Lock()
			s.busy = false
			s.mu.Unlock()
		}
	}()
	if s.Image != "" {
		return nil, registry.Invalid("deck: this deck runs from the image %s; a newer one comes as a newer image: docker compose pull && docker compose up -d", s.Image)
	}
	fs, err := s.check(ctx)
	if err != nil {
		return nil, registry.Refuse("deck: the latest release: %v", err)
	}
	if !release.Newer(fs.Version, s.Version) {
		return nil, registry.Invalid("deck: %s is the latest release", s.Version)
	}
	if s.Launcher {
		if !s.Go(func(context.Context) { time.Sleep(time.Second); s.Restart(release.Restart{Launcher: true}) }) {
			return nil, registry.Conflict("deck: deck is stopping; update it once it has started again")
		}
		done = true
		return map[string]any{"version": fs.Version, "by": "launcher"}, nil
	}
	f, ok := fs.File(s.Variant)
	if !ok {
		return nil, registry.Refuse("deck: release %s has no build %s", fs.Version, s.Variant)
	}
	exe, err := s.Exe()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return nil, err
	}
	fresh, old := exe+".new", exe+".old"
	if err := s.download(ctx, f, fresh); err != nil {
		os.Remove(fresh)
		return nil, err
	}
	if err := runs(ctx, fresh, fs.Version); err != nil {
		os.Remove(fresh)
		return nil, err
	}
	// What ran is what goes in place: its sha256 again, then the mode of a program.
	if err := sameFile(fresh, f); err != nil {
		os.Remove(fresh)
		return nil, err
	}
	if err := s.Snapshot(ctx); err != nil {
		os.Remove(fresh)
		return nil, registry.Refuse("deck: a snapshot before the update: %v", err)
	}
	os.Remove(old)
	if err := errors.Join(os.Chmod(fresh, 0o755), os.Link(exe, old),
		release.WritePending(s.Pending, release.Pending{Exe: exe, Old: old, Version: fs.Version})); err != nil {
		os.Remove(fresh)
		os.Remove(old)
		os.Remove(s.Pending)
		return nil, registry.Refuse("deck: keep the binary before the update: %v", err)
	}
	if err := os.Rename(fresh, exe); err != nil {
		os.Remove(fresh)
		os.Remove(old)
		os.Remove(s.Pending)
		return nil, registry.Refuse("deck: put the update in place: %v", err)
	}
	syncDir(filepath.Dir(exe))
	done = true
	if !s.Go(func(context.Context) { time.Sleep(time.Second); s.Restart(release.Restart{Exe: exe, Old: old}) }) {
		return map[string]any{"version": fs.Version, "by": "deck", "restart": "deck is stopping; it starts in the new version"}, nil
	}
	return map[string]any{"version": fs.Version, "by": "deck"}, nil
}

// sameFile checks a file of a release once more by its size and sha256.
func sameFile(path string, f release.File) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer in.Close()
	h := sha256.New()
	n, err := io.Copy(h, in)
	if err != nil || n != f.Size || hex.EncodeToString(h.Sum(nil)) != f.SHA256 {
		return registry.Refuse("deck: the sha256 of %s is not the one of the signed list", f.Name)
	}
	return nil
}

// syncDir writes a folder's entries to the disk, so a rename in it outlives a loss of power.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

// download writes file f of the latest release to path once its size and sha256 are those of the signed list.
func (s *Service) download(ctx context.Context, f release.File, path string) error {
	resp, err := gateway.Download(ctx, s.Source.Domains, s.Source.URL+f.Name)
	if err != nil {
		return registry.Refuse("deck: download %s: %v", f.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Scheme != "https" {
		return registry.Refuse("deck: download %s: %s", f.Name, resp.Status)
	}
	os.Remove(path)
	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, f.Size+1))
	if serr := out.Sync(); err == nil {
		err = serr
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		return registry.Refuse("deck: download %s: %v", f.Name, err)
	case n != f.Size:
		return registry.Refuse("deck: %s is %d bytes, the signed list says %d", f.Name, n, f.Size)
	case hex.EncodeToString(h.Sum(nil)) != f.SHA256:
		return registry.Refuse("deck: the sha256 of %s is not the one of the signed list", f.Name)
	}
	return nil
}

// runs checks that a binary runs on this machine and is the version it should be.
func runs(ctx context.Context, bin, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-version").Output()
	if err != nil {
		return registry.Refuse("deck: the new binary does not run here: %v", err)
	}
	if got, _, _ := strings.Cut(strings.TrimSpace(string(out)), " "); got != version {
		return registry.Refuse("deck: the new binary says it is %q, not %s", got, version)
	}
	return nil
}

func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("version", "Version of deck",
			"Gives the version, commit and build of deck, the latest release deck found and whether it is newer.",
			registry.OwnerSide, s.version).On("health"),
		registry.Def("update_check", "Look for an update",
			"Reads the latest release of deck now and tells the owner when it is newer.",
			registry.OwnerOnly, s.checkNow),
		registry.Def("update", "Update deck",
			"Puts the latest release of deck in place, checked against its signed list, and restarts deck in it; agents go on in their sessions.",
			registry.OwnerOnly, s.update),
	}
}
