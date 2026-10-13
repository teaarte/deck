// Package script runs project scripts in the sandbox.
package script

import (
	"bytes"
	"context"
	"deck2/internal/home"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/sandbox"
)

const maxStdout = 32 << 20

// Exec runs scripts of projects whose state lives under Home.
type Exec struct {
	Sandbox sandbox.Sandbox
	Home    home.Layout
}

func New(sb sandbox.Sandbox, h home.Layout) *Exec { return &Exec{Sandbox: sb, Home: h} }

// DataDir is the lasting folder of a project that its scripts and agents write indexes, caches and state to.
func (x *Exec) DataDir(project int64) string {
	return x.Home.Data(project)
}

// Job is one run of a script; Dir is its run folder, with work/ and results/ inside.
type Job struct {
	Project int64
	Script  *manifest.Script
	Source  string
	Dir     string
	Stdin   []byte
	// Door is the script's way to deck, opened by Token, and to the web; without it the script has no network.
	Door  *sandbox.Door
	Token string
	// Read are files and folders the script may read, such as files of its input and the folders of its project.
	Read    []string
	OnLine  func(line string)
	OnStart func(pgid int)
}

func (j Job) Work() string    { return filepath.Join(j.Dir, "work") }
func (j Job) Results() string { return filepath.Join(j.Dir, "results") }

// Run runs the script confined and returns its stdout; a non-zero exit is an error with the end of stderr.
func (x *Exec) Run(ctx context.Context, j Job) ([]byte, error) {
	if err := x.Sandbox.Check(); err != nil {
		return nil, fmt.Errorf("sandbox %s: %w", x.Sandbox.Name(), err)
	}
	if err := check(*j.Script); err != nil {
		return nil, err
	}
	src := filepath.Join(j.Dir, "script")
	tmp := filepath.Join(j.Work(), "tmp")
	data := x.DataDir(j.Project)
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
	c := sandbox.Close(x.Home, os.Getenv("PATH")+string(os.PathListSeparator)+filepath.Dir(argv[0]))
	cmd, err := x.Sandbox.Command(ctx, sandbox.Policy{Argv: argv, Dir: j.Work(), Env: environ(j, data, tmp),
		Write: []string{j.Work(), j.Results(), data}, Read: append(append([]string{src}, j.Read...), c.Read...),
		Hide: c.Hide, Private: c.Private, Door: j.Door})
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	stderr := &lineWriter{f: j.OnLine}
	cmd.Stdin = bytes.NewReader(j.Stdin)
	started, drain, err := pipes(cmd, &limited{w: &out, n: maxStdout}, stderr)
	if err != nil {
		return nil, err
	}
	err = cmd.Start()
	started()
	if err != nil {
		drain()
		return nil, err
	}
	if j.OnStart != nil {
		j.OnStart(cmd.Process.Pid)
	}
	err = cmd.Wait()
	sandbox.End(cmd)
	drain()
	stderr.flush()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		tail := strings.Join(stderr.last, "\n")
		if sandbox.Refused(tail) {
			tail += "\ndeck: " + sandbox.OpenHint
		}
		if j.Door != nil {
			for _, why := range j.Door.Refused() {
				tail += "\ndeck: the proxy refused: " + why
			}
		}
		return nil, fmt.Errorf("script %s exited with code %d: %s", j.Script.Name, exit.ExitCode(), tail)
	}
	if err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// environ is the whole environment of a script: the confined one, its folders, caches in the data of its project,
// and deck with its door last.
func environ(j Job, data, tmp string) []string {
	env := append(sandbox.Env(j.Work(), tmp), "TERM=dumb",
		"DECK_RESULTS="+j.Results(), "DECK_DATA="+data, "XDG_CACHE_HOME="+filepath.Join(data, ".cache"),
		"npm_config_cache="+filepath.Join(data, ".cache", "npm"), "PYTHONDONTWRITEBYTECODE=1", "PYTHONUNBUFFERED=1")
	if j.Door == nil {
		return env
	}
	url := j.Door.URL()
	return append(append(env, "DECK_URL="+url, "DECK_TOKEN="+j.Token, "DECK_MODELS="+url+"/models"), j.Door.Env()...)
}

func argvOf(s *manifest.Script, file string) ([]string, error) {
	argv := []string{file}
	if p := interpreters[s.Runtime]; p != "" {
		argv = []string{p, file}
	}
	if !filepath.IsAbs(argv[0]) {
		p, err := exec.LookPath(argv[0])
		if err != nil {
			return nil, fmt.Errorf("program %q of script %s is not on this machine", argv[0], s.Name)
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

// pipes gives the command its own stdout and stderr, so Wait returns when it exits and its group dies at once,
// unlike WaitDelay, which waits for programs it started; started closes deck's ends, drain takes what comes in 2 s.
func pipes(cmd *exec.Cmd, stdout, stderr io.Writer) (started, drain func(), err error) {
	var writers []*os.File
	var gates []*gate
	var done []chan struct{}
	for i, w := range []io.Writer{stdout, stderr} {
		r, pw, err := os.Pipe()
		if err != nil {
			return nil, nil, err
		}
		if i == 0 {
			cmd.Stdout = pw
		} else {
			cmd.Stderr = pw
		}
		g := &gate{w: w}
		ch := make(chan struct{})
		go func() { _, _ = io.Copy(g, r); _ = r.Close(); close(ch) }()
		writers, gates, done = append(writers, pw), append(gates, g), append(done, ch)
	}
	started = func() {
		for _, w := range writers {
			_ = w.Close()
		}
	}
	drain = func() {
		started()
		deadline := time.Now().Add(2 * time.Second)
		for i, ch := range done {
			select {
			case <-ch:
			case <-time.After(time.Until(deadline)):
			}
			gates[i].close()
		}
	}
	return started, drain, nil
}

// gate passes writes until it is closed, then drops them.
type gate struct {
	mu     sync.Mutex
	w      io.Writer
	closed bool
}

func (g *gate) Write(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.closed {
		return len(p), nil
	}
	return g.w.Write(p)
}

func (g *gate) close() {
	g.mu.Lock()
	g.closed = true
	g.mu.Unlock()
}

// lineWriter passes stderr lines to f and keeps the last of them.
type lineWriter struct {
	f    func(string)
	buf  []byte
	last []string
}

func (l *lineWriter) Write(p []byte) (int, error) {
	l.buf = append(l.buf, p...)
	for {
		i := bytes.IndexByte(l.buf, '\n')
		if i < 0 {
			break
		}
		l.line(string(l.buf[:i]))
		l.buf = l.buf[i+1:]
	}
	if len(l.buf) > 64<<10 {
		l.line(string(l.buf))
		l.buf = nil
	}
	return len(p), nil
}

func (l *lineWriter) flush() {
	if len(l.buf) > 0 {
		l.line(string(l.buf))
		l.buf = nil
	}
}

func (l *lineWriter) line(s string) {
	if l.f != nil {
		l.f(s)
	}
	if l.last = append(l.last, s); len(l.last) > 20 {
		l.last = l.last[1:]
	}
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
