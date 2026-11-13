package sandbox

import (
	"context"
	"os/exec"
)

// System runs a program of the system itself, outside any box, and gives what it printed: for deck's own setup of
// its machine, such as the service of a node.
func System(ctx context.Context, argv ...string) ([]byte, error) {
	return exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
}
