package acp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"deck2/internal/provider"
	"deck2/internal/sandbox"
)

var semver = regexp.MustCompile(`\d+\.\d+\.\d+[0-9A-Za-z.+-]*`)

// Version is the version the agent reports, such as 1.18.20; the agent runs in the sandbox of deck for it too.
func (a *Adapter) Version(ctx context.Context) (string, error) {
	bin, err := exec.LookPath(a.cfg.Binary)
	if err != nil {
		return "", fmt.Errorf("acp: %s is not installed: %w", a.cfg.Agent, err)
	}
	if err := os.MkdirAll(a.Dir, 0o700); err != nil {
		return "", err
	}
	home, err := os.MkdirTemp(a.Dir, "version-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(home)
	cmd, err := a.cfg.Sandbox.Command(ctx, a.closed(sandbox.Policy{Argv: []string{bin, "--version"}, Dir: home,
		Env: append(environ(os.Environ()), "HOME="+home, "TMPDIR="+home), Write: []string{home}}))
	if err != nil {
		return "", err
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", a.cfg.Agent, err)
	}
	v := semver.FindString(string(out))
	if v == "" {
		return "", fmt.Errorf("%s --version printed no version: %q", a.cfg.Agent, untrustedLine(out))
	}
	return a.cfg.Agent + " " + v, nil
}

func untrustedLine(b []byte) string {
	s, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// SelfCheck runs the shell check of deck on the agent in deck's sandbox; what the model fails to do is told apart.
func (a *Adapter) SelfCheck(ctx context.Context) provider.Check {
	var c provider.Check
	if c.Version, c.Err = a.Version(ctx); c.Err != nil {
		return c
	}
	return provider.ShellCheck{Agent: a.cfg.Agent, Dir: a.Dir, Run: a.Run, Model: true,
		Done: func(session string) { _ = os.RemoveAll(a.sessionDir(session)) }}.Check(ctx, c)
}
