package scenarios

import (
	"bytes"
	"encoding/binary"
	"testing"

	"golang.org/x/sys/unix"
)

// startEnv reads the environment of a process the way any program of the owner can: KERN_PROCARGS2.
func startEnv(t *testing.T, pid int) []string {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		t.Fatal(err)
	}
	argc := int(binary.LittleEndian.Uint32(raw))
	parts := bytes.Split(raw[4:], []byte{0})
	var out []string
	for _, p := range parts[1:] {
		if len(p) == 0 {
			continue
		}
		if argc > 0 {
			argc--
			continue
		}
		out = append(out, string(p))
	}
	return out
}
