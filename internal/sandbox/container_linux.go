//go:build linux

package sandbox

import (
	"context"
	"os/exec"
)

// contained is the program of a box in deck's container as it runs today: as the user of agents, so deck's state is
// closed to it by the rights of its files; its network is the container's, and the door's proxy only what it honours.
func contained(ctx context.Context, b Box, env []string) (*exec.Cmd, error) {
	argv := marked(b)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir, cmd.Env = b.Dir, env
	cmd = group(cmd)
	return cmd, AsAgent(cmd, b.Write, b.Read)
}
