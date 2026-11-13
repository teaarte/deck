package sandbox

import (
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Reap kills a process group an earlier deck left running while one of its members has marker in its
// command line, and leaves it otherwise: a group id reused after a reboot belongs to someone else.
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

// reapMarked kills the process groups one of whose members has marker in its command line; it looks again after
// each round, for a box being set up forks a process with the mark into a group of its own while the first is killed.
func reapMarked(marker string) {
	for range 5 {
		left := false
		for pgid := range groups(marker) {
			if pgid > 1 && pgid != syscall.Getpgrp() {
				left = true
				Reap(pgid, marker)
			}
		}
		if !left {
			return
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
