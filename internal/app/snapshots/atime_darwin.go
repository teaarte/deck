package snapshots

import (
	"os"
	"syscall"
	"time"
)

// readAt is when a file was last read, as far as the system keeps it.
func readAt(fi os.FileInfo) time.Time {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Atimespec.Unix())
	}
	return time.Time{}
}
