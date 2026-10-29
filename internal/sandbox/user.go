package sandbox

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
)

// agentUser is the user of deck's container that the programs of agents and scripts run as (DECK_AGENT_USER):
// deck runs as another, so its state stays closed to them.
type agentUser struct {
	uid, gid uint32
}

var (
	agentOnce sync.Once
	agentIs   *agentUser
	agentErr  error
)

func theAgent() (*agentUser, error) {
	agentOnce.Do(func() {
		name := os.Getenv("DECK_AGENT_USER")
		if name == "" {
			return
		}
		u, err := user.Lookup(name)
		if err != nil {
			agentErr = fmt.Errorf("the user of agents %s: %w", name, err)
			return
		}
		uid, _ := strconv.ParseUint(u.Uid, 10, 32)
		gid, _ := strconv.ParseUint(u.Gid, 10, 32)
		if int(uid) == os.Getuid() || uid == 0 {
			agentErr = fmt.Errorf("the user of agents %s is root or the user deck runs as", name)
			return
		}
		agentIs = &agentUser{uid: uint32(uid), gid: uint32(gid)}
	})
	return agentIs, agentErr
}

// HasAgentUser says programs of agents and scripts run as the user of agents of deck's container.
func HasAgentUser() bool {
	a, _ := theAgent()
	return a != nil
}

// Contained says deck runs agents and scripts as the user of agents of its container, not in a sandbox of its own:
// their network is not limited and they share one user.
func Contained() bool {
	a, _ := theAgent()
	return a != nil && os.Getenv("DECK_SANDBOX") != "bwrap"
}

// AsAgent runs cmd as the user of agents of deck's container and gives that user the folders cmd writes and the paths
// it reads; outside the container it does nothing.
func AsAgent(cmd *exec.Cmd, write, read []string) error {
	a, err := theAgent()
	if err != nil || a == nil {
		return err
	}
	for _, w := range write {
		if err := a.hand(w, true); err != nil {
			return err
		}
	}
	for _, r := range read {
		if err := a.hand(r, false); err != nil {
			return err
		}
	}
	for _, d := range []string{cmd.Dir, filepath.Dir(cmd.Path)} {
		if filepath.IsAbs(d) {
			if err := a.pass(d); err != nil {
				return err
			}
		}
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: a.uid, Gid: a.gid}
	return nil
}

// hand gives the user of agents path, its own to write or readable, through a root it cannot lead out of; a file
// another name holds stays as it is.
func (a *agentUser) hand(path string, writes bool) error {
	if !filepath.IsAbs(path) {
		return nil
	}
	root, rel, err := a.reach(filepath.Clean(path), false)
	if err != nil || root == nil {
		return err
	}
	defer root.Close()
	deck := uint32(os.Getuid())
	if fi, err := root.Lstat(rel); err != nil || owner(fi) != deck {
		return nil
	}
	// What deck made is collected first and changed deepest first, so no folder is the agent's while deck is inside it.
	var mine []string
	_ = fs.WalkDir(root.FS(), filepath.ToSlash(rel), func(p string, e fs.DirEntry, err error) error {
		fi, ierr := e.Info()
		if err != nil || ierr != nil || owner(fi) != deck {
			if e != nil && e.IsDir() && p != filepath.ToSlash(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && fi.Mode().IsRegular() && st.Nlink > 1 {
			return nil
		}
		mine = append(mine, p)
		return nil
	})
	for i := len(mine) - 1; i >= 0; i-- {
		p := filepath.FromSlash(mine[i])
		fi, err := root.Lstat(p)
		switch {
		case err != nil:
		case writes:
			err = root.Lchown(p, int(a.uid), int(a.gid))
		case fi.IsDir():
			err = root.Chmod(p, fi.Mode().Perm()|0o005)
		case fi.Mode().IsRegular():
			err = root.Chmod(p, fi.Mode().Perm()|0o004)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// pass lets the user of agents pass the folders of deck from dir up, so a program finds what it is given in them.
func (a *agentUser) pass(dir string) error {
	root, _, err := a.reach(filepath.Clean(dir), true)
	if root != nil {
		root.Close()
	}
	return err
}

// reach opens the deepest folder on the way to path the user of agents cannot change, lets it pass the folders of
// deck down to path's own (path too with self), and gives that folder as a root with the way from it.
func (a *agentUser) reach(path string, self bool) (*os.Root, string, error) {
	deck := uint32(os.Getuid())
	top, parts := "/", splitPath(path)
	i := 0
	for ; i < len(parts); i++ {
		next := filepath.Join(top, parts[i])
		fi, err := os.Lstat(next)
		if err != nil {
			return nil, "", nil
		}
		if !fi.IsDir() || owner(fi) == a.uid {
			break
		}
		if owner(fi) == deck && fi.Mode().Perm()&0o001 == 0 && (self || i < len(parts)-1) {
			if err := os.Chmod(next, fi.Mode().Perm()|0o001); err != nil {
				return nil, "", err
			}
		}
		top = next
	}
	root, err := os.OpenRoot(top)
	if err != nil {
		return nil, "", err
	}
	rel := "."
	if i < len(parts) {
		rel = filepath.Join(parts[i:]...)
	}
	last := len(parts) - 1
	if self {
		last++
	}
	for d, j := "", i; j < last; j++ {
		d = filepath.Join(d, parts[j])
		if fi, err := root.Lstat(d); err == nil && fi.IsDir() && owner(fi) == deck && fi.Mode().Perm()&0o001 == 0 {
			if err := root.Chmod(d, fi.Mode().Perm()|0o001); err != nil {
				root.Close()
				return nil, "", err
			}
		}
	}
	return root, rel, nil
}

func splitPath(path string) []string {
	var parts []string
	for p := filepath.Clean(path); p != "/" && p != "."; p = filepath.Dir(p) {
		parts = append([]string{filepath.Base(p)}, parts...)
	}
	return parts
}

func owner(fi fs.FileInfo) uint32 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return st.Uid
	}
	return 0
}
