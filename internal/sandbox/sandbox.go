// Package sandbox confines scripts with the mechanism of the operating system behind one interface.
package sandbox

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"
)

// Policy is what a confined program may touch: everything is readable except Hide, Read reopens paths
// inside Hide, Private closes paths inside Read again; only Write is writable, and it opens all of them.
type Policy struct {
	Argv    []string
	Dir     string
	Env     []string
	Write   []string
	Read    []string
	Hide    []string
	Private []string
	// Out is the listener of deck the program reaches; without it the program has no network.
	Out net.Listener
	// Net opens the whole network; only MCP servers of a project get it.
	Net bool
	// Port is a local port the program listens on itself; its own network namespace needs no rule for it.
	Port int
	// Tag names the program in what Watch reports.
	Tag string
}

type Sandbox interface {
	Name() string
	// Check reports whether the mechanism works on this machine.
	Check() error
	// Listen opens in dir the listener a confined program reaches deck through.
	Listen(dir string) (net.Listener, error)
	// Addr is the host:port the confined program dials to reach ln.
	Addr(ln net.Listener) string
	// Command builds the confined command; it runs in its own process group, killed as a whole on cancel.
	Command(ctx context.Context, p Policy) (*exec.Cmd, error)
	// Watch passes f each path the sandbox kept closed from a program with tag, until stop.
	Watch(tag string, f func(path string)) (stop func())
}

// New returns the sandbox of this operating system.
func New() Sandbox { return platform() }

// End kills what is left of the process group of a finished command.
func End(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}

func group(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// real resolves symlinks, so rules match the paths the kernel sees; a missing path resolves through its
// nearest existing folder.
func real(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	rest := ""
	for d := abs; ; d = filepath.Dir(d) {
		if r, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Join(r, rest)
		}
		if d == filepath.Dir(d) {
			return abs
		}
		rest = filepath.Join(filepath.Base(d), rest)
	}
}

// Closure is what deck keeps from a confined program: Hide closes deck's folder and the owner's home, Read opens
// the runtimes on PATH inside them again, Private keeps the credential stores closed inside Read.
type Closure struct{ Hide, Read, Private []string }

// Close is the closure of a program with path as its PATH under deck's folder deck; open is the old sandbox, which
// reads the home folder but for the credential stores.
func Close(deck, path string, open bool) Closure {
	home, err := os.UserHomeDir()
	if open || err != nil || real(home) == "/" {
		return Closure{Hide: Hidden(deck)}
	}
	return Closure{Hide: []string{deck, home}, Read: Runtimes(home, path), Private: Private()}
}

// Runtimes are the folders of path inside home and the installations they are the bin of, with those of the
// programs they link to: a node, a python or a JDK keeps its libraries next to its bin.
func Runtimes(home, path string) []string {
	home = real(home)
	seen := map[string]bool{}
	var out []string
	var add func(dir string, links int)
	add = func(dir string, links int) {
		dir = real(dir)
		if !strings.HasPrefix(dir, home+"/") || seen[dir] {
			return
		}
		seen[dir] = true
		root := dir
		if up := filepath.Dir(dir); filepath.Base(dir) == "bin" && up != home && installation(up) {
			root = up
		}
		if !slices.Contains(out, root) {
			out = append(out, root)
		}
		if links == 0 {
			return
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if e.Type()&os.ModeSymlink == 0 {
				continue
			}
			if to, err := filepath.EvalSymlinks(filepath.Join(dir, e.Name())); err == nil {
				add(filepath.Dir(to), links-1)
			}
		}
	}
	for _, d := range filepath.SplitList(path) {
		if filepath.IsAbs(d) {
			add(d, 2)
		}
	}
	return out
}

// installation is a folder that holds what the programs in its bin load.
func installation(dir string) bool {
	for _, n := range []string{"lib", "libexec", "versions"} {
		if fi, err := os.Stat(filepath.Join(dir, n)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// Hidden is what no confined program reads: the state of deck and the owner's credential stores.
func Hidden(state string) []string { return append([]string{state}, Private()...) }

// Private are the owner's credential stores and the data of their other apps.
func Private() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(private))
	for _, p := range private {
		out = append(out, filepath.Join(home, p))
	}
	return out
}

var private = []string{".ssh", ".aws", ".azure", ".gnupg", ".kube", ".docker", ".netrc", ".git-credentials", ".npmrc",
	".pypirc", ".cargo/credentials", ".cargo/credentials.toml", ".terraform.d", ".password-store", ".mozilla",
	".config/gcloud", ".config/gh", ".config/op", ".config/google-chrome", ".config/chromium", ".local/share/keyrings",
	".claude", ".claude.json", ".codex", ".dsh", ".config/opencode", ".local/share/opencode", "Library/Keychains", "Library/Cookies", "Library/Application Support",
	"Library/Containers", "Library/Group Containers", "Library/Mail", "Library/Messages", "Library/Safari"}
