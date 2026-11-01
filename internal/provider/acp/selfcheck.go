package acp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"

	"deck2/internal/provider"
	"deck2/internal/release"
	"deck2/internal/sandbox"
	"deck2/internal/untrusted"
)

// Version is the version the agent reports, such as 1.18.20; the agent runs in the sandbox of deck for it too.
func (a *Adapter) Version(ctx context.Context) (string, error) {
	if _, err := sandbox.Find(a.cfg.Binary); err != nil {
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
	var out bytes.Buffer
	p, err := a.cfg.Sandbox.Run(ctx, sandbox.Box{Kind: sandbox.KindAgent, Argv: []string{a.cfg.Binary, "--version"}, Dir: home, Home: home},
		sandbox.IO{Stdin: strings.NewReader(""), Stdout: &out})
	if err != nil {
		return "", fmt.Errorf("acp: %s: %w", a.cfg.Agent, err)
	}
	if err := p.Wait(); err != nil {
		return "", fmt.Errorf("%s --version: %w", a.cfg.Agent, err)
	}
	v := release.Semver(out.String())
	if v == "" {
		return "", fmt.Errorf("%s --version printed no version: %s", a.cfg.Agent, untrusted.Line(out.String(), 200))
	}
	return a.cfg.Agent + " " + v, nil
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
