package sandbox

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Reap kills a process group an earlier deck left running, only while one of its members has marker in
// its command line: a group id reused after a reboot belongs to someone else.
func Reap(pgid int, marker string) {
	if pgid <= 0 || syscall.Kill(-pgid, 0) != nil || !holds(pgid, marker) {
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

func holds(pgid int, marker string) bool {
	out, err := exec.Command("ps", "-A", "-o", "pgid=,args=").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		id, args, _ := strings.Cut(strings.TrimSpace(line), " ")
		if id == strconv.Itoa(pgid) && strings.Contains(args, marker) {
			return true
		}
	}
	return false
}
