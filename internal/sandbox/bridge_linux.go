//go:build linux

package sandbox

import (
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"syscall"
)

// bridgeArg makes the deck binary, started inside the sandbox, carry a local port to deck's socket.
const bridgeArg = "__deck_sandbox_bridge"

func init() {
	if len(os.Args) > 1 && os.Args[1] == bridgeArg {
		os.Exit(bridge(os.Args[2:]))
	}
}

// bridge listens on a local address, carries each connection to a unix socket and runs the program.
func bridge(args []string) int {
	if len(args) < 4 || args[2] != "--" {
		return 2
	}
	sock, addr, argv := args[0], args[1], args[3:]
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		os.Stderr.WriteString("deck sandbox: " + err.Error() + "\n")
		return 2
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go carry(c, sock)
		}
	}()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			// A program a signal ended exits as a shell tells it: 128 and the signal.
			if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
				return 128 + int(ws.Signal())
			}
			return exit.ExitCode()
		}
		os.Stderr.WriteString("deck sandbox: " + err.Error() + "\n")
		return 127
	}
	return 0
}

func carry(c net.Conn, sock string) {
	defer c.Close()
	up, err := net.Dial("unix", sock)
	if err != nil {
		return
	}
	defer up.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	<-done
}
