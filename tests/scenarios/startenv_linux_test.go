package scenarios

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// startEnv reads the environment a process started with, as another process of the same user sees it.
func startEnv(t *testing.T, pid int) []string {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
}
