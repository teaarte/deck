package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"deck2/internal/home"
	"deck2/internal/sandbox"
)

// Homed puts the home of a CLI for project into its box at one path in every box, home in the folder of the CLI's
// lock, with the file login of its one sign-in; a lock beside the home, as of a refresh, is one for all.
func Homed(b *sandbox.Box, h home.Layout, cli string, project int64, login string) string {
	at := filepath.Join(h.AgentLock(cli), "home")
	b.Home = h.Agent(cli, project)
	b.Write = append(b.Write, h.AgentLock(cli))
	b.Shared = map[string]string{at: b.Home, filepath.Join(at, login): filepath.Join(h.Login(cli), login)}
	return at
}

// skillsIdle is how long the skills of a session stay after the last turn of any session wrote its own.
const skillsIdle = 7 * 24 * time.Hour

// Skills writes the files of the skills of a turn anew in the folder of its session at a path named by what they
// hold, so a session finds them at the same path while they do not change; "" without skills.
func Skills(h home.Layout, cli, session string, files map[string]string) (string, error) {
	if len(files) == 0 {
		return "", nil
	}
	sum := sha256.New()
	for _, k := range slices.Sorted(maps.Keys(files)) {
		fmt.Fprintf(sum, "%q %q\n", k, files[k])
	}
	all := h.AgentSkills(cli)
	own := filepath.Join(all, filepath.Base(session))
	if err := os.RemoveAll(own); err != nil {
		return "", err
	}
	dir := filepath.Join(own, hex.EncodeToString(sum.Sum(nil))[:16])
	if err := home.WriteTree(dir, files); err != nil {
		return "", err
	}
	at, err := os.Stat(own)
	if err != nil {
		return "", err
	}
	if es, err := os.ReadDir(all); err == nil {
		for _, e := range es {
			if fi, err := e.Info(); err == nil && at.ModTime().Sub(fi.ModTime()) > skillsIdle {
				_ = os.RemoveAll(filepath.Join(all, e.Name()))
			}
		}
	}
	return dir, nil
}

// Turn is a new folder of the files deck gives a turn of a CLI, which its box reads; done removes it.
func Turn(h home.Layout, cli string) (dir string, done func(), err error) {
	if err := os.MkdirAll(h.AgentTurns(cli), 0o700); err != nil {
		return "", nil, err
	}
	if dir, err = os.MkdirTemp(h.AgentTurns(cli), ""); err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// Hosts are what the box of a CLI reaches: those of its project and the CLI's own.
func Hosts(project []string, own ...string) []string {
	for _, h := range project {
		if h == "*" {
			return []string{"*"}
		}
	}
	return append(append([]string{}, project...), own...)
}

// Copy copies the file or folder from to to, each file that is not there yet.
func Copy(from, to string) error {
	return filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.Type().IsRegular() {
			return err
		}
		rel, err := filepath.Rel(from, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		at := filepath.Join(to, rel)
		if _, err := os.Lstat(at); err == nil {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
			return err
		}
		return home.WriteFile(at, b)
	})
}
