//go:build darwin

package sandbox

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Policy is what sandbox-exec lets a program touch, the box of deck and of a node on macOS: all is readable but Hide,
// Read reopens paths in Hide, Private closes paths in Read again; only Write is writable. Read and Write are real paths.
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

func (s *Sandbox) Name() string { return platform().Name() }

func (s *Sandbox) check() error { return platform().Check() }

func (s *Sandbox) listen(dir string) (net.Listener, string, error) {
	ln, err := platform().Listen(dir)
	return ln, "", err
}

func (s *Sandbox) addr(ln net.Listener) string { return platform().Addr(ln) }

// outside is the CLI of an agent, which runs outside sandbox-exec as it did, in the owner's home and network.
func outside(b Box) bool { return b.Kind == KindCLI }

// command is the box as sandbox-exec has run programs: deck's folder and the owner's home closed but for the
// runtimes on PATH and the folder of the program; deck's browsers and the CLIs of agents run outside it, as they did.
func (s *Sandbox) command(ctx context.Context, b Box, door *Door, env []string) (*exec.Cmd, error) {
	argv := marked(b)
	if b.Kind == KindBrowser || outside(b) {
		own := []string{"HOME", "TMPDIR", "XDG_"}
		if outside(b) {
			own = append(own, "NODE_", "CODEX_HOME")
		}
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir, cmd.Env = b.Dir, merged(append(Kept(own...), env...))
		return group(cmd), nil
	}
	env = merged(append(base(b), env...))
	path := ""
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path = v
		}
	}
	c := Close(s.home, path+string(os.PathListSeparator)+filepath.Dir(b.Argv[0]))
	read, err := follow(b.Read)
	if err != nil {
		return nil, err
	}
	write, err := follow(b.Write)
	if err != nil {
		return nil, err
	}
	for _, r := range c.Read {
		read = append(read, real(r))
	}
	return platform().Command(ctx, Policy{Argv: argv, Dir: b.Dir, Env: env, Write: write, Read: read, Hide: c.Hide,
		Private: c.Private, Door: door})
}

// follow are the real paths of the folders of a box, through the links of the system only: sandbox-exec checks the
// path a program reaches, so a folder turned into a link later leads where no rule opens.
func follow(paths []string) ([]string, error) {
	var out []string
	for _, p := range paths {
		at, rest, links := "/", parts(p), 0
		for len(rest) > 0 {
			next := filepath.Join(at, rest[0])
			rest = rest[1:]
			fi, err := os.Lstat(next)
			if err != nil {
				// What is not there yet is opened by the path it will have.
				at = filepath.Join(append([]string{next}, rest...)...)
				break
			}
			if fi.Mode()&os.ModeSymlink == 0 {
				at = next
				continue
			}
			st, _ := fi.Sys().(*syscall.Stat_t)
			var dir syscall.Stat_t
			if links++; st == nil || links > 40 || syscall.Lstat(at, &dir) != nil || !rootLink(st.Uid, dir.Uid, uint32(dir.Mode)) {
				return nil, &Moved{Path: p, Link: next}
			}
			to, err := os.Readlink(next)
			if err != nil {
				return nil, err
			}
			if !filepath.IsAbs(to) {
				to = filepath.Join(at, to)
			}
			at, rest = "/", append(parts(to), rest...)
		}
		out = append(out, at)
	}
	return out, nil
}
