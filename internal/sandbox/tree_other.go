//go:build !darwin

package sandbox

import "os/exec"

// descend does nothing where the box is namespaces of its own: its processes end with it.
func descend(*exec.Cmd) func() { return func() {} }
