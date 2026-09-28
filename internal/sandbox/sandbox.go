// Package sandbox confines scripts with the mechanism of the operating system behind one interface.
package sandbox

import (
	"context"
	"net"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Policy is what a confined program may touch: everything is readable except Hide, Read reopens paths
// inside Hide, only Write is writable.
type Policy struct {
	Argv  []string
	Dir   string
	Env   []string
	Write []string
	Read  []string
	Hide  []string
	// Out is the listener of deck the program reaches; without it the program has no network.
	Out net.Listener
	// Net opens the whole network; only MCP servers of a project get it.
	Net bool
	// Port is a local port the program listens on itself; its own network namespace needs no rule for it.
	Port int
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

// real resolves symlinks, so rules match the paths the kernel sees; a missing path stays as given.
func real(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	return abs
}

// Private are the owner's credential stores under home that no confined program reads.
func Private(home string) []string {
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
