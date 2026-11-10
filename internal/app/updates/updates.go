// Package updates finds a newer release of deck once a day, tells the owner, and puts it in place at the owner's
// press: the binary of this variant, checked against the signed list.
package updates

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"deck2/internal/events"
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

	"deck2/internal/access"
	"deck2/internal/egress"
	"deck2/internal/health"
	"deck2/internal/home"
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
	// Commit is the commit of this build, empty when not known.
	Commit string
	// Variant is the binary of this build in a release.
	Variant string
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

// Mark keeps the row of the update with the version and build of this deck and the way it updates; a row of a release
// that says no newer one is out is rewritten, so a row of another build does not stay.
func (s *Service) Mark(ctx context.Context) error {
	return s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		row, err := health.Get(ctx, q, health.Of{}, Subject)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err != nil || s.Version == "dev" || row.Status == health.OK && s.way() != "" {
			text := s.way()
			if s.unknown() != "" {
				text = fmt.Sprintf("%s; its commit is not known", text)
			}
			if _, err := health.Set(ctx, q, s.Now(), health.Of{}, Subject, health.OK, text); err != nil {
				return err
			}
		}
		version := release.Named(s.Version, s.Commit) + " " + s.Variant
		return q.SetHealthVersion(ctx, db.SetHealthVersionParams{Version: version, Subject: Subject})
	})
}

// way is how this deck updates, as its row says: a build from source into an image is the image deck:local that
// install.sh of a checkout builds, for the Dockerfile names the published image in every build.
func (s *Service) way() string {
	switch {
	case s.Version == "dev" && s.Image != "":
		return "built from source into the image deck:local; it updates with sh install.sh in its checkout (git pull, docker build -t deck:local, docker compose up -d)"
	case s.Version == "dev":
		return "built from source; it updates with git pull and go build"
	case s.Image != "":
		return fmt.Sprintf("runs from the image %s; it updates with docker compose pull && docker compose up -d", s.Image)
	}
	return ""
}

// unknown says that a build from source does not know its commit, and how to build one that does.
func (s *Service) unknown() string {
	if s.Version != "dev" || s.Commit != "" {
		return ""
	}
	return "its commit is not known: build it in a git checkout, the image with docker build --build-arg COMMIT=$(git rev-parse --short=12 HEAD)"
}

// inPlace refuses an update in place of a deck that updates another way, in the words of its row.
func (s *Service) inPlace() error {
	switch {
	case s.Version == "dev" && s.Image != "":
		return registry.Invalid("this deck is built from source into the image deck:local; it updates with sh install.sh in its checkout (git pull, docker build -t deck:local, docker compose up -d)")
	case s.Version == "dev":
		return registry.Invalid("this deck is built from source; it updates with git pull and go build")
	case s.Image != "":
		return registry.Invalid("this deck runs from the image %s; it updates with docker compose pull && docker compose up -d", s.Image)
	}
	return nil
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
	resp, err := egress.Fetch(ctx, s.Source.Domains, s.Source.URL+name)
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
	if s.Image != "" {
		text = fmt.Sprintf("deck %s is the latest; it runs from the image %s and updates with docker compose pull && docker compose up -d", s.Version, s.Image)
	}
	newer := release.Newer(fs.Version, s.Version)
	if newer {
		status, text = health.Warning, fmt.Sprintf("deck %s is out; this one is %s", fs.Version, s.Version)
		if s.Image != "" {
			text = fmt.Sprintf("deck %s is out; pull the image %s: docker compose pull && docker compose up -d", fs.Version, s.Image)
		}
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		changed, err := health.Set(ctx, q, s.Now(), health.Of{}, Subject, status, text)
		if err != nil || !changed || !newer {
			return err
		}
		spaces, err := q.ListWorkspaces(ctx)
		if err != nil {
			return err
		}
		buttons, _ := json.Marshal([]map[string]any{{"label": "Update", "command": registry.Deck("update_install"), "icon": "download"}})
		if s.Image != "" {
			buttons = []byte("[]")
		}
		for _, ws := range spaces {
			m, err := access.Master(ctx, q, ws)
			if err != nil {
				continue
			}
			if _, err := store.AddNote(ctx, q, db.AddFeedParams{CreatedAt: store.Time(s.Now()), WorkspaceID: ws, ProjectID: m.ID,
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
	// Unknown says that a build from source does not know its commit.
	Unknown string `json:"unknown,omitempty"`
	Variant string `json:"variant"`
	Latest  string `json:"latest,omitempty"`
	Newer   bool   `json:"newer"`
	Error   string `json:"error,omitempty"`
}

func (s *Service) out() versionOut {
	s.mu.Lock()
	defer s.mu.Unlock()
	return versionOut{Version: release.Named(s.Version, s.Commit), Commit: s.Commit, Unknown: s.unknown(), Variant: s.Variant,
		Latest: s.latest.Version, Newer: release.Newer(s.latest.Version, s.Version), Error: s.failed}
}

func (s *Service) version(context.Context, registry.Actor, struct{}) (versionOut, error) {
	return s.out(), nil
}

func (s *Service) checkNow(ctx context.Context, _ registry.Actor, _ struct{}) (any, error) {
	if s.Version == "dev" {
		return nil, s.inPlace()
	}
	_, _ = s.check(ctx)
	return s.out(), nil
}

// update puts the latest release in place and restarts deck in it.
func (s *Service) update(ctx context.Context, _ registry.Actor, _ struct{}) (any, error) {
	if err := s.inPlace(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if s.busy {
		s.mu.Unlock()
		return nil, registry.Conflict("an update is under way")
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
	fs, err := s.check(ctx)
	if err != nil {
		return nil, registry.Refuse("the latest release: %v", err)
	}
	if !release.Newer(fs.Version, s.Version) {
		return nil, registry.Invalid("%s is the latest release", s.Version)
	}
	f, ok := fs.File(s.Variant)
	if !ok {
		return nil, registry.Refuse("release %s has no build %s", fs.Version, s.Variant)
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
		return nil, registry.Refuse("a snapshot before the update: %v", err)
	}
	os.Remove(old)
	if err := errors.Join(os.Chmod(fresh, 0o755), os.Link(exe, old),
		home.WriteFile(s.Pending, release.Pending{Exe: exe, Old: old, Version: fs.Version}.JSON())); err != nil {
		os.Remove(fresh)
		os.Remove(old)
		os.Remove(s.Pending)
		return nil, registry.Refuse("keep the binary before the update: %v", err)
	}
	if err := os.Rename(fresh, exe); err != nil {
		os.Remove(fresh)
		os.Remove(old)
		os.Remove(s.Pending)
		return nil, registry.Refuse("put the update in place: %v", err)
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
		return registry.Refuse("the sha256 of %s is not the one of the signed list", f.Name)
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
	resp, err := egress.Download(ctx, s.Source.Domains, s.Source.URL+f.Name)
	if err != nil {
		return registry.Refuse("download %s: %v", f.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Scheme != "https" {
		return registry.Refuse("download %s: %s", f.Name, resp.Status)
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
		return registry.Refuse("download %s: %v", f.Name, err)
	case n != f.Size:
		return registry.Refuse("%s is %d bytes, the signed list says %d", f.Name, n, f.Size)
	case hex.EncodeToString(h.Sum(nil)) != f.SHA256:
		return registry.Refuse("the sha256 of %s is not the one of the signed list", f.Name)
	}
	return nil
}

// runs checks that a binary runs on this machine and is the version it should be.
func runs(ctx context.Context, bin, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-version").Output()
	if err != nil {
		return registry.Refuse("the new binary does not run here: %v", err)
	}
	if got, _, _ := strings.Cut(strings.TrimSpace(string(out)), " "); got != version {
		return registry.Refuse("the new binary says it is %q, not %s", got, version)
	}
	return nil
}

func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("version_get", "Version of deck",
			"Gives the version, commit and build of deck, the latest release deck found and whether it is newer.",
			registry.OwnerSide, s.version).On(events.Health),
		registry.Def("update_check", "Look for an update",
			"Reads the latest release of deck now and tells the owner when it is newer.",
			registry.OwnerOnly, s.checkNow),
		registry.Def("update_install", "Update deck",
			"Puts the latest release of deck in place, checked against its signed list, and restarts deck in it; agents go on in their sessions.",
			registry.OwnerOnly, s.update),
	}
}
