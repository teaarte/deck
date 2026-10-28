package codex

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"deck2/internal/provider"
	"deck2/internal/sandbox"
)

// Version is the version the CLI reports, such as 0.155.1.
func (a *Adapter) Version(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, a.Binary, "--version")
	cmd.Env = environ()
	if err := sandbox.AsAgent(cmd, nil, nil); err != nil {
		return "", err
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", a.Binary, err)
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return "", fmt.Errorf("%s --version printed nothing", a.Binary)
	}
	return f[len(f)-1], nil
}

// SelfCheck runs the shell check of deck on Codex; Codex asks deck about its own approvals.
func (a *Adapter) SelfCheck(ctx context.Context) provider.Check {
	var c provider.Check
	if c.Version, c.Err = a.Version(ctx); c.Err != nil {
		return c
	}
	return provider.ShellCheck{Agent: "codex", Dir: a.Dir, Run: a.Run, PermissionMCP: provider.Qualify("approve")}.Check(ctx, c)
}

// Login is the sign-in of the CLI by a link, as the user its turns run as.
func (a *Adapter) Login() (*exec.Cmd, error) {
	cmd := exec.Command(a.Binary, "login", "--device-auth")
	cmd.Env = environ()
	return cmd, sandbox.AsAgent(cmd, nil, nil)
}
