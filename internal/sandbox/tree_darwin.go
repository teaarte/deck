//go:build darwin

package sandbox

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"syscall"

	"golang.org/x/sys/unix"
)

// boxVar names in the environment of the processes of a box the box they belong to: macOS has no namespace that ends
// the processes of a box with it, and a child that leaves its group, such as by setsid, still carries it.
const boxVar = "DECK_BOX"

// descend marks the environment of cmd before it starts; the end it returns, once the program is done, kills the
// processes of the user that still carry the mark.
func descend(cmd *exec.Cmd) func() {
	if cmd.Env == nil {
		// A command without an environment of its own would take deck's whole; every box gives one.
		return func() {}
	}
	id := make([]byte, 12)
	_, _ = rand.Read(id)
	mark := boxVar + "=" + hex.EncodeToString(id)
	cmd.Env = append(cmd.Env, mark)
	return func() { killMarked(append([]byte(mark), 0)) }
}

// killMarked kills the processes of this user whose environment holds mark, the variable with its ending NUL.
func killMarked(mark []byte) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.uid", os.Getuid())
	if err != nil {
		return
	}
	for _, p := range procs {
		pid := int(p.Proc.P_pid)
		if pid == os.Getpid() {
			continue
		}
		// kern.procargs2 is the count of arguments, the path, the arguments and the environment, each ending in NUL.
		args, err := unix.SysctlRaw("kern.procargs2", pid)
		if err == nil && bytes.Contains(args, append([]byte{0}, mark...)) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
	}
}
