package sandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"deck2/internal/fault"
	"deck2/internal/home"
)

// The kinds of boxes.
const (
	KindScript  = "script"
	KindMCP     = "mcp"
	KindAgent   = "agent"
	KindBrowser = "browser"
	// KindCLI is the CLI of an agent with a home of its own: Claude Code, Codex.
	KindCLI = "cli"
)

// Box is what a foreign program sees and reaches. It is data, so a node can run the same box: deck's side of its
// door is a token, not an address.
type Box struct {
	Kind string `json:"kind"`
	// Mark is in the command line of the box, so a deck that starts again finds what a deck before it left running.
	Mark string   `json:"mark,omitempty"`
	Argv []string `json:"argv"`
	Dir  string   `json:"dir"`
	// Home is HOME of the program, its temp folder and caches; the box writes it.
	Home string `json:"home"`
	// Env is the program's own environment over deck's few variables; {door}, {door.addr} and {door.key} in Argv,
	// Env and Files are the door's URL of deck, its address and its key.
	Env []string `json:"env,omitempty"`
	// Files are written before the program starts, at their paths in Home or at the absolute paths deck chose.
	Files map[string]string `json:"files,omitempty"`
	// Read are the paths the box reads besides the system and the programs on PATH, Write those it writes.
	Read  []string `json:"read,omitempty"`
	Write []string `json:"write,omitempty"`
	// Shared are files and folders of the machine the box writes at other paths, by the path in the box: the login
	// of a CLI the homes of it share, a home where the CLI finds it. One the machine has not yet is not there.
	Shared map[string]string `json:"shared,omitempty"`
	// Hosts are what the door lets the box reach; nil is no network, "*" the public internet.
	Hosts []string `json:"hosts,omitempty"`
	// Deck is the token of deck's side of the door.
	Deck string `json:"deck,omitempty"`
	// GPU opens the graphics card: /dev/dri on Linux.
	GPU bool `json:"gpu,omitempty"`
	// Secrets are values the program is given that deck masks in what it writes, as it masks the key of the door.
	Secrets []string `json:"secrets,omitempty"`
	// HostNet keeps the network of the machine: deck's browser of shots, which opens deck's own pages.
	HostNet bool `json:"host_net,omitempty"`
	// Reach hears each host the box reached through its door; without it the side of Deck does, when a Reacher.
	Reach func(host string) `json:"-"`
}

// IO is what the program of a box reads and writes; nil Stdin and Stdout are pipes of the Proc.
type IO struct {
	Stdin  io.Reader
	Stdout io.Writer
	// Stderr gets each line of stderr.
	Stderr func(line string)
}

// Run starts the program of b in its box; without a box that works deck starts no foreign program.
func (s *Sandbox) Run(ctx context.Context, b Box, o IO) (*Proc, error) {
	if err := s.Check(); err != nil {
		return nil, fault.New(fault.ProgramMissing, "deck starts no program of a module without its box: "+err.Error())
	}
	if len(b.Argv) == 0 {
		return nil, fmt.Errorf("sandbox: a box of %s with no program", b.Kind)
	}
	b.Argv = slices.Clone(b.Argv)
	bin, err := Find(b.Argv[0])
	if err != nil {
		return nil, fault.New(fault.ProgramMissing, err.Error())
	}
	b.Argv[0] = bin
	b.Read, b.Write = append(slices.Clone(b.Read), s.Also.Read...), append(append(slices.Clone(b.Write), b.Home, b.Dir), s.Also.Write...)
	for _, d := range append([]string{filepath.Join(b.Home, "tmp")}, b.Write...) {
		if err := os.MkdirAll(d, 0o700); d != "" && err != nil && !errors.Is(err, syscall.ENOTDIR) {
			return nil, err
		}
	}
	var door *Door
	if b.Hosts != nil || b.Deck != "" {
		reach := b.Reach
		if reach == nil {
			reach = s.reacher(b.Deck)
		}
		if door, err = s.open(b.Hosts, s.side(b.Deck), reach); err != nil {
			return nil, err
		}
	}
	fill := strings.NewReplacer().Replace
	if door != nil {
		fill = strings.NewReplacer("{door.addr}", door.self, "{door.key}", door.key, "{door}", door.URL()).Replace
	}
	for i := range b.Argv {
		b.Argv[i] = fill(b.Argv[i])
	}
	env := slices.Clone(b.Env)
	for i := range env {
		env[i] = fill(env[i])
	}
	for name, text := range b.Files {
		if err == nil {
			at := filepath.Join(b.Home, filepath.Clean("/"+name))
			if filepath.IsAbs(name) {
				at = name
			}
			err = home.WriteFile(at, []byte(fill(text)))
		}
	}
	if err != nil {
		door.Close()
		return nil, err
	}
	if door != nil && !outside(b) {
		env = append(env, door.Env()...)
	}
	cmd, err := s.command(ctx, b, door, env)
	if err != nil {
		door.Close()
		return nil, err
	}
	return start(cmd, door, o, secretsOf(b, door))
}

// Leaves says what a box of kind leaves open on this machine: the files of the machine, its network.
func Leaves(kind string) (files, network bool) {
	out := outside(Box{Kind: kind})
	return out, out
}

// Homed says the CLI of an agent runs in a home deck keeps for its project, in a box of its own; else, on macOS, in
// the home of the user.
func Homed() bool { return !outside(Box{Kind: KindCLI}) }

// base is the environment a box starts from: deck's few variables, its PATH, the box's home and temp folder, so
// the caches of npm, uv and pip are the box's too; the box's own and its door's go over it.
func base(b Box) []string {
	return append(Kept(), "HOME="+b.Home, "TMPDIR="+filepath.Join(b.Home, "tmp"))
}

// marked is the program of a box under a shell whose command line carries the mark, where the box is no process of
// its own that carries it.
func marked(b Box) []string {
	if b.Mark == "" {
		return b.Argv
	}
	return append([]string{"/bin/sh", "-c", `"$@"; exit $?`, b.Mark}, b.Argv...)
}

// merged is env with each name once, the last value kept.
func merged(env []string) []string {
	var out []string
	at := map[string]int{}
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if i, ok := at[name]; ok {
			out[i] = kv
			continue
		}
		at[name] = len(out)
		out = append(out, kv)
	}
	return out
}
