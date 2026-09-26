package sandbox

import (
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Reap kills a process group an earlier deck left running when its leader is gone or its command line
// holds marker: a pid reused after a reboot belongs to someone else.
func Reap(pgid int, marker string) {
	if pgid <= 0 || syscall.Kill(-pgid, 0) != nil {
		return
	}
	out, err := exec.Command("ps", "-o", "args=", "-p", strconv.Itoa(pgid)).Output()
	var exit *exec.ExitError
	switch {
	case err == nil && !strings.Contains(string(out), marker):
		return
	case err != nil && !errors.As(err, &exit):
		return
	}
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	for range 50 {
		if syscall.Kill(-pgid, 0) != nil {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
}
