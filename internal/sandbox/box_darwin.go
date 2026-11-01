//go:build darwin

package sandbox

import (
	"context"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Policy is what sandbox-exec lets a program touch, frozen until deck on macOS moves into a Linux container: all is
// readable but Hide, Read reopens paths in Hide, Private closes paths in Read again; only Write is writable.
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

// command is the box as sandbox-exec has run programs: deck's folder and the owner's home closed but for the
// runtimes on PATH and the folder of the program; deck's browsers run outside it, as they did.
func (s *Sandbox) command(ctx context.Context, b Box, door *Door, env []string) (*exec.Cmd, error) {
	argv := marked(b)
	if b.Kind == KindBrowser {
		cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
		cmd.Dir, cmd.Env = b.Dir, merged(append(Kept("HOME", "TMPDIR", "XDG_"), env...))
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
	return platform().Command(ctx, Policy{Argv: argv, Dir: b.Dir, Env: env, Write: b.Write,
		Read: append(slices.Clone(b.Read), c.Read...), Hide: c.Hide, Private: c.Private, Door: door})
}
