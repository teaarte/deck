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
	_, ok := groups(marker)[pgid]
	return ok
}

// reapMarked kills every process group one of whose members has marker in its command line.
func reapMarked(marker string) {
	for pgid := range groups(marker) {
		if pgid > 1 && pgid != syscall.Getpgrp() {
			Reap(pgid, marker)
		}
	}
}

// groups are the process groups with a member that has marker in its command line.
func groups(marker string) map[int]bool {
	out, err := exec.Command("ps", "-A", "-ww", "-o", "pgid=,args=").Output()
	if err != nil {
		return nil
	}
	found := map[int]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		id, args, _ := strings.Cut(strings.TrimSpace(line), " ")
		if n, err := strconv.Atoi(id); err == nil && strings.Contains(args, marker) {
			found[n] = true
		}
	}
	return found
}
