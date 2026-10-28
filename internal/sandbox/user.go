package sandbox

import (
	"errors"
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
		if int(uid) == os.Getuid() {
			agentErr = fmt.Errorf("the user of agents %s is the user deck runs as", name)
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
		if !filepath.IsAbs(d) {
			continue
		}
		if err := a.noAgentLinks(d); err != nil {
			return err
		}
		if err := pass(d); err != nil {
			return err
		}
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	}
	cmd.SysProcAttr.Credential = &syscall.Credential{Uid: a.uid, Gid: a.gid}
	return nil
}

// hand gives the user of agents path: what deck made in it becomes the agent's when it writes there, readable when it
// only reads, and the folders of deck above it can be passed. A link the agent could have made on the way is refused.
func (a *agentUser) hand(path string, writes bool) error {
	if !filepath.IsAbs(path) {
		return nil
	}
	path = filepath.Clean(path)
	if err := a.noAgentLinks(path); err != nil {
		return err
	}
	if err := pass(filepath.Dir(path)); err != nil {
		return err
	}
	deck := uint32(os.Getuid())
	root, err := os.Lstat(path)
	if err != nil || owner(root) != deck {
		return nil
	}
	// What deck made is collected first and changed deepest first, so no folder is the agent's while deck is inside it.
	var mine []string
	_ = filepath.WalkDir(path, func(p string, e fs.DirEntry, err error) error {
		fi, ierr := e.Info()
		if err != nil || ierr != nil || owner(fi) != deck {
			if e != nil && e.IsDir() && p != path {
				return filepath.SkipDir
			}
			return nil
		}
		if st, ok := fi.Sys().(*syscall.Stat_t); ok && writes && fi.Mode().IsRegular() && st.Nlink > 1 {
			return nil
		}
		mine = append(mine, p)
		return nil
	})
	for i := len(mine) - 1; i >= 0; i-- {
		p := mine[i]
		fi, err := os.Lstat(p)
		switch {
		case err != nil:
		case writes:
			err = os.Lchown(p, int(a.uid), int(a.gid))
		case fi.IsDir():
			err = os.Chmod(p, fi.Mode().Perm()|0o005)
		case fi.Mode().IsRegular():
			err = os.Chmod(p, fi.Mode().Perm()|0o004)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// pass lets others pass the folders of deck from dir up, so a program finds what it is given in them; it lists none.
func pass(dir string) error {
	deck := uint32(os.Getuid())
	for d := filepath.Clean(dir); d != "/" && d != "."; d = filepath.Dir(d) {
		if fi, err := os.Lstat(d); err == nil && fi.IsDir() && owner(fi) == deck && fi.Mode().Perm()&0o001 == 0 {
			if err := os.Chmod(d, fi.Mode().Perm()|0o001); err != nil {
				return err
			}
		}
	}
	return nil
}

// noAgentLinks refuses a path through a link in a folder the user of agents owns, which it could point anywhere.
func (a *agentUser) noAgentLinks(path string) error {
	parent := "/"
	for _, part := range splitPath(path) {
		next := filepath.Join(parent, part)
		fi, err := os.Lstat(next)
		if err != nil {
			return nil
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			if pi, err := os.Lstat(parent); err != nil || owner(pi) == a.uid {
				return errors.New("sandbox: " + next + " is a link in a folder of the agents; deck does not follow it")
			}
		}
		parent = next
	}
	return nil
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
