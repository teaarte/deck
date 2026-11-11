// Package script runs project scripts in the sandbox.
package script

import (
	"bytes"
	"context"
	"deck2/internal/home"
	"deck2/internal/untrusted"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/fault"
	"deck2/internal/manifest"
	"deck2/internal/sandbox"
)

const maxStdout = 32 << 20

// Exec runs scripts of projects whose state lives under Home.
type Exec struct {
	Sandbox *sandbox.Sandbox
	Home    home.Layout
	// Image is the container image deck runs from: a program not in it comes only with a newer image.
	Image string
}

func New(sb *sandbox.Sandbox, h home.Layout) *Exec { return &Exec{Sandbox: sb, Home: h} }

// Job is one run of a script; Dir is its run folder, with work/ and results/ inside.
type Job struct {
	Project int64
	Script  *manifest.Script
	Source  string
	Dir     string
	Stdin   []byte
	// Box is the box of the script by its rights, with its mark and deck's side of its door; Run adds the program
	// and its folders.
	Box   sandbox.Box
	Token string
	// Read are the files of its input the script reads besides the folders of its box.
	Read    []string
	OnLine  func(line string)
	OnStart func(pgid int)
}

func (j Job) Work() string    { return filepath.Join(j.Dir, "work") }
func (j Job) Results() string { return filepath.Join(j.Dir, "results") }

// Run runs the script in its box and returns its stdout; a non-zero exit is an error with the end of stderr.
func (x *Exec) Run(ctx context.Context, j Job) ([]byte, error) {
	if err := x.check(*j.Script); err != nil {
		return nil, err
	}
	src := filepath.Join(j.Dir, "script")
	tmp := filepath.Join(j.Work(), "tmp")
	data := x.Home.Data(j.Project)
	for _, d := range []string{src, tmp, j.Results(), data} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	file := filepath.Join(src, filepath.Base(j.Script.File))
	if err := os.WriteFile(file, []byte(j.Source), 0o700); err != nil {
		return nil, err
	}
	argv, err := argvOf(j.Script, file)
	if err != nil {
		return nil, err
	}
	b := j.Box
	b.Argv, b.Dir, b.Home = argv, j.Work(), j.Work()
	b.Write = append(slices.Clone(b.Write), j.Work(), j.Results(), data)
	b.Read = append(append(slices.Clone(b.Read), src), j.Read...)
	b.Env, b.Secrets = environ(j, data), append(slices.Clone(b.Secrets), j.Token)
	var out bytes.Buffer
	var last []string
	p, err := x.Sandbox.Run(ctx, b, sandbox.IO{Stdin: bytes.NewReader(j.Stdin), Stdout: &limited{w: &out, n: maxStdout},
		Stderr: func(line string) {
			if j.OnLine != nil {
				j.OnLine(line)
			}
			if last = append(last, line); len(last) > 20 {
				last = last[1:]
			}
		}})
	if err != nil {
		return nil, err
	}
	if j.OnStart != nil {
		j.OnStart(p.Pid())
	}
	err = p.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if code, exited := sandbox.ExitCode(err); exited {
		tail := strings.Join(last, "\n")
		kind, hints := fault.ScriptFailed, ""
		if sandbox.Refused(tail) {
			kind = fault.RightsDenied
			hints += "\ndeck: " + sandbox.OpenHint
		}
		for _, why := range p.Refused() {
			kind = fault.RightsDenied
			hints += "\ndeck: the proxy refused: " + why
		}
		if f := Reported(out.Bytes()); f != nil {
			f.Text = "script " + j.Script.Name + ": " + f.Text + hints
			f.Details = strings.TrimSpace(f.Details + "\n" + tail)
			return nil, f
		}
		return nil, fault.With(kind, fmt.Sprintf("script %s exited with code %d: %s", j.Script.Name, code, lastLine(last))+hints, tail)
	}
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// environ is the environment of a script over that of its box: its folders, caches in the data of its project, and
// deck at its door.
func environ(j Job, data string) []string {
	env := append(pathFirst([]string{"PATH=" + sandbox.Path()}), "TERM=dumb", "DECK_RESULTS="+j.Results(), "DECK_DATA="+data,
		"XDG_CACHE_HOME="+filepath.Join(data, ".cache"), "npm_config_cache="+filepath.Join(data, ".cache", "npm"),
		"UV_CACHE_DIR="+filepath.Join(data, ".cache", "uv"), "PYTHONDONTWRITEBYTECODE=1", "PYTHONUNBUFFERED=1")
	if j.Box.Deck == "" {
		return env
	}
	return append(env, "DECK_URL={door}", "DECK_TOKEN="+j.Token, "DECK_MODELS={door}/models")
}

func argvOf(s *manifest.Script, file string) ([]string, error) {
	argv := []string{file}
	if p := interpreters[s.Runtime]; p != "" {
		argv = []string{p, file}
	}
	if py, err := python(); err == nil && s.Runtime == "python" {
		argv[0] = py
	}
	if !filepath.IsAbs(argv[0]) {
		p, err := sandbox.Find(argv[0])
		if err != nil {
			return nil, fault.New(fault.ProgramMissing, fmt.Sprintf("program %q of script %s is not on this machine", argv[0], s.Name))
		}
		argv[0] = p
	}
	if s.Runtime == "python" {
		argv[0] = realPython(argv[0])
	}
	return argv, nil
}

var pythons sync.Map

// realPython is the interpreter behind a launcher such as macOS /usr/bin/python3, which in a sandbox runs xcrun
// and writes cache errors to stderr on every start; the launcher itself when it cannot tell.
func realPython(path string) string {
	if got, ok := pythons.Load(path); ok {
		return got.(string)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-c", "import sys; print(sys.executable)").Output()
	real := strings.TrimSpace(string(out))
	if fi, statErr := os.Stat(real); err != nil || statErr != nil || !filepath.IsAbs(real) || fi.IsDir() {
		real = path
	}
	pythons.Store(path, real)
	return real
}

type limited struct {
	w io.Writer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if len(p) > l.n {
		return 0, errors.New("script output is larger than 32 MB")
	}
	l.n -= len(p)
	return l.w.Write(p)
}

// Reported is the failure a script printed to stdout as it exited non-zero: {"error": text, "type", "details"}.
func Reported(out []byte) *fault.F {
	var v struct {
		Error   string          `json:"error"`
		Type    string          `json:"type"`
		Details json.RawMessage `json:"details"`
	}
	if json.Unmarshal(bytes.TrimSpace(out), &v) != nil || strings.TrimSpace(v.Error) == "" {
		return nil
	}
	f := fault.New(fault.FromScript(v.Type), untrusted.Flat(v.Error, 300))
	switch d := bytes.TrimSpace(v.Details); {
	case len(d) == 0 || string(d) == "null":
	case d[0] == '"':
		_ = json.Unmarshal(d, &f.Details)
	default:
		f.Details = string(d)
	}
	return f
}

// lastLine is the last line of stderr that says something, most often the error itself.
func lastLine(lines []string) string {
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return untrusted.Flat(l, 200)
		}
	}
	return "no output on stderr"
}
