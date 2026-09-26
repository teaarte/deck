package script

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/sandbox"
)

const specFile = ".deck-spec"

// installers build the package environment of a runtime in dir; each step runs in the sandbox with the network.
var installers = map[string]struct {
	tool  string
	steps func(dir string, pkgs []string) [][]string
}{
	"python": {"uv", func(dir string, pkgs []string) [][]string {
		py, _ := exec.LookPath("python3")
		return [][]string{{"uv", "venv", "--relocatable", "--python", py, dir},
			append([]string{"uv", "pip", "install", "--python", filepath.Join(dir, "bin", "python")}, pkgs...)}
	}},
	"node": {"npm", func(dir string, pkgs []string) [][]string {
		return [][]string{append([]string{"npm", "install", "--prefix", dir, "--no-audit", "--no-fund"}, pkgs...)}
	}},
}

var interpreters = map[string]string{"python": "python3", "node": "node", "bash": "bash"}

// project is what Exec keeps per project: a lock that waits with a context, and the scripts running now.
type project struct {
	lock    chan struct{}
	running int
}

func (x *Exec) project(id int64) *project {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.projects[id] == nil {
		x.projects[id] = &project{lock: make(chan struct{}, 1)}
	}
	return x.projects[id]
}

func (p *project) acquire(ctx context.Context) error {
	select {
	case p.lock <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *project) release() { <-p.lock }

// Ensure checks the programs the scripts of a manifest need and installs their packages into the project
// environment when the declared packages changed; the result maps a script name to its problem.
func (x *Exec) Ensure(ctx context.Context, projectID int64, m *manifest.Manifest) map[string]error {
	p := x.project(projectID)
	if err := p.acquire(ctx); err != nil {
		return everyScript(m, err)
	}
	defer p.release()
	return x.ensure(ctx, projectID, p, m)
}

func everyScript(m *manifest.Manifest, err error) map[string]error {
	out := map[string]error{}
	for _, s := range m.Scripts {
		out[s.Name] = err
	}
	return out
}

// ensure runs with the project lock held; old environments go only while no script of the project runs.
func (x *Exec) ensure(ctx context.Context, projectID int64, p *project, m *manifest.Manifest) map[string]error {
	problems := map[string]error{}
	for _, s := range m.Scripts {
		for _, prog := range needs(s) {
			if _, err := exec.LookPath(prog); err != nil {
				problems[s.Name] = fmt.Errorf("program %q that script %s needs is not on this machine", prog, s.Name)
				break
			}
		}
	}
	keep := map[string]bool{}
	for runtime := range installers {
		pkgs, users := packages(m, runtime)
		if len(pkgs) == 0 {
			continue
		}
		if _, err := exec.LookPath(installers[runtime].tool); err != nil {
			continue
		}
		dir := x.envPath(projectID, runtime, pkgs)
		keep[filepath.Base(dir)] = true
		if err := x.install(ctx, projectID, dir, runtime, pkgs); err != nil {
			for _, u := range users {
				problems[u] = fmt.Errorf("installing packages of script %s: %w", u, err)
			}
		}
	}
	x.mu.Lock()
	idle := p.running == 0
	x.mu.Unlock()
	if idle {
		old, _ := filepath.Glob(filepath.Join(x.envDir(projectID), "*-*"))
		for _, d := range old {
			if !keep[filepath.Base(d)] {
				_ = os.RemoveAll(d)
			}
		}
	}
	return problems
}

// packages is the sorted union of the packages the scripts of a runtime declare, and those scripts.
func packages(m *manifest.Manifest, runtime string) ([]string, []string) {
	var pkgs, users []string
	for _, s := range m.Scripts {
		if s.Runtime == runtime && len(s.Packages) > 0 {
			pkgs, users = append(pkgs, s.Packages...), append(users, s.Name)
		}
	}
	slices.Sort(pkgs)
	return slices.Compact(pkgs), users
}

// envPath is the environment folder of a set of packages; a new set gets a new folder.
func (x *Exec) envPath(projectID int64, runtime string, pkgs []string) string {
	sum := sha256.Sum256([]byte(strings.Join(pkgs, "\n")))
	return filepath.Join(x.envDir(projectID), runtime+"-"+hex.EncodeToString(sum[:6]))
}

func needs(s manifest.Script) []string {
	var out []string
	if p := interpreters[s.Runtime]; p != "" {
		out = append(out, p)
	}
	if in, ok := installers[s.Runtime]; ok && len(s.Packages) > 0 {
		out = append(out, in.tool)
	}
	return append(out, s.Programs...)
}

func (x *Exec) install(ctx context.Context, projectID int64, dir, runtime string, pkgs []string) error {
	spec := strings.Join(pkgs, "\n")
	if b, err := os.ReadFile(filepath.Join(dir, specFile)); err == nil && string(b) == spec {
		return nil
	}
	if err := x.Sandbox.Check(); err != nil {
		return fmt.Errorf("sandbox %s: %w", x.Sandbox.Name(), err)
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	root := x.envDir(projectID)
	next := dir + ".new"
	home, tmp := filepath.Join(root, ".home"), filepath.Join(root, ".tmp")
	_ = os.RemoveAll(next)
	_ = os.RemoveAll(dir)
	for _, d := range []string{home, tmp} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "TMPDIR=" + tmp, "UV_PYTHON_DOWNLOADS=never",
		"UV_CACHE_DIR=" + filepath.Join(root, ".cache", "uv"), "npm_config_cache=" + filepath.Join(root, ".cache", "npm")}
	var local []string
	for _, p := range pkgs {
		if filepath.IsAbs(p) {
			local = append(local, p)
		}
	}
	if runtime == "node" {
		if err := os.MkdirAll(next, 0o700); err != nil {
			return err
		}
	}
	for _, argv := range installers[runtime].steps(next, pkgs) {
		p, err := exec.LookPath(argv[0])
		if err != nil {
			return err
		}
		argv[0] = p
		cmd, err := x.Sandbox.Command(ctx, sandbox.Policy{Argv: argv, Dir: root, Env: env, Write: []string{root},
			Read: local, Hide: x.hidden(), Net: true})
		if err != nil {
			return err
		}
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err = cmd.Run()
		sandbox.End(cmd)
		if err != nil {
			return errors.New(strings.TrimSpace(tailOf(out.String(), 2000)) + " (" + err.Error() + ")")
		}
	}
	if err := os.WriteFile(filepath.Join(next, specFile), []byte(spec), 0o600); err != nil {
		return err
	}
	return os.Rename(next, dir)
}

func tailOf(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
