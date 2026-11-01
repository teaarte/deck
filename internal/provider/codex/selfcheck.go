package codex

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"deck2/internal/provider"
	"deck2/internal/release"
	"deck2/internal/sandbox"
	"deck2/internal/untrusted"
)

// Version is the version the CLI reports, such as 0.155.1.
func (a *Adapter) Version(ctx context.Context) (string, error) {
	b := a.box(0, []string{a.Binary, "--version"}, nil)
	b.Dir, b.Hosts = b.Home, nil
	var out bytes.Buffer
	p, err := a.Sandbox.Run(ctx, b, sandbox.IO{Stdin: strings.NewReader(""), Stdout: &out})
	if err == nil {
		err = p.Wait()
	}
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", a.Binary, err)
	}
	v := release.Semver(out.String())
	if v == "" {
		return "", fmt.Errorf("%s --version printed no version: %s", a.Binary, untrusted.Line(out.String(), 200))
	}
	return v, nil
}

// SelfCheck runs the shell check of deck on Codex; Codex asks deck about its own approvals.
func (a *Adapter) SelfCheck(ctx context.Context) provider.Check {
	var c provider.Check
	if c.Version, c.Err = a.Version(ctx); c.Err != nil {
		return c
	}
	return provider.ShellCheck{Agent: "codex", Kind: sandbox.KindCLI, Dir: a.Dir, Run: a.Run, PermissionMCP: provider.Qualify("approve")}.Check(ctx, c)
}

// Login is the sign-in of the CLI by a link, into the one login every home of the CLI shares.
func (a *Adapter) Login(ctx context.Context, o sandbox.IO) (*sandbox.Proc, error) {
	argv := []string{a.Binary, "login", "--device-auth"}
	b := a.box(0, argv, nil)
	if sandbox.Homed() {
		b.Home, b.Shared = a.Home.Login(cli), nil
		b.Env = []string{"CODEX_HOME=" + b.Home}
		b.Argv = []string{a.Binary, "-c", "cli_auth_credentials_store=" + quote("file"), "login", "--device-auth"}
	}
	b.Dir = b.Home
	return a.Sandbox.Run(ctx, b, o)
}
