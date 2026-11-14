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
	"path"
	"path/filepath"
	"slices"
	"strings"
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
	// Keep is where the files of scripts and the caches of their programs stay between runs: Home on deck, the
	// node's own folder on a node, out of the copies of deck's folders it runs in.
	Keep home.Layout
	// Image is the container image deck runs from: a program not in it comes only with a newer image.
	Image string
	// Now is deck's clock, which stamps the file of a script at each run; none on a node.
	Now func() time.Time
}

func New(sb *sandbox.Sandbox, h home.Layout) *Exec { return &Exec{Sandbox: sb, Home: h, Keep: h} }

// Job is one run of a script; Dir is its run folder, with work/ and results/ inside.
type Job struct {
	Project int64
	Script  *manifest.Script
	Source  string
	// Draft says the run is a trial, whose file stays apart from that of real runs.
	Draft bool
	Dir   string
	Stdin []byte
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
	tmp := filepath.Join(j.Work(), "tmp")
	data, cache := x.Home.Data(j.Project), x.Keep.Cache(j.Project)
	for _, d := range []string{tmp, j.Results(), data, cache} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, err
		}
	}
	file, err := x.place(j)
	if err != nil {
		return nil, err
	}
	argv, err := argvOf(j.Script, file)
	if err != nil {
		return nil, err
	}
	b := j.Box
	b.Argv, b.Dir, b.Home = argv, j.Work(), j.Work()
	b.Write = append(slices.Clone(b.Write), j.Work(), j.Results(), data)
	if !home.Within(data, cache) {
		b.Write = append(b.Write, cache)
	}
	b.Read = append(append(slices.Clone(b.Read), filepath.Dir(file)), j.Read...)
	b.Env, b.Secrets = environ(j, data, cache), append(slices.Clone(b.Secrets), j.Token)
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
		if f := otherPythonOf(j.Script.Name, tail, x.Image != ""); f != nil {
			return nil, f
		}
		return nil, fault.With(kind, fmt.Sprintf("script %s exited with code %d: %s", j.Script.Name, code, lastLine(last))+hints, tail)
	}
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// place is the file of a script in its folder of the project, written whole only when it changed: uv keeps the
// environment of a script by its path, so every run of the same file shares one.
func (x *Exec) place(j Job) (string, error) {
	dir := x.Keep.Scripts(j.Project)
	if j.Draft {
		dir = x.Keep.Drafts(j.Project)
	}
	rel := path.Clean(j.Script.File)
	if !filepath.IsLocal(filepath.FromSlash(rel)) {
		return "", fault.New(fault.ScriptFailed, fmt.Sprintf("script %s: file %q is outside the folder of scripts", j.Script.Name, j.Script.File))
	}
	file := filepath.Join(dir, filepath.FromSlash(rel))
	if old, err := home.ReadIn(dir, filepath.FromSlash(rel), int64(len(j.Source))+1); err != nil || string(old) != j.Source {
		t, err := home.Create(filepath.Dir(file))
		if err != nil {
			return "", err
		}
		defer t.Drop()
		if _, err := t.WriteString(j.Source); err != nil {
			return "", err
		}
		if err := t.Chmod(0o700); err != nil {
			return "", err
		}
		if err := t.Keep(file); err != nil {
			return "", err
		}
	}
	if x.Now == nil {
		return file, nil
	}
	// The time of the file is when a script of the module last ran: the cleanup keeps uv's cache by it.
	return file, os.Chtimes(file, x.Now(), x.Now())
}

// environ is the environment of a script over that of its box: its folders, caches of its project, the one python
// of uv, and deck at its door.
func environ(j Job, data, cache string) []string {
	env := []string{"PATH=" + sandbox.Path(), "TERM=dumb", "DECK_RESULTS=" + j.Results(), "DECK_DATA=" + data,
		"XDG_CACHE_HOME=" + cache, "npm_config_cache=" + filepath.Join(cache, "npm"), "UV_CACHE_DIR=" + filepath.Join(cache, "uv"),
		"UV_PYTHON_DOWNLOADS=never", "PYTHONDONTWRITEBYTECODE=1", "PYTHONUNBUFFERED=1"}
	if r := j.Script.Runtime; r == "python" || r == "exec" {
		if py, err := Python(); err == nil {
			env = append(env, "UV_PYTHON="+py)
		}
	}
	if j.Box.Deck == "" {
		return env
	}
	return append(env, "DECK_URL={door}", "DECK_TOKEN="+j.Token, "DECK_MODELS={door}/models")
}

// argvOf is how a script starts: one of runtime python by uv on the one python of deck, so its PEP 723 block counts.
func argvOf(s *manifest.Script, file string) ([]string, error) {
	argv := []string{file}
	switch s.Runtime {
	case "python":
		argv = []string{"uv", "run", "--script", file}
	case "node", "bash":
		argv = []string{s.Runtime, file}
	}
	if !filepath.IsAbs(argv[0]) {
		p, err := sandbox.Find(argv[0])
		if err != nil {
			return nil, fault.New(fault.ProgramMissing, fmt.Sprintf("program %q of script %s is not on this machine", argv[0], s.Name))
		}
		argv[0] = p
	}
	return argv, nil
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
