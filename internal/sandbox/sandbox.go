// Package sandbox confines scripts with the mechanism of the operating system behind one interface.
package sandbox

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"
	"time"

	"deck2/internal/home"
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
	// Door is the way out of the program; without it the program has no network.
	Door *Door
}

type Sandbox interface {
	Name() string
	// Check reports whether the mechanism works on this machine.
	Check() error
	// Listen opens in dir the listener of a door.
	Listen(dir string) (net.Listener, error)
	// Addr is the host:port the confined program dials to reach ln.
	Addr(ln net.Listener) string
	// Command builds the confined command; it runs in its own process group, killed as a whole on cancel.
	Command(ctx context.Context, p Policy) (*exec.Cmd, error)
}

// New returns the sandbox of this operating system, or deck's container when deck runs agents as a user of their own
// there without its own sandbox (DECK_SANDBOX=bwrap keeps it).
func New() Sandbox {
	if Contained() {
		return container{}
	}
	return platform()
}

// End kills what is left of the process group of a command.
func End(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = Kill(cmd, syscall.SIGKILL)
	}
}

// Kill sends sig to the process group of a started command.
func Kill(cmd *exec.Cmd, sig syscall.Signal) error { return syscall.Kill(-cmd.Process.Pid, sig) }

func group(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return Kill(cmd, syscall.SIGKILL) }
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

// Confine builds the command of a program in the sandbox as deck confines every one: the sandbox checked, deck's
// folder and the owner's home closed but for the runtimes on path and the folder of bin, the door's environment last.
func Confine(ctx context.Context, sb Sandbox, layout home.Layout, path, bin string, p Policy) (*exec.Cmd, error) {
	if err := sb.Check(); err != nil {
		return nil, fmt.Errorf("sandbox %s: %w", sb.Name(), err)
	}
	c := Close(layout, path+string(os.PathListSeparator)+filepath.Dir(bin))
	p.Hide, p.Read, p.Private = c.Hide, append(slices.Clone(p.Read), c.Read...), c.Private
	if p.Door != nil {
		p.Env = append(slices.Clone(p.Env), p.Door.Env()...)
	}
	cmd, err := sb.Command(ctx, p)
	if err != nil {
		return nil, err
	}
	write := slices.Clone(p.Write)
	if p.Door != nil {
		write = append(write, p.Door.dir)
	}
	if err := AsAgent(cmd, write, p.Read); err != nil {
		return nil, err
	}
	return cmd, nil
}
