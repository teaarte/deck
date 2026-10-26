package main

import (
	"os/exec"
	"syscall"
)

// hide keeps a console window from showing for a command the launcher runs.
func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
