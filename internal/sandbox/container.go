package sandbox

import (
	"context"
	"net"
	"os/exec"
)

// container is deck's container as the sandbox: a program runs as the user of agents, so deck's state is closed to
// it by the rights of its files; its network is the container's, and the door's proxy only what it honours.
type container struct{}

func (container) Name() string { return "container" }

func (container) Check() error {
	_, err := theAgent()
	return err
}

func (container) Listen(string) (net.Listener, error) { return net.Listen("tcp", "127.0.0.1:0") }

func (container) Addr(ln net.Listener) string { return ln.Addr().String() }

func (container) Command(ctx context.Context, p Policy) (*exec.Cmd, error) {
	cmd := exec.CommandContext(ctx, p.Argv[0], p.Argv[1:]...)
	cmd.Dir, cmd.Env = p.Dir, p.Env
	return group(cmd), nil
}
