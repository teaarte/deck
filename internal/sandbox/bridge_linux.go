//go:build linux

package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"deck2/internal/paths"
)

// bridgeArg makes the deck binary, started inside the sandbox, carry a local port to deck's socket.
const bridgeArg = "__deck_sandbox_bridge"

// bridgeAddr is where a confined program reaches deck inside its own network namespace.
const bridgeAddr = "127.0.0.1:7777"

func init() {
	if len(os.Args) > 1 && os.Args[1] == bridgeArg {
		os.Exit(bridge(os.Args[2:]))
	}
}

// shared are folders with sockets of other programs (D-Bus, ssh-agent, docker, X11) that a read-only view
// still lets a program connect to; they are replaced by empty ones.
var shared = []string{"/tmp", "/run", "/var/run", "/var/tmp", "/dev/shm"}

type bwrap struct {
	once sync.Once
	err  error
}

func platform() Sandbox { return &bwrap{} }

func (*bwrap) Name() string { return "bubblewrap" }

func (b *bwrap) Check() error {
	b.once.Do(func() {
		out, err := exec.Command("bwrap", "--unshare-all", "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc", "true").CombinedOutput()
		if err != nil {
			b.err = errors.New("bwrap does not work here: " + strings.TrimSpace(string(out)+" "+err.Error()))
		}
	})
	return b.err
}

// Listen opens deck's socket in the run folder dir; its names are short, since a socket path holds 108 bytes.
func (*bwrap) Listen(dir string) (net.Listener, error) {
	dir = filepath.Join(dir, "n")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	sock := filepath.Join(dir, "s")
	if len(sock) > 107 {
		return nil, fmt.Errorf("sandbox: the socket %s is longer than the 107 bytes Linux takes; set TMPDIR to a shorter folder", sock)
	}
	return net.Listen("unix", sock)
}

func (*bwrap) Addr(net.Listener) string { return bridgeAddr }

func (*bwrap) Command(ctx context.Context, p Policy) (*exec.Cmd, error) {
	args := []string{"--die-with-parent", "--new-session", "--unshare-all"}
	args = append(args, "--ro-bind", "/", "/", "--dev", "/dev", "--proc", "/proc")
	for _, d := range shared {
		if fi, err := os.Lstat(d); err == nil && fi.IsDir() {
			args = append(args, "--tmpfs", d)
		}
	}
	for _, d := range rebuilt(p.Hide) {
		args = append(args, "--tmpfs", real(d))
		entries, _ := os.ReadDir(d)
		for _, e := range entries {
			src, dst := filepath.Join(d, e.Name()), filepath.Join(real(d), e.Name())
			if e.Type()&os.ModeSymlink != 0 {
				if to, err := os.Readlink(src); err == nil {
					args = append(args, "--symlink", to, dst)
				}
				continue
			}
			args = append(args, "--ro-bind-try", real(src), dst)
		}
	}
	args = append(args, closed(p.Hide)...)
	made := map[string]bool{}
	for _, r := range p.Read {
		args = append(args, "--ro-bind-try", real(r), real(r))
		args = append(args, links(r, p.Hide, made)...)
	}
	var private []string
	for _, h := range p.Private {
		if slices.ContainsFunc(p.Read, func(r string) bool { return paths.Inside(real(r), real(h)) }) {
			private = append(private, h)
		}
	}
	args = append(args, closed(private)...)
	for _, w := range p.Write {
		args = append(args, "--bind", real(w), real(w))
	}
	if filepath.IsAbs(p.Argv[0]) {
		args = append(args, "--ro-bind-try", real(p.Argv[0]), real(p.Argv[0]))
	}
	argv := p.Argv
	if p.Door != nil {
		// In deck's image deck itself has capabilities and only it may run it; DECK_BRIDGE is a copy without them.
		self, err := os.Executable()
		if b := os.Getenv("DECK_BRIDGE"); b != "" {
			self, err = b, nil
		}
		if err != nil {
			return nil, err
		}
		sock := real(p.Door.ln.Addr().String())
		args = append(args, "--bind", filepath.Dir(sock), filepath.Dir(sock), "--ro-bind", real(self), real(self))
		argv = append([]string{real(self), bridgeArg, sock, bridgeAddr, "--"}, argv...)
	}
	args = append(args, "--chdir", real(p.Dir), "--clearenv")
	for _, kv := range p.Env {
		k, v, _ := strings.Cut(kv, "=")
		args = append(args, "--setenv", k, v)
	}
	args = append(args, "--")
	cmd := exec.CommandContext(ctx, "bwrap", append(args, argv...)...)
	return group(cmd), nil
}

// links recreate the links along path inside the hidden folders, so a program reaches it by the name it was given.
func links(path string, hide []string, made map[string]bool) []string {
	var args []string
	p := "/"
	for _, part := range strings.Split(filepath.Clean(path), "/")[1:] {
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if err != nil {
			break
		}
		inside := slices.ContainsFunc(hide, func(h string) bool { return paths.Inside(real(h), p) || paths.Inside(h, p) })
		if fi.Mode()&os.ModeSymlink != 0 && inside && !made[p] {
			if to, err := os.Readlink(p); err == nil {
				made[p] = true
				args = append(args, "--symlink", to, p)
			}
		}
	}
	return args
}

// closed are the arguments that empty folders and files that exist.
func closed(paths []string) []string {
	var args []string
	for _, h := range paths {
		switch fi, err := os.Stat(h); {
		case err != nil:
		case fi.IsDir():
			args = append(args, "--tmpfs", real(h))
		default:
			args = append(args, "--ro-bind", "/dev/null", real(h))
		}
	}
	return args
}

// rebuilt are the nearest folders above hidden paths that do not exist yet, shallowest first: each is shown
// as it was at the start, so a credential store made later in it does not appear to a long-lived program.
func rebuilt(hide []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, h := range hide {
		if _, err := os.Lstat(h); err == nil {
			continue
		}
		d := filepath.Dir(h)
		for _, err := os.Lstat(d); err != nil && d != "/"; _, err = os.Lstat(d) {
			d = filepath.Dir(d)
		}
		if d != "/" && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	slices.SortFunc(out, func(a, b string) int { return strings.Count(a, "/") - strings.Count(b, "/") })
	return out
}

// bridge listens on a local address, carries each connection to a unix socket and runs the program.
func bridge(args []string) int {
	if len(args) < 4 || args[2] != "--" {
		return 2
	}
	sock, addr, argv := args[0], args[1], args[3:]
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		os.Stderr.WriteString("deck sandbox: " + err.Error() + "\n")
		return 2
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go carry(c, sock)
		}
	}()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return exit.ExitCode()
		}
		os.Stderr.WriteString("deck sandbox: " + err.Error() + "\n")
		return 127
	}
	return 0
}

func carry(c net.Conn, sock string) {
	defer c.Close()
	up, err := net.Dial("unix", sock)
	if err != nil {
		return
	}
	defer up.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	<-done
}
