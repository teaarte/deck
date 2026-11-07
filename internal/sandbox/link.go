package sandbox

import (
	"fmt"
	"os"
	"strings"
	"syscall"
)

// Moved is a path of a box that goes through a link a program of deck's user could have made: a folder a box writes
// turned into a link elsewhere between deck's check and the mount.
type Moved struct{ Path, Link string }

func (e *Moved) Error() string {
	return fmt.Sprintf("deck builds no box with %s: %s on its way is a link a program could have made; deck opens the folders of a box only by their real paths", e.Path, e.Link)
}

// parts are the names along a path; .. stays a name the kernel resolves, never past the root.
func parts(path string) []string {
	var out []string
	for _, p := range strings.Split(path, "/") {
		if p != "" && p != "." {
			out = append(out, p)
		}
	}
	return out
}

// rootLink says a link is one no program of deck's user could have made or changed: root's, in a folder of root's
// that only root writes, or in which only the owner of an entry may change it.
func rootLink(linkUid, dirUid, dirMode uint32) bool {
	return os.Geteuid() != 0 && linkUid == 0 && dirUid == 0 && (dirMode&0o022 == 0 || dirMode&syscall.S_ISVTX != 0)
}
